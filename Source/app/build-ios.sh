#!/bin/sh
# Сборка приложения под iOS. Работает только на macOS: нужен Apple SDK.
#
# Две цели, и они делают разное:
#
#   simulator — сборка под симулятор. Ей не нужна ни подпись, ни учётная
#               запись разработчика, и в симуляторе приложение реально
#               запускается и ходит в сеть. Это наш способ проверять
#               работу: другого у нас нет.
#
#   device    — сборка под iPhone. Подписать её нам нечем (для этого
#               нужна платная учётная запись Apple), поэтому на выходе
#               неподписанный .ipa. Подпись ставится потом, на стороне
#               пользователя: Sideloadly под Windows делает это сам по
#               обычному Apple ID, срок жизни такой подписи — 7 дней.
#
# После упаковки в Info.plist добавляется режим фоновой работы «audio».
# Fyne свой Info.plist собирает по жёсткому шаблону и подставить туда
# ничего не даёт, поэтому правим готовый — до подписи, иначе подпись
# сломается. Без этой строки удержание в фоне (keepalive_ios.m) не
# работает: система просто усыпит приложение.
#
# Запуск:  ./build-ios.sh simulator | ./build-ios.sh device
set -e

APP_VERSION=0.0.4
APP_BUILD=4
APP_ID=com.vkandreevich.torlocalproxy
APP_NAME=TorLocalProxy

TARGET=${1:-simulator}
case "$TARGET" in
  simulator) FYNE_TARGET=iossimulator ;;
  device)    FYNE_TARGET=ios ;;
  *) echo "неизвестная цель: $TARGET (simulator или device)" >&2; exit 1 ;;
esac

cd "$(dirname "$0")"
ROOT=$(cd ../.. && pwd)

if [ "$(uname)" != "Darwin" ]; then
  echo "Собрать под iOS можно только на macOS: нужен Apple SDK." >&2
  echo "Локально этого не сделать — сборка идёт в GitHub Actions," >&2
  echo "рабочий процесс .github/workflows/build-ios.yml." >&2
  exit 1
fi

# 1. Приносим статический tor нужного среза.
./fetch-tor-ios.sh "$TARGET"

# 2. Собираем приложение.
#
# fyne требует сертификат подписи в связке ключей даже для симулятора:
# из него он берёт код команды разработчика, без которого не соберёт
# проект Xcode вовсе. Настоящего сертификата у нас нет, поэтому в CI
# подставляется самоподписанный, а сама подпись отключается через
# xcconfig — этим занимается ci-fake-cert.sh. Имя сертификата приходит
# в FYNE_CERT; без этой переменной fyne ищет обычный «iPhone Developer».
echo "Собираю под $FYNE_TARGET..."
rm -rf "$APP_NAME.app"
if [ -n "$FYNE_CERT" ]; then
  set -- --cert "$FYNE_CERT"
else
  set --
fi
fyne package --target "$FYNE_TARGET" \
  --app-version "$APP_VERSION" --app-build "$APP_BUILD" \
  --icon Icon.png --app-id "$APP_ID" --name "$APP_NAME" "$@"

APP="$APP_NAME.app"
if [ ! -d "$APP" ]; then
  echo "Fyne не оставил $APP — смотрите вывод выше" >&2
  exit 1
fi

# 3. Дописываем режим фоновой работы.
echo "Вписываю UIBackgroundModes в Info.plist..."
plutil -replace UIBackgroundModes -json '["audio"]' "$APP/Info.plist"
# Заодно оставляем в свойствах пояснение: зачем приложению звук. Его
# видно в настройках iOS, и без объяснения это выглядит странно.
plutil -replace NSMicrophoneUsageDescription \
  -string "Звук не записывается. Беззвучное воспроизведение нужно только для того, чтобы система не выключала прокси, пока приложение не на экране." \
  "$APP/Info.plist"
plutil -p "$APP/Info.plist" | grep -A 3 UIBackgroundModes

# 4. Для симулятора на этом всё: подпись ad-hoc уже поставил Fyne.
if [ "$TARGET" = "simulator" ]; then
  # Правка Info.plist сломала прежнюю подпись — ставим заново.
  codesign --force --sign - "$APP"
  echo "Готово: $APP (для симулятора)"
  exit 0
fi

# 5. Для устройства собираем .ipa: это zip с папкой Payload внутри.
echo "Упаковываю .ipa..."
BUNDLE="$APP_NAME-$APP_VERSION-ios"
rm -rf "$BUNDLE" "$BUNDLE.ipa"
mkdir -p "$BUNDLE/Payload"
cp -R "$APP" "$BUNDLE/Payload/"
(cd "$BUNDLE" && zip -qr "../$BUNDLE.ipa" Payload)
rm -rf "$BUNDLE"

mkdir -p "$ROOT/Release"
mv "$BUNDLE.ipa" "$ROOT/Release/$BUNDLE.ipa"
echo "Готово: Release/$BUNDLE.ipa (без подписи)"
echo
echo "Поставить на iPhone с Windows: Sideloadly, обычный Apple ID."
echo "Подпись живёт 7 дней, потом переподписать тем же способом."
