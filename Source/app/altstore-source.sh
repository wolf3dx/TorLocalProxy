#!/bin/sh
# Генерирует источник AltStore (apps.json) для установки без Sideloadly.
#
# AltStore — бесплатный способ ставить приложения на iPhone по обычному
# Apple ID (подпись на 7 дней), но, в отличие от ручного Sideloadly, он
# сам продлевает подпись в фоне и сам обновляет приложение из «источника»
# — JSON по ссылке. Пользователь один раз добавляет наш источник в
# AltStore, дальше установка и обновления идут оттуда.
#
# JSON печатается в stdout. Значения приходят из окружения — их
# подставляет CI, где известны точная версия, размер и ссылка на .ipa:
#   VERSION       — 0.0.6
#   DOWNLOAD_URL  — прямая ссылка на .ipa в разделе выпусков GitHub
#   SIZE          — размер .ipa в байтах
#   DATE          — дата выпуска, YYYY-MM-DD
#   ICON_URL      — по желанию; по умолчанию Icon.png из репозитория
set -e

: "${VERSION:?нужна VERSION}"
: "${DOWNLOAD_URL:?нужна DOWNLOAD_URL}"
: "${SIZE:?нужна SIZE}"
: "${DATE:?нужна DATE}"
# export — иначе дочерний python не увидит переменную в окружении.
export ICON_URL="${ICON_URL:-https://raw.githubusercontent.com/wolf3dx/TorLocalProxy/main/Source/app/Icon.png}"

python3 - <<'PY'
import json, os

v    = os.environ["VERSION"]
url  = os.environ["DOWNLOAD_URL"]
size = int(os.environ["SIZE"])
date = os.environ["DATE"]
icon = os.environ["ICON_URL"]

описание = (
    "Локальный прокси сети Tor без браузера. Поднимает SOCKS5 и HTTP, "
    "умеет работать через мосты (obfs4, webtunnel, snowflake, meek). "
    "Адрес прокси вписывается в настройки других приложений. Обновления "
    "приходят прямо в AltStore."
)
что_нового = "Версия " + v + ": встроенная проверка обновлений, «Проверить IP», защита от повторного запуска."

версия = {
    "version": v,
    "date": date,
    "localizedDescription": что_нового,
    "downloadURL": url,
    "size": size,
    "minOSVersion": "12.0",
}

приложение = {
    "name": "TorLocalProxy",
    "bundleIdentifier": "com.vkandreevich.torlocalproxy",
    "developerName": "VKAndreevich",
    "subtitle": "Tor-прокси без браузера",
    "localizedDescription": описание,
    "iconURL": icon,
    "tintColor": "7d4698",
    "category": "utilities",
    "screenshotURLs": [],
    # Плоские поля — их читает старый AltStore.
    "version": v,
    "versionDate": date,
    "versionDescription": что_нового,
    "downloadURL": url,
    "size": size,
    "minOSVersion": "12.0",
    # Массив versions — новый формат AltStore и SideStore.
    "versions": [версия],
}

источник = {
    "name": "TorLocalProxy",
    "identifier": "com.vkandreevich.torlocalproxy.altsource",
    "subtitle": "Обновления TorLocalProxy для iPhone",
    "iconURL": icon,
    "website": "https://gitlab.com/vkandreevich/TorLocalProxy",
    "apps": [приложение],
    "news": [],
}

print(json.dumps(источник, ensure_ascii=False, indent=2))
PY
