#!/usr/bin/env bash
# 无 Gradle 的极简 APK 构建：aapt2 -> javac -> d8 -> zipalign -> apksigner
# 注意：aapt2 只接受相对路径，且必须关闭 MSYS 的路径转换。
set -e

SDKW="C:/Users/WangXin/.android-build/sdk"
BTW="$SDKW/build-tools/34.0.0"
PLATW="$SDKW/platforms/android-34/android.jar"
SDKP="/c/Users/WangXin/.android-build/sdk"
BTP="$SDKP/build-tools/34.0.0"
PLATP="$SDKP/platforms/android-34/android.jar"

ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"

[ -f "$PLATP" ] || { echo "缺少 android.jar: $PLATP"; exit 1; }
[ -d "$BTP" ] || { echo "缺少 build-tools: $BTP"; exit 1; }

OUT=build
rm -rf "$OUT"
mkdir -p "$OUT/classes" "$OUT/dex" "$OUT/flat"

AAPT="env MSYS2_ARG_CONV_EXCL=* MSYS_NO_PATHCONV=1 $BTW/aapt2.exe"

echo "== 1/6 编译资源 =="
$AAPT compile --dir res -o "$OUT/res.zip"
unzip -o -q "$OUT/res.zip" -d "$OUT/flat"
FLATS=$(ls "$OUT/flat"/*.flat | tr '\n' ' ')
echo "   生成 $(echo $FLATS | wc -w) 个 .flat"

echo "== 2/6 链接清单，生成未签名 APK =="
# shellcheck disable=SC2086
$AAPT link \
  -o "$OUT/app-unsigned.apk" \
  -I "$PLATW" \
  --manifest AndroidManifest.xml \
  --min-sdk-version 21 \
  --target-sdk-version 34 \
  --version-code 1 \
  --version-name 1.0 \
  $FLATS

echo "== 3/6 编译 Java =="
find src -name "*.java" > "$OUT/sources.txt"
javac -encoding UTF-8 --release 11 -nowarn \
  -classpath "$PLATP" \
  -d "$OUT/classes" \
  @"$OUT/sources.txt"

echo "== 4/6 dex =="
# shellcheck disable=SC2046
java -cp "$BTP/lib/d8.jar" com.android.tools.r8.D8 \
  --lib "$PLATP" --min-api 21 --release \
  --output "$OUT/dex" \
  $(find "$OUT/classes" -name "*.class")

echo "== 5/6 打包 dex =="
python - "$OUT" <<'PY'
import sys, os, zipfile
out = sys.argv[1]
apk = os.path.join(out, "app-unsigned.apk")
dex = os.path.join(out, "dex", "classes.dex")
with zipfile.ZipFile(apk, "a", zipfile.ZIP_DEFLATED) as z:
    z.write(dex, "classes.dex")
print("   classes.dex 已写入")
PY

echo "== 6/6 对齐 + 签名 =="
"$BTP/zipalign.exe" -f 4 "$OUT/app-unsigned.apk" "$OUT/app-aligned.apk"

KS=debug.keystore
if [ ! -f "$KS" ]; then
  keytool -genkeypair -keystore "$KS" -storepass android -keypass android \
    -alias androiddebugkey -keyalg RSA -keysize 2048 -validity 10000 \
    -dname "CN=Android Debug,O=Android,C=US" >/dev/null 2>&1
fi

java -jar "$BTP/lib/apksigner.jar" sign \
  --ks "$KS" --ks-pass pass:android --key-pass pass:android \
  --out "$OUT/script-du-jour.apk" "$OUT/app-aligned.apk"

java -jar "$BTP/lib/apksigner.jar" verify --print-certs "$OUT/script-du-jour.apk"

echo
echo "== 完成 =="
ls -lh "$OUT/script-du-jour.apk"
