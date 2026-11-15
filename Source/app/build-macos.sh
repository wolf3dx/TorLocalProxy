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

  # Склеиваем всё, что лежит в MacOS и libs. Имена в обоих пакетах
  # обязаны совпадать — они берутся из одних и тех же формул Homebrew.
  # Не совпали — значит на машинах оказались разные версии библиотек, и
  # молча отдавать половинчатый пакет нельзя.
  for file in "$APP_NAME.app/Contents/MacOS"/* "$APP_NAME.app/Contents/libs"/*; do
    [ -f "$file" ] || continue
    rel=${file#"$APP_NAME.app/"}
    pair="$second/$rel"
    if [ ! -f "$pair" ]; then
      echo "во втором пакете нет $rel — склеить нечем" >&2
      echo "Чаще всего это значит, что половины собраны на разных версиях" >&2
      echo "macOS: Homebrew даёт на них разные библиотеки. Вот что есть" >&2
      echo "у каждой половины:" >&2
      echo "--- первая ($first) ---" >&2
      ls "$first/Contents/libs" >&2 || true
      echo "--- вторая ($second) ---" >&2
      ls "$second/Contents/libs" >&2 || true
      exit 1
    fi
    # lipo ругается на то, что не Mach-O (скажем, на текстовый файл).
    # Такое пропускаем, но говорим вслух.
    if ! lipo -info "$file" >/dev/null 2>&1; then
      echo "  пропускаю (не бинарник): $rel"
      continue
    fi
    lipo -create "$file" "$pair" -output "$file.universal"
    mv "$file.universal" "$file"
    chmod +x "$file"
    echo "  склеен: $rel -> $(lipo -archs "$file")"
  done

  # Проверка: в пакете не должно остаться ни одного файла с одной
  # архитектурой. Это и есть главный признак универсальности, и поймать
  # его лучше здесь, чем получить жалобу от владельца Intel-мака.
  echo "Проверяю, что все бинарники универсальны..."
  lonely=0
  for file in "$APP_NAME.app/Contents/MacOS"/* "$APP_NAME.app/Contents/libs"/*; do
    [ -f "$file" ] || continue
    lipo -info "$file" >/dev/null 2>&1 || continue
    archs=$(lipo -archs "$file")
    case "$archs" in
      *arm64*x86_64*|*x86_64*arm64*) : ;;
      *) echo "  ТОЛЬКО $archs: ${file#"$APP_NAME.app/"}" >&2; lonely=$((lonely+1)) ;;
    esac
  done
  if [ "$lonely" != "0" ]; then
    echo "Универсального пакета не вышло: файлов с одной архитектурой — $lonely." >&2
    exit 1
  fi
  echo "Все бинарники несут обе архитектуры."
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
