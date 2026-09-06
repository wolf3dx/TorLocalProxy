#!/bin/sh
# Сборка приложения для Windows.
#
# Рядом с исполняемым файлом кладётся tor.exe: приложение ищет его в
# своём каталоге первым делом (см. core/torrun/external, SearchPaths).
# Так поставка не зависит от установленного Tor Browser — пользователь
# распаковывает архив и запускает.
#
# tor.exe от Tor Project собран статически: одна программа на десять
# мегабайт без единой библиотеки рядом. Берётся он из готовой поставки в
# таком порядке:
#   1) переменная TOR_BINARY, если указана;
#   2) Tor Browser, установленный на этой машине;
#   3) отдельно распакованный Tor Expert Bundle в tor-windows/.
#
# Скачивать его сам скрипт не пытается: torproject.org доступен не из
# каждой сети, и молчаливый отказ посреди сборки хуже внятного вопроса.
#
# Запуск:  ./build-windows.sh
set -e

APP_VERSION=0.0.4
APP_BUILD=4

cd "$(dirname "$0")"
ROOT=$(cd ../.. && pwd)

# 1. Ищем tor.exe.
find_tor() {
  if [ -n "$TOR_BINARY" ] && [ -f "$TOR_BINARY" ]; then
    echo "$TOR_BINARY"
    return
  fi
  for candidate in \
    "tor-windows/tor.exe" \
    "$USERPROFILE/Desktop/Tor Browser/Browser/TorBrowser/Tor/tor.exe" \
    "$LOCALAPPDATA/Tor Browser/Browser/TorBrowser/Tor/tor.exe" \
    "/c/Program Files/Tor Browser/Browser/TorBrowser/Tor/tor.exe"
  do
    if [ -f "$candidate" ]; then
      echo "$candidate"
      return
    fi
  done
}

TOR=$(find_tor)
if [ -z "$TOR" ]; then
  echo "tor.exe не найден." >&2
  echo "Укажите путь в TOR_BINARY, положите файл в tor-windows/tor.exe" >&2
  echo "или установите Tor Browser." >&2
  exit 1
fi
echo "tor: $TOR"

# 2. Собираем приложение. Fyne вписывает в exe иконку и данные о версии.
echo "Собираю приложение..."
fyne package --target windows \
  --app-version "$APP_VERSION" --app-build "$APP_BUILD" \
  --icon Icon.png --app-id com.vkandreevich.torlocalproxy --name TorLocalProxy

# 3. Собираем папку поставки.
BUNDLE="TorLocalProxy-$APP_VERSION-windows-x86_64"
rm -rf "$BUNDLE" && mkdir -p "$BUNDLE"
mv TorLocalProxy.exe "$BUNDLE/"
cp "$TOR" "$BUNDLE/tor.exe"
cp "$ROOT/Release/TorLocalProxy-$APP_VERSION-manual.pdf" "$BUNDLE/Руководство.pdf" 2>/dev/null || true

cat > "$BUNDLE/ЧИТАТЬ.txt" <<'TEXT'
TorLocalProxy — мини-прокси для сети Tor без браузера.

Запуск: TorLocalProxy.exe

Устанавливать ничего не нужно, всё лежит в этой папке. Рядом с
приложением находится tor.exe от Tor Project — приложение запускает его
само, отдельно открывать не надо.

Прокси поднимается на 127.0.0.1:9050. Этот адрес вписывается в настройки
тех программ, которые нужно пропустить через Tor: тип прокси SOCKS5,
логин и пароль не нужны.

Закрытие окна не выключает прокси: приложение уходит в значок рядом с
часами и продолжает работать. Полностью выйти — из меню этого значка.

Подробности и разбор отказов — в Руководстве.pdf.
TEXT

# 4. Кладём архив в Release.
echo "Упаковываю..."
mkdir -p "$ROOT/Release"
rm -f "$ROOT/Release/$BUNDLE.zip"
python -c "
import os, sys, zipfile
папка, архив = sys.argv[1], sys.argv[2]
with zipfile.ZipFile(архив, 'w', zipfile.ZIP_DEFLATED) as z:
    for корень, _, файлы in os.walk(папка):
        for имя in файлы:
            полный = os.path.join(корень, имя)
            z.write(полный, os.path.relpath(полный, os.path.dirname(папка)))
" "$BUNDLE" "$ROOT/Release/$BUNDLE.zip"
rm -rf "$BUNDLE"

echo "Готово: Release/$BUNDLE.zip"
