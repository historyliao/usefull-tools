#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
APP_NAME="SSH Proxy Manager"
EXEC_NAME="SSHProxyManager"
BUNDLE_ID="com.historyliao.ssh-proxy-manager"
BUNDLE="$ROOT/build/$APP_NAME.app"

mkdir -p "$BUNDLE/Contents/MacOS" "$BUNDLE/Contents/Resources"

if [ -f "$ROOT/Assets/AppIcon.icns" ]; then
    cp "$ROOT/Assets/AppIcon.icns" "$BUNDLE/Contents/Resources/AppIcon.icns"
fi

cat > "$BUNDLE/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleName</key>
    <string>$APP_NAME</string>
    <key>CFBundleDisplayName</key>
    <string>$APP_NAME</string>
    <key>CFBundleExecutable</key>
    <string>$EXEC_NAME</string>
    <key>CFBundleIdentifier</key>
    <string>$BUNDLE_ID</string>
    <key>CFBundleIconFile</key>
    <string>AppIcon</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleShortVersionString</key>
    <string>0.2.0</string>
    <key>CFBundleVersion</key>
    <string>2</string>
    <key>LSMinimumSystemVersion</key>
    <string>14.0</string>
    <key>NSHighResolutionCapable</key>
    <true/>
    <key>LSUIElement</key>
    <false/>
</dict>
</plist>
PLIST

# 内核随 App 一起打包，界面退出（stdin 断裂）时由内核收走全部隧道
( cd "$ROOT" && go build -o "$BUNDLE/Contents/MacOS/spm" ./cmd/spm )

swiftc \
  -parse-as-library \
  -swift-version 5 \
  -target arm64-apple-macos14.0 \
  -O \
  -o "$BUNDLE/Contents/MacOS/$EXEC_NAME" \
  "$ROOT"/Sources/*.swift

codesign --force --sign - --identifier "$BUNDLE_ID" "$BUNDLE" >/dev/null 2>&1 || true

echo "built: $BUNDLE"
