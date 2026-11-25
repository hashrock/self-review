#!/bin/sh
# Builds dist/self-review.app (universal: arm64 + amd64), signs it with a
# Developer ID, and notarizes it when NOTARY_PROFILE names a notarytool
# keychain profile. Leaves dist/self-review-<version>-macos.zip.
#
#   VERSION=0.1.0 NOTARY_PROFILE=notary scripts/build-mac.sh
#
# SIGN_IDENTITY defaults to the keychain's "Developer ID Application" identity.
set -eu

cd "$(dirname "$0")/.."
VERSION=${VERSION:-0.0.0}
SIGN_IDENTITY=${SIGN_IDENTITY:-Developer ID Application}
APP=dist/self-review.app
ZIP=dist/self-review-$VERSION-macos.zip

rm -rf dist && mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

for arch in arm64 amd64; do
	CGO_ENABLED=0 GOOS=darwin GOARCH=$arch go build -trimpath \
		-ldflags "-s -w -X main.version=$VERSION" -o "dist/self-review-$arch" .
done
lipo -create -output "$APP/Contents/MacOS/self-review" dist/self-review-arm64 dist/self-review-amd64
rm dist/self-review-arm64 dist/self-review-amd64

cp assets/AppIcon.icns "$APP/Contents/Resources/AppIcon.icns"
cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key><string>self-review</string>
	<key>CFBundleDisplayName</key><string>self-review</string>
	<key>CFBundleIdentifier</key><string>com.hashrock.self-review</string>
	<key>CFBundleExecutable</key><string>self-review</string>
	<key>CFBundleIconFile</key><string>AppIcon</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>$VERSION</string>
	<key>CFBundleVersion</key><string>$VERSION</string>
	<key>LSMinimumSystemVersion</key><string>12.0</string>
	<key>LSApplicationCategoryType</key><string>public.app-category.developer-tools</string>
	<key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST

codesign --force --options runtime --timestamp --sign "$SIGN_IDENTITY" "$APP"
codesign --verify --strict --verbose=2 "$APP"

ditto -c -k --keepParent "$APP" "$ZIP"
if [ -n "${NOTARY_PROFILE:-}" ]; then
	xcrun notarytool submit "$ZIP" --keychain-profile "$NOTARY_PROFILE" --wait
	xcrun stapler staple "$APP"
	rm "$ZIP" && ditto -c -k --keepParent "$APP" "$ZIP"
	spctl --assess --type execute --verbose=2 "$APP"
fi
echo "built $ZIP"
