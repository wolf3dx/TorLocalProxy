#!/bin/sh
# Готовит macOS-раннер к сборке под iOS без учётной записи Apple.
#
# Загвоздка вот в чём: fyne читает код команды разработчика из
# сертификата подписи в связке ключей и без него не начинает сборку
# вовсе — даже под симулятор, где подпись не нужна. Настоящего
# сертификата у нас нет и быть не может: он выдаётся только по платной
# подписке Apple Developer.
#
# Поэтому делаем два дела:
#   1) создаём самоподписанный сертификат с именем «iPhone Developer»
#      и произвольным кодом команды — fyne удовлетворён;
#   2) отключаем саму подпись через xcconfig. xcodebuild читает файл,
#      указанный в XCODE_XCCONFIG_FILE, и наши настройки перекрывают
#      всё, что fyne вписал в проект.
#
# На выходе — неподписанное приложение. Для симулятора этого достаточно
# (fyne ставит ad-hoc подпись сам), а пакет для телефона подписывается
# уже у пользователя: Sideloadly делает это по обычному Apple ID.
#
# Скрипт печатает строки для GITHUB_ENV, если переменная задана.
set -e

KEYCHAIN=torproxy-ci.keychain
PASSWORD=torproxy-ci
CERT_NAME="iPhone Developer: TorLocalProxy CI"
# Код команды у Apple — десять знаков. Здесь он выдуманный: его
# единственное назначение — попасть в настройку DEVELOPMENT_TEAM,
# которая при отключённой подписи ни на что не влияет.
TEAM_ID=TORPROXYCI

WORK=$(mktemp -d)
cd "$WORK"

echo "Делаю самоподписанный сертификат..."
openssl req -x509 -newkey rsa:2048 -sha256 -days 30 -nodes \
  -keyout ci.key -out ci.crt \
  -subj "/CN=$CERT_NAME/OU=$TEAM_ID/O=TorLocalProxy CI/C=US" \
  -addext extendedKeyUsage=codeSigning >/dev/null 2>&1
openssl pkcs12 -export -out ci.p12 -inkey ci.key -in ci.crt \
  -passout "pass:$PASSWORD" >/dev/null 2>&1

echo "Кладу его в отдельную связку ключей..."
security delete-keychain "$KEYCHAIN" 2>/dev/null || true
security create-keychain -p "$PASSWORD" "$KEYCHAIN"
security unlock-keychain -p "$PASSWORD" "$KEYCHAIN"
# Срок жизни связки без пароля: по умолчанию она запирается через пять
# минут, а сборка идёт дольше.
security set-keychain-settings -t 7200 -u "$KEYCHAIN"
security import ci.p12 -k "$KEYCHAIN" -P "$PASSWORD" \
  -T /usr/bin/codesign -T /usr/bin/security
security set-key-partition-list -S apple-tool:,apple:,codesign: \
  -s -k "$PASSWORD" "$KEYCHAIN" >/dev/null
# Своя связка должна искаться первой, но login.keychain из списка
# убирать нельзя: в ней лежат сертификаты самого Xcode.
security list-keychains -d user -s "$KEYCHAIN" login.keychain
security find-certificate -c "iPhone Developer" -p >/dev/null

echo "Готовлю xcconfig, отключающий подпись..."
XCCONFIG="$WORK/no-signing.xcconfig"
cat > "$XCCONFIG" <<'CONF'
CODE_SIGNING_ALLOWED = NO
CODE_SIGNING_REQUIRED = NO
CODE_SIGN_IDENTITY =
CODE_SIGN_ENTITLEMENTS =
ENABLE_BITCODE = NO
CONF

echo "Готово. Сертификат: $CERT_NAME"
if [ -n "$GITHUB_ENV" ]; then
  echo "FYNE_CERT=$CERT_NAME" >> "$GITHUB_ENV"
  echo "XCODE_XCCONFIG_FILE=$XCCONFIG" >> "$GITHUB_ENV"
  echo "Переменные FYNE_CERT и XCODE_XCCONFIG_FILE переданы дальше."
else
  echo "export FYNE_CERT=\"$CERT_NAME\""
  echo "export XCODE_XCCONFIG_FILE=\"$XCCONFIG\""
fi
