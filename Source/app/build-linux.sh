#!/bin/sh
# Сборка приложения под Linux. Работает только на Linux: Fyne тянет
# системные библиотеки OpenGL и X11 через cgo, и подменить их с другой
# системы нечем. Локально у нас Linux-машины нет — сборка идёт в GitHub
# Actions, рабочий процесс .github/workflows/build-linux.yml.
#
# На выходе два файла на каждую архитектуру:
#
#   .tar.gz — распаковал и запустил, ничего не устанавливая;
#   .deb    — обычная установка в /opt с ярлыком в меню.
#
# Архитектура берётся у самой машины: собрать под arm64 с x86_64 здесь
# нельзя по той же причине, по которой нельзя собрать с Windows. В CI
# для этого отдельный раннер.
#
# Что происходит с tor, кроме копирования:
#
#   1. tor кладётся РЯДОМ с приложением. Приложение ищет его в своём
#      каталоге первым делом (core/torrun/external, SearchPaths), так
#      что поставка не зависит от того, установлен ли tor в системе.
#
#   2. tor из пакета дистрибутива слинкован динамически (libssl,
#      libevent, liblzma и прочее). На чужой машине этих библиотек может
#      не быть или они могут оказаться другой версии, поэтому нужные
#      копируются в libs/ рядом, а самому tor через patchelf
#      прописывается rpath $ORIGIN/libs. Это Linux-ответ тому, что на
#      macOS делает dylibbundler.
#
#      Библиотеки самой системы (libc, libm, libpthread, ld-linux и
#      прочее из glibc) не копируются нарочно: они обязаны быть той
#      версии, что у ядра и загрузчика на машине пользователя, и
#      подмена их чужими копиями ломает запуск надёжнее, чем их
#      отсутствие.
#
# Что нужно в системе, где эта сборка идёт:
#
#   gcc pkg-config patchelf dpkg-dev tor
#   libgl1-mesa-dev xorg-dev libxkbcommon-dev
#   libwayland-dev wayland-protocols libwayland-bin libdecor-0-dev libegl-dev
#
# Заголовки обеих оконных подсистем, X11 и Wayland, нужны вместе: glfw в
# Fyne 2.8 собирает их разом, а не ту, что выбрана тегом сборки.
#
# Запуск:  ./build-linux.sh
set -e

APP_VERSION=0.0.10
APP_ID=com.vkandreevich.torlocalproxy
APP_NAME=TorLocalProxy

cd "$(dirname "$0")"
ROOT=$(cd ../.. && pwd)

if [ "$(uname)" != "Linux" ]; then
  echo "Собрать под Linux можно только на Linux: Fyne требует cgo и" >&2
  echo "системных библиотек OpenGL/X11." >&2
  echo "Сборка идёт в GitHub Actions, .github/workflows/build-linux.yml." >&2
  exit 1
fi

# Имя архитектуры — то же, что в именах пакетов: x86_64 и arm64.
case "$(uname -m)" in
  x86_64)          ARCH=x86_64; DEB_ARCH=amd64 ;;
  aarch64|arm64)   ARCH=arm64;  DEB_ARCH=arm64 ;;
  *) echo "незнакомая архитектура: $(uname -m)" >&2; exit 1 ;;
esac
echo "архитектура: $ARCH"

# 1. Ищем tor. Готовый бинарник берём у дистрибутива (apt install tor);
# TOR_BINARY позволяет указать свой.
TOR="$TOR_BINARY"
if [ -z "$TOR" ]; then
  for candidate in "$(command -v tor 2>/dev/null)" /usr/bin/tor /usr/sbin/tor; do
    if [ -n "$candidate" ] && [ -x "$candidate" ]; then TOR="$candidate"; break; fi
  done
fi
if [ -z "$TOR" ]; then
  echo "tor не найден. Установите: apt-get install -y tor (или задайте TOR_BINARY)." >&2
  exit 1
fi
echo "tor: $TOR"

command -v patchelf >/dev/null 2>&1 || {
  echo "нет patchelf — без него tor не найдёт вложенные библиотеки." >&2
  echo "Установите: apt-get install -y patchelf" >&2
  exit 1
}

# 2. Собираем приложение.
#
# fyne package здесь не зовём: под Linux он делает архив с деревом
# /usr/local, которое нам всё равно пришлось бы разбирать. Иконку и
# ярлык кладём сами — ниже.
echo "Собираю приложение..."
CGO_ENABLED=1 go build -trimpath -ldflags "-s -w" -o "$APP_NAME" .

# 3. Собираем папку поставки.
BUNDLE="$APP_NAME-$APP_VERSION-linux-$ARCH"
rm -rf "$BUNDLE" && mkdir -p "$BUNDLE/libs"
mv "$APP_NAME" "$BUNDLE/"
cp "$TOR" "$BUNDLE/tor"
chmod +x "$BUNDLE/$APP_NAME" "$BUNDLE/tor"
cp Icon.png "$BUNDLE/$APP_NAME.png"

