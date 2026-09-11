#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
APP_NAME="SSH Mount Manager"
EXEC_NAME="SSHMountManager"
BUNDLE_ID="com.historyliao.ssh-mount-manager"
OUT_ROOT="${1:-/tmp/ssh-mount-manager-build}"
BUNDLE="$OUT_ROOT/$APP_NAME.app"

mkdir -p "$BUNDLE/Contents/MacOS" "$BUNDLE/Contents/Resources"
# 构建产物不落在被 Spotlight 索引的目录里：否则会被自动注册进 LaunchServices，
# 启动台就会出现第二份同名应用
touch "$OUT_ROOT/.metadata_never_index"

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
    <string>0.1.0</string>
    <key>CFBundleVersion</key>
    <string>1</string>
    <key>LSMinimumSystemVersion</key>
    <string>14.0</string>
    <key>NSHighResolutionCapable</key>
    <true/>
    <key>LSUIElement</key>
    <false/>
</dict>
</plist>
PLIST

swiftc \
  -parse-as-library \
  -swift-version 5 \
  -target arm64-apple-macos14.0 \
  -O \
  -o "$BUNDLE/Contents/MacOS/$EXEC_NAME" \
  "$ROOT"/Sources/*.swift

codesign --force --sign - --identifier "$BUNDLE_ID" "$BUNDLE" >/dev/null 2>&1 || true

# 开发副本不注册到 LaunchServices，避免启动台/聚焦里出现重复条目
LSREGISTER=/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister
"$LSREGISTER" -u "$BUNDLE" >/dev/null 2>&1 || true

echo "built: $BUNDLE"
