#!/bin/sh
# Builds Ember.app so ember starts from Spotlight or Launchpad on macOS.
# The app opens the dashboard and quits by itself a few seconds after the
# last dashboard tab is closed.
#
#   scripts/macos-app.sh [destination folder]   (default: ~/Applications)
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
dest=${1:-"$HOME/Applications"}
app="$dest/Ember.app"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/Ember.app/Contents/MacOS" "$work/Ember.app/Contents/Resources"
(cd "$root" && go build -mod=vendor -o "$work/Ember.app/Contents/Resources/ember" .)

cat > "$work/Ember.app/Contents/MacOS/Ember" <<'LAUNCH'
#!/bin/sh
# ember runs detached and this launcher exits at once, so opening the app
# again while ember is up runs it again and brings the dashboard back.
mkdir -p "$HOME/Library/Logs"
nohup "$(dirname "$0")/../Resources/ember" -quit-on-close >> "$HOME/Library/Logs/ember.log" 2>&1 &
LAUNCH
chmod +x "$work/Ember.app/Contents/MacOS/Ember"

cat > "$work/Ember.app/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>Ember</string>
  <key>CFBundleDisplayName</key><string>Ember</string>
  <key>CFBundleIdentifier</key><string>io.github.odmrs.ember</string>
  <key>CFBundleExecutable</key><string>Ember</string>
  <key>CFBundleIconFile</key><string>Ember</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>0.1.0</string>
  <key>LSMinimumSystemVersion</key><string>11.0</string>
  <key>LSUIElement</key><true/>
</dict>
</plist>
PLIST

# The icon is optional: without the system tools the app keeps the default one.
if command -v qlmanage >/dev/null && command -v iconutil >/dev/null && command -v sips >/dev/null; then
  qlmanage -t -s 1024 -o "$work" "$root/assets/icon.svg" >/dev/null 2>&1 || true
  if [ -f "$work/icon.svg.png" ]; then
    set_dir="$work/Ember.iconset"
    mkdir "$set_dir"
    for size in 16 32 128 256 512; do
      sips -z $size $size "$work/icon.svg.png" --out "$set_dir/icon_${size}x${size}.png" >/dev/null
      sips -z $((size * 2)) $((size * 2)) "$work/icon.svg.png" --out "$set_dir/icon_${size}x${size}@2x.png" >/dev/null
    done
    iconutil -c icns "$set_dir" -o "$work/Ember.app/Contents/Resources/Ember.icns" || true
  fi
fi

mkdir -p "$dest"
rm -rf "$app"
mv "$work/Ember.app" "$app"
echo "Installed $app"
