#!/bin/sh
# Сборка приложения под macOS. Работает только на macOS: нужен Apple SDK
# и codesign. Локально у нас Mac-а нет — сборка идёт в GitHub Actions,
# рабочий процесс .github/workflows/build-macos.yml.
#
# Имена переменных и функций здесь только латиницей: /bin/sh других не
# принимает. Правило уже оплачено кровью — на кириллическом имени
# переменной падала сборка Linux (dash: «Bad for loop variable»).
#
# Что здесь происходит, кроме упаковки:
#
#   1. tor кладётся ВНУТРЬ .app. На десктопе приложение поднимает tor
#      отдельным процессом (core/torrun/external) и ищет его в том числе
#      рядом с собой — SearchPaths проверяет Contents/MacOS/tor. Так
#      поставка не зависит от установленного Tor Browser.
#
#   2. tor от Homebrew динамически слинкован (libevent, openssl@3, …).
#      В отрыве от Homebrew эти dylib недоступны, поэтому dylibbundler
#      копирует их в Contents/libs и переписывает пути загрузки на
#      @executable_path/../libs. Без этого tor на чужом Mac не стартует.
#
#   3. ad-hoc подпись всего .app (codesign --sign -). На Apple Silicon
#      неподписанный исполняемый файл система убивает при запуске —
#      значит и вложенный tor, и dylib нужно подписать. Подпись ad-hoc
#      бесплатна и учётной записи Apple не требует; Gatekeeper при
#      скачивании всё равно попросит «Открыть» из меню правой кнопки —
#      это карантин, а не подпись.
#
# ---------------------------------------------------------------------
# Про универсальный пакет (две архитектуры в одном файле).
#
# Маков сейчас два вида: на процессорах Apple (arm64) и прежние на Intel
# (x86_64). Программа, собранная под один вид, на другом либо не
# запустится вовсе, либо пойдёт через Rosetta — медленнее и не всегда.
#
# В macOS есть «толстый» формат: в одном файле лежат обе версии кода
# сразу, а система при запуске берёт подходящую. Склеивает их lipo — она
# же их и разбирает обратно. Но склеить надо НЕ ТОЛЬКО нашу программу:
# внутри пакета лежит ещё tor и десяток его библиотек, и они тоже
# собраны под одну архитектуру. Пакет универсален ровно настолько,
# насколько универсален самый «узкий» файл внутри него.
#
# Собрать обе половины на одной машине нельзя: tor и его библиотеки
# берутся готовыми у Homebrew, а Homebrew ставит их под ту систему, на
# которой работает. Поэтому половины собираются на двух разных машинах
# GitHub, а потом склеиваются. Отсюда и режимы запуска.
#
# Запуск:
#   ./build-macos.sh app            — собрать .app под эту машину
#   ./build-macos.sh universal A B  — склеить два .app в универсальный
#   ./build-macos.sh pack           — подписать готовый .app и сделать .dmg
#   ./build-macos.sh                — всё сразу, под одну архитектуру
#                                     (так собирают вручную на своём Mac)
set -e

APP_VERSION=0.0.12
APP_BUILD=11
APP_ID=com.vkandreevich.torlocalproxy
APP_NAME=TorLocalProxy

MODE=${1:-all}

cd "$(dirname "$0")"
ROOT=$(cd ../.. && pwd)

if [ "$(uname)" != "Darwin" ]; then
  echo "Собрать под macOS можно только на macOS: нужен Apple SDK." >&2
  echo "Локально этого не сделать — сборка идёт в GitHub Actions," >&2
  echo "рабочий процесс .github/workflows/build-macos.yml." >&2
  exit 1
fi

MACOS_DIR="$APP_NAME.app/Contents/MacOS"
LIBS_DIR="$APP_NAME.app/Contents/libs"

