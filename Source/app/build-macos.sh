#!/bin/sh
# Сборка приложения под macOS. Работает только на macOS: нужен Apple SDK
# и codesign. Локально у нас Mac-а нет — сборка идёт в GitHub Actions,
# рабочий процесс .github/workflows/build-macos.yml.
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
# Запуск:  ./build-macos.sh
set -e

APP_VERSION=0.0.6
APP_BUILD=6
APP_ID=com.vkandreevich.torlocalproxy
APP_NAME=TorLocalProxy

cd "$(dirname "$0")"
ROOT=$(cd ../.. && pwd)

if [ "$(uname)" != "Darwin" ]; then
  echo "Собрать под macOS можно только на macOS: нужен Apple SDK." >&2
  echo "Локально этого не сделать — сборка идёт в GitHub Actions," >&2
  echo "рабочий процесс .github/workflows/build-macos.yml." >&2
  exit 1
fi

# 1. Ищем tor. Готовый бинарник берём у Homebrew (те же зависимости, что
# и у Tor Browser, но пакетом). TOR_BINARY позволяет указать свой.
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

# 2. Собираем .app. Fyne сам делает Contents/MacOS/<name>, Info.plist и
# иконку.
echo "Собираю приложение..."
rm -rf "$APP_NAME.app"
fyne package --target darwin \
  --app-version "$APP_VERSION" --app-build "$APP_BUILD" \
  --icon Icon.png --app-id "$APP_ID" --name "$APP_NAME"

MACOS_DIR="$APP_NAME.app/Contents/MacOS"
LIBS_DIR="$APP_NAME.app/Contents/libs"

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

# 5. Подписываем ad-hoc: иначе Apple Silicon не запустит вложенные
# бинарники. --deep охватывает tor и все dylib.
echo "Подписываю ad-hoc..."
codesign --force --deep --sign - "$APP_NAME.app"
codesign --verify --deep --strict "$APP_NAME.app" && echo "подпись на месте"

# 5.1. Проверяем, что вложенный tor реально запускается: если
# dylibbundler или подпись что-то сломали, это всплывёт здесь, в сборке,
# а не у пользователя. tor --version печатает версию и выходит.
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
