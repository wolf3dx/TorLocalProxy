#!/bin/sh
# Приносит статический tor для сборки под iOS.
#
# Компилировать tor под Apple мы не пытаемся: проект iCepa выпускает
# готовый tor.xcframework, в котором собрано всё нужное одним архивом —
# tor, OpenSSL 3, libevent, liblzma и zlib. Версия tor там та же, что мы
# кладём в APK, и это не совпадение: и то, и другое собирают люди,
# которые занимаются тором на телефонах годами.
#
# Раскладываем так, как ждёт пакет core/torrun/torlib:
#   torlib/include/tor_api.h  — заголовок с tor_run_main;
#   torlib/lib/libtor.a       — срез архива под нужную архитектуру.
#
# В репозиторий эти файлы не попадают: вместе они на шестьдесят с лишним
# мегабайт, и любой может получить их этой командой.
#
# Запуск:
#   ./fetch-tor-ios.sh device     — для iPhone (ios-arm64)
#   ./fetch-tor-ios.sh simulator  — для симулятора (arm64 + x86_64)
set -e

# Версия рамки iCepa. Их нумерация «ABB.C.X» означает tor 0.A.B.C и
# выпуск рамки X: 409.11.2 — это tor 0.4.9.11, второй выпуск.
FRAMEWORK_VERSION=v409.11.2
ARCHIVE_URL="https://github.com/iCepa/Tor.framework/releases/download/${FRAMEWORK_VERSION}/tor.xcframework.zip"

TARGET=${1:-device}
case "$TARGET" in
  device)    SLICE=ios-arm64 ;;
  simulator) SLICE=ios-arm64_x86_64-simulator ;;
  *) echo "неизвестная цель: $TARGET (device или simulator)" >&2; exit 1 ;;
esac

cd "$(dirname "$0")"
TORLIB=../core/torrun/torlib
CACHE=".tor-ios-cache"

mkdir -p "$CACHE"
if [ ! -f "$CACHE/tor.xcframework.zip" ]; then
  echo "Скачиваю tor.xcframework $FRAMEWORK_VERSION (около 140 МБ)..."
  curl -fL --retry 3 -o "$CACHE/tor.xcframework.zip.part" "$ARCHIVE_URL"
  mv "$CACHE/tor.xcframework.zip.part" "$CACHE/tor.xcframework.zip"
fi

if [ ! -d "$CACHE/tor.xcframework" ]; then
  echo "Распаковываю..."
  unzip -q -o "$CACHE/tor.xcframework.zip" -d "$CACHE"
fi

SRC="$CACHE/tor.xcframework/$SLICE/tor.framework/Versions/A"
if [ ! -f "$SRC/tor" ]; then
  echo "В рамке нет среза $SLICE — состав изменился, проверьте выпуск" >&2
  exit 1
fi

echo "Ставлю срез $SLICE..."
rm -rf "$TORLIB/lib" "$TORLIB/include"
mkdir -p "$TORLIB/lib" "$TORLIB/include"
cp "$SRC/tor" "$TORLIB/lib/libtor.a"
cp "$SRC/Headers/feature/api/tor_api.h" "$TORLIB/include/tor_api.h"

echo "Готово:"
ls -la "$TORLIB/lib/libtor.a" "$TORLIB/include/tor_api.h"