# =====================================================================
# Режим app: собрать пакет под архитектуру этой машины.
# =====================================================================
build_app() {
  # 1. Ищем tor. Готовый бинарник берём у Homebrew (те же зависимости,
  # что и у Tor Browser, но пакетом). TOR_BINARY позволяет указать свой.
  TOR="$TOR_BINARY"
  if [ -z "$TOR" ]; then
    for candidate in "$(brew --prefix 2>/dev/null)/bin/tor" /opt/homebrew/bin/tor /usr/local/bin/tor; do
      if [ -x "$candidate" ]; then TOR="$candidate"; break; fi
    done
  fi
  if [ -z "$TOR" ]; then
    echo "tor не найден. Установите: brew install tor (или задайте TOR_BINARY)." >&2
    exit 1
  fi
  echo "tor: $TOR"

  # 2. Собираем .app. Fyne сам делает Contents/MacOS/<name>, Info.plist
  # и иконку.
  echo "Собираю приложение..."
  rm -rf "$APP_NAME.app"
  fyne package --target darwin \
    --app-version "$APP_VERSION" --app-build "$APP_BUILD" \
    --icon Icon.png --app-id "$APP_ID" --name "$APP_NAME"

  # 3. Вкладываем tor рядом с исполняемым файлом приложения.
  echo "Вкладываю tor..."
  cp "$TOR" "$MACOS_DIR/tor"
  chmod +x "$MACOS_DIR/tor"

  # 4. Приносим зависимости tor внутрь пакета и переписываем пути.
  echo "Собираю зависимости tor (dylibbundler)..."
  dylibbundler --overwrite-dir --bundle-deps --create-dir \
    --fix-file "$MACOS_DIR/tor" \
    --dest-dir "$LIBS_DIR" \
    --install-path "@executable_path/../libs/"

  echo "Пакет собран под $(uname -m)."
}

