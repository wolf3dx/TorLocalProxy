#!/bin/sh
# Сборка APK для Android.
#
# Fyne упаковывает приложение сам, но положить рядом посторонний
# исполняемый файл не умеет — а нам нужен tor. Поэтому после упаковки
# APK вскрывается, в него добавляется lib/<архитектура>/libtor.so, и
# пакет подписывается заново.
#
# tor берётся готовым от Guardian Project (те же, кто делает Orbot):
# info.guardianproject:tor-android. Собирать его самим значило бы тянуть
# весь набор зависимостей под каждую архитектуру.
#
# Почему не встроенный tor из torrun/embedded: go-libtor несёт версию
# 0.3.5 от 2019 года, а сеть требует протокол FlowCtrl=1. Такой tor
# доходит до 40 % и зовёт exit(1), унося с собой всё приложение.
#
# Запуск:  ./build-android.sh [amd64|arm64|arm]
#          amd64 — для эмулятора, arm64 — для телефона.
set -e

TOR_VERSION=0.4.9.11
APP_VERSION=0.0.4
APP_BUILD=4
ARCH=${1:-amd64}

case "$ARCH" in
  amd64) ABI=x86_64 ;;
  arm64) ABI=arm64-v8a ;;
  arm)   ABI=armeabi-v7a ;;
  *) echo "неизвестная архитектура: $ARCH" >&2; exit 1 ;;
esac

: "${ANDROID_HOME:?не задан ANDROID_HOME}"
: "${ANDROID_NDK_HOME:?не задан ANDROID_NDK_HOME — путь должен быть без пробелов}"
: "${JAVA_HOME:?не задан JAVA_HOME}"

cd "$(dirname "$0")"

# 1. Забираем tor, если его ещё нет.
if [ ! -f "tor-android/$ABI/libtor.so" ]; then
  echo "Скачиваю tor $TOR_VERSION от Guardian Project..."
  mkdir -p "tor-android/$ABI"
  curl -sL -o tor-android/tor-android.aar \
    "https://repo1.maven.org/maven2/info/guardianproject/tor-android/$TOR_VERSION/tor-android-$TOR_VERSION.aar"
  unzip -o -j tor-android/tor-android.aar "jni/$ABI/libtor.so" -d "tor-android/$ABI"
fi

# 2. Собираем APK.
echo "Собираю APK под android/$ARCH..."
fyne package --target "android/$ARCH" \
  --icon Icon.png --app-id com.vkandreevich.torlocalproxy --name TorLocalProxy

# 3. Собираем свои классы Java: службу переднего плана и стартовую
# activity. Без них Android убивает приложение, как только оно уходит с
# экрана, — вместе с tor.
echo "Собираю классы Java..."
ANDROID_JAR=$(ls -d "$ANDROID_HOME"/platforms/android-*/android.jar | sort -r | head -1)
rm -rf .java-work && mkdir -p .java-work/classes
"$JAVA_HOME/bin/javac" -source 8 -target 8 -nowarn   -bootclasspath "$ANDROID_JAR" -classpath "$ANDROID_JAR"   -d .java-work/classes $(find java -name '*.java')
# d8 вызывается через java напрямую: обёртка d8.bat не переживает
# пробелов в пути к самой себе, а путь к SDK у Visual Studio с пробелами.
TOOLS_DIR=$(ls -d "$ANDROID_HOME"/build-tools/*/ | sort -r | head -1)
"$JAVA_HOME/bin/java" -cp "${TOOLS_DIR}lib/d8.jar" com.android.tools.r8.D8   --lib "$ANDROID_JAR" --output .java-work   $(find .java-work/classes -name '*.class')

# 4. Вкладываем tor в каталог нативных библиотек пакета.
#
# Пакет пересобирается целиком, а не дополняется: jar из JDK на архиве
# от Fyne спотыкается («only DEFLATED entries can have EXT descriptor»).
# Заодно выбрасываем старую подпись — она всё равно перестаёт быть
# действительной, как только состав файлов меняется.
echo "Вкладываю tor в APK..."
python - "$ABI" <<'PYTHON'
import os, shutil, sys, zipfile

abi = sys.argv[1]
исходный, новый = "TorLocalProxy.apk", "TorLocalProxy.apk.new"

with zipfile.ZipFile(исходный) as старый,      zipfile.ZipFile(новый, "w", zipfile.ZIP_DEFLATED) as цель:
    for запись in старый.infolist():
        имя = запись.filename
        if имя.startswith("META-INF/") and имя.upper().endswith((".SF", ".RSA", ".DSA", "MANIFEST.MF")):
            continue
        цель.writestr(запись, старый.read(имя))
    цель.write(os.path.join("tor-android", abi, "libtor.so"), f"lib/{abi}/libtor.so")
    # Свои классы идут вторым файлом dex: Android с пятой версии читает
    # их из пакета сам, без библиотеки multidex.
    цель.write(os.path.join(".java-work", "classes.dex"), "classes2.dex")

shutil.move(новый, исходный)
print(f"tor вложен как lib/{abi}/libtor.so")
PYTHON
rm -rf .java-work

# 5. Подписываем заново: добавление файла ломает прежнюю подпись.
if [ ! -f debug.keystore ]; then
  "$JAVA_HOME/bin/keytool" -genkeypair -keystore debug.keystore -storepass android \
    -alias androiddebugkey -keypass android -keyalg RSA -validity 10000 \
    -dname "CN=TorLocalProxy Debug"
fi
TOOLS=$(ls -d "$ANDROID_HOME"/build-tools/*/ | sort -r | head -1)
echo "Подписываю ($TOOLS)..."
"${TOOLS}apksigner.bat" sign --ks debug.keystore --ks-pass pass:android \
  --key-pass pass:android TorLocalProxy.apk

# 6. Складываем готовый пакет в Release с версией и архитектурой в имени.
mkdir -p ../../Release
mv TorLocalProxy.apk "../../Release/TorLocalProxy-$APP_VERSION-$ABI.apk"
rm -f TorLocalProxy.apk.idsig

echo "Готово: Release/TorLocalProxy-$APP_VERSION-$ABI.apk"
