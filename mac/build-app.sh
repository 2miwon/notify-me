#!/bin/bash
# Packages the Swift Package's built executable into a real NotifyMe.app
# bundle — double-clickable from Finder, addable to the Dock, launchable
# from Spotlight — instead of only via `swift run` from a terminal.
#
# No Xcode project needed: a macOS .app is just a folder with this
# specific layout (Contents/MacOS/<executable> + Contents/Info.plist).
# LaunchServices treats anything shaped like that as a real app.
set -euo pipefail
cd "$(dirname "$0")"

CONFIG="${1:-release}" # release (default, faster to run) or debug
APP_NAME="NotifyMe.app"
BUILD_DIR=".build/$CONFIG-app"

echo "Building ($CONFIG)..."
swift build -c "$CONFIG"

BIN_PATH=$(swift build -c "$CONFIG" --show-bin-path)/NotifyMe

rm -rf "$BUILD_DIR/$APP_NAME"
mkdir -p "$BUILD_DIR/$APP_NAME/Contents/MacOS"
cp "$BIN_PATH" "$BUILD_DIR/$APP_NAME/Contents/MacOS/NotifyMe"
cp Info.plist "$BUILD_DIR/$APP_NAME/Contents/Info.plist"

# Ad-hoc signing quiets Gatekeeper's "unidentified developer" friction for
# a locally-built app you're running on your own machine — not the same
# as a real Developer ID signature, just enough for local use.
codesign --force --deep --sign - "$BUILD_DIR/$APP_NAME" 2>/dev/null || true

echo "Built: $BUILD_DIR/$APP_NAME"
echo "Run it with: open \"$BUILD_DIR/$APP_NAME\""
echo "Or drag it into /Applications, or right-click -> Add to Dock."