# =====================================================================
# Режим universal: склеить два пакета в один.
#
# За основу берётся первый: из него остаются Info.plist, иконка и всё
# прочее содержимое, одинаковое для обеих архитектур. Заменяются только
# исполняемые файлы и библиотеки — каждый склеивается со своей парой из
# второго пакета.
# =====================================================================
build_universal() {
  first=$1
  second=$2
  if [ ! -d "$first" ] || [ ! -d "$second" ]; then
    echo "Нужны два готовых пакета: ./build-macos.sh universal A.app B.app" >&2
    exit 1
  fi

  echo "Склеиваю $first + $second"
  rm -rf "$APP_NAME.app"
  cp -R "$first" "$APP_NAME.app"

  # --- Исполняемые файлы. ---------------------------------------------
  # Их имена в обеих половинах одинаковы (приложение и tor), и каждый
  # обязан стать универсальным: именно их запускает система.
  for file in "$APP_NAME.app/Contents/MacOS"/*; do
    [ -f "$file" ] || continue
    rel=${file#"$APP_NAME.app/"}
    pair="$second/$rel"
    if [ ! -f "$pair" ]; then
      echo "во второй половине нет $rel — склеить нечем" >&2
      exit 1
    fi
    lipo -info "$file" >/dev/null 2>&1 || continue
    lipo -create "$file" "$pair" -output "$file.universal"
    mv "$file.universal" "$file"
    chmod +x "$file"
    echo "  склеен: $rel -> $(lipo -archs "$file")"
  done

  # --- Библиотеки. ----------------------------------------------------
  # Здесь хитрее. Homebrew собирает tor для Intel и для Apple с разными
  # версиями openssl: одной нужен libcrypto.3.dylib, другой —
  # libcrypto.4.dylib. Склеить их нельзя, да и не нужно: имена разные,
  # поэтому обе лягут рядом, и каждая половина tor при запуске попросит
  # свою. Библиотеки с совпадающими именами (libevent, libscrypt)
  # склеиваем как обычно — их просит и та половина, и другая.
  for file in "$second/Contents/libs"/*; do
    [ -f "$file" ] || continue
    name=$(basename "$file")
    mine="$APP_NAME.app/Contents/libs/$name"
    if [ -f "$mine" ]; then
      lipo -create "$mine" "$file" -output "$mine.universal"
      mv "$mine.universal" "$mine"
      echo "  склеена: libs/$name -> $(lipo -archs "$mine")"
    else
      cp "$file" "$mine"
      echo "  добавлена: libs/$name ($(lipo -archs "$mine")) — своя для второй половины"
    fi
  done

  # --- Проверка. ------------------------------------------------------
  # Мало убедиться, что файлы на месте: надо знать, что КАЖДАЯ половина
  # найдёт всё, что просит. Для этого спрашиваем у самого tor отдельно
  # по каждой архитектуре, какие библиотеки ему нужны, и проверяем, что
  # такой файл есть и что в нём есть эта архитектура.
  echo "Проверяю, что обе половины укомплектованы..."
  for arch in arm64 x86_64; do
    for file in "$APP_NAME.app/Contents/MacOS"/*; do
      [ -f "$file" ] || continue
      lipo -info "$file" >/dev/null 2>&1 || continue
      case "$(lipo -archs "$file")" in
        *"$arch"*) : ;;
        *) echo "  $arch: в ${file#"$APP_NAME.app/"} нет этой архитектуры" >&2; exit 1 ;;
      esac
      otool -arch "$arch" -L "$file" 2>/dev/null         | awk '/@executable_path\/\.\.\/libs\//{print $1}'         | sed 's|@executable_path/\.\./libs/||'         | while read -r need; do
            [ -n "$need" ] || continue
            lib="$APP_NAME.app/Contents/libs/$need"
            if [ ! -f "$lib" ]; then
              echo "  $arch: ${file#"$APP_NAME.app/"} просит $need, а его нет" >&2
              exit 1
            fi
            case "$(lipo -archs "$lib")" in
              *"$arch"*) : ;;
              *) echo "  $arch: $need есть, но без этой архитектуры" >&2; exit 1 ;;
            esac
          done || exit 1
    done
    echo "  $arch: всё на месте"
  done
}

# =====================================================================
# Режим pack: подписать готовый пакет и завернуть в .dmg.
# =====================================================================
build_dmg() {
  if [ ! -d "$APP_NAME.app" ]; then
    echo "Нет $APP_NAME.app — сначала соберите или склейте пакет." >&2
    exit 1
  fi

  # 5. Подписываем ad-hoc: иначе Apple Silicon не запустит вложенные
  # бинарники. --deep охватывает tor и все dylib. Подписывать надо ПОСЛЕ
  # склейки: lipo переписывает файл и прежнюю подпись стирает.
  echo "Подписываю ad-hoc..."
  codesign --force --deep --sign - "$APP_NAME.app"
  codesign --verify --deep --strict "$APP_NAME.app" && echo "подпись на месте"

  # 5.1. Проверяем, что вложенный tor реально запускается: если
  # dylibbundler, склейка или подпись что-то сломали, это всплывёт здесь,
  # в сборке, а не у пользователя. tor --version печатает версию и
  # выходит. Запускается половина под архитектуру этой машины — за
  # вторую отвечает проверка архитектур выше.
  echo "Проверяю вложенный tor..."
  "$MACOS_DIR/tor" --version

  # 6. Пакуем в .dmg с ярлыком на Applications — привычная установка
  # перетаскиванием.
  echo "Собираю .dmg..."
  BUNDLE="TorLocalProxy-$APP_VERSION-macos"
  rm -rf .dmgroot "$ROOT/Release/$BUNDLE.dmg"
  mkdir -p .dmgroot
  cp -R "$APP_NAME.app" .dmgroot/
  ln -s /Applications .dmgroot/Applications
  mkdir -p "$ROOT/Release"
  hdiutil create -volname "$APP_NAME" -srcfolder .dmgroot \
    -ov -format UDZO "$ROOT/Release/$BUNDLE.dmg"
  rm -rf .dmgroot "$APP_NAME.app"

  # 7. Контрольная сумма — её проверяет встроенное обновление.
  shasum -a 256 "$ROOT/Release/$BUNDLE.dmg" \
    | awk -v n="$BUNDLE.dmg" '{print $1 "  " n}' \
    > "$ROOT/Release/$BUNDLE.dmg.sha256"

  echo "Готово: Release/$BUNDLE.dmg (+ .sha256)"
}

case "$MODE" in
  app)       build_app ;;
  universal) build_universal "$2" "$3" ;;
  pack)      build_dmg ;;
  all)       build_app; build_dmg ;;
  *) echo "неизвестный режим: $MODE (app | universal A B | pack | all)" >&2; exit 1 ;;
esac