# 4. Приносим зависимости tor внутрь поставки.
#
# ldd печатает «библиотека => путь (адрес)»; берём вторую колонку и
# отсеиваем то, что принадлежит самой системе.
echo "Собираю зависимости tor..."
ldd "$BUNDLE/tor" | awk '{print $3}' | grep '^/' | while read -r lib; do
  case "$(basename "$lib")" in
    libc.so.*|libm.so.*|libpthread.so.*|libdl.so.*|librt.so.*|ld-linux*|libgcc_s.so.*)
      continue ;;
  esac
  cp -n "$lib" "$BUNDLE/libs/" || true
done
patchelf --set-rpath '$ORIGIN/libs' "$BUNDLE/tor"
echo "вложено библиотек: $(ls -1 "$BUNDLE/libs" | wc -l)"

# 4.1. Проверяем, что вложенный tor реально запускается: если копирование
# или patchelf что-то сломали, это всплывёт здесь, в сборке, а не у
# пользователя. Проверять надо в отрыве от системных путей, иначе
# подмена пройдёт незамеченной — отсюда пустой LD_LIBRARY_PATH.
echo "Проверяю вложенный tor..."
( cd "$BUNDLE" && env -u LD_LIBRARY_PATH ./tor --version )

cat > "$BUNDLE/ЧИТАТЬ.txt" <<'TEXT'
TorLocalProxy — мини-прокси для сети Tor без браузера.

Запуск: ./TorLocalProxy

Устанавливать ничего не нужно, всё лежит в этой папке. Рядом с
приложением находится tor со своими библиотеками — приложение запускает
его само, отдельно ставить tor не надо.

Прокси поднимается на 127.0.0.1:9050. Этот адрес вписывается в настройки
тех программ, которые нужно пропустить через Tor: тип прокси SOCKS5,
логин и пароль не нужны. Рядом работает HTTP-прокси на 127.0.0.1:8118 —
для программ, которые знают только его.

Закрытие окна не выключает прокси: приложение уходит в значок рядом с
часами и продолжает работать. Полностью выйти — из меню этого значка.
Если значков в трее в вашей оболочке нет, закрытие окна означает выход.

Запуск вместе с системой включается в самом приложении: шестерёнка в
правом верхнем углу → «Запускать при входе в систему». Приложение
пропишет себя в ~/.config/autostart — там же его можно и отключить.

Подробности и разбор отказов — в Руководстве.pdf.
TEXT
cp "$ROOT/Release/TorLocalProxy-$APP_VERSION-manual.pdf" "$BUNDLE/Руководство.pdf" 2>/dev/null || true

mkdir -p "$ROOT/Release"

# 5. Портативный архив.
echo "Упаковываю .tar.gz..."
rm -f "$ROOT/Release/$BUNDLE.tar.gz"
tar -czf "$ROOT/Release/$BUNDLE.tar.gz" "$BUNDLE"

# 6. Пакет .deb: то же самое, но с ярлыком в меню и командой в PATH.
#
# Кладём в /opt, а не в /usr/bin с библиотеками по системным путям:
# поставка самодостаточна, и растаскивать её по дереву значило бы
# смешивать свои библиотеки tor с системными.
echo "Собираю .deb..."
DEB="$BUNDLE-deb"
rm -rf "$DEB"
mkdir -p "$DEB/DEBIAN" "$DEB/opt/$APP_NAME" "$DEB/usr/bin" \
         "$DEB/usr/share/applications" "$DEB/usr/share/pixmaps"
cp -r "$BUNDLE/." "$DEB/opt/$APP_NAME/"
ln -s "/opt/$APP_NAME/$APP_NAME" "$DEB/usr/bin/torlocalproxy"
cp Icon.png "$DEB/usr/share/pixmaps/$APP_ID.png"

cat > "$DEB/usr/share/applications/$APP_ID.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=TorLocalProxy
Comment=Локальный прокси к сети Tor
Exec=/opt/$APP_NAME/$APP_NAME
Icon=$APP_ID
Terminal=false
Categories=Network;Security;
EOF

# Размер в килобайтах — его показывает менеджер пакетов до установки.
SIZE=$(du -sk "$DEB/opt" | cut -f1)
cat > "$DEB/DEBIAN/control" <<EOF
Package: torlocalproxy
Version: $APP_VERSION
Section: net
Priority: optional
Architecture: $DEB_ARCH
Installed-Size: $SIZE
Maintainer: v.k.andreevich <v.k.andreevich@gmail.com>
Description: Мини-прокси для сети Tor без браузера
 Поднимает локальный прокси SOCKS5 на 127.0.0.1:9050 и HTTP на
 127.0.0.1:8118, которые вписываются в настройки других программ.
 tor поставляется в комплекте, ставить его отдельно не нужно.
EOF

rm -f "$ROOT/Release/$BUNDLE.deb"
dpkg-deb --build --root-owner-group "$DEB" "$ROOT/Release/$BUNDLE.deb"
rm -rf "$DEB" "$BUNDLE"

# 7. Контрольные суммы — их проверяет встроенное обновление перед
# заменой. Формат «сумма  имя», как у sha256sum.
echo "Считаю sha256..."
for пакет in "$ROOT/Release/$BUNDLE.tar.gz" "$ROOT/Release/$BUNDLE.deb"; do
  ( cd "$(dirname "$пакет")" && sha256sum "$(basename "$пакет")" > "$(basename "$пакет").sha256" )
done

echo "Готово: Release/$BUNDLE.tar.gz и Release/$BUNDLE.deb (+ .sha256)"
