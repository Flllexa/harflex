#!/usr/bin/env bash
# Signs, notarizes and staples the macOS app and installer. Runs on the macOS release runner, after `wails3 task darwin:package:universal`.
# Expects the secrets of the release workflow in the environment (see .github/workflows/release.yml).
# Run after `wails3 task darwin:package:universal`; it builds the .dmg itself, from the stapled app.
set -euo pipefail

APP="bin/harflex.app"
DMG="bin/harflex.dmg"
PROFILE="harflex-notary"
KEYCHAIN="${RUNNER_TEMP}/harflex-signing.keychain-db"
KEYCHAIN_PASSWORD="$(openssl rand -hex 24)"

# A temporary keychain that lives only for this job.
security create-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN"
security set-keychain-settings -lut 21600 "$KEYCHAIN"
security unlock-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN"
security list-keychains -d user -s "$KEYCHAIN" $(security list-keychains -d user | tr -d '"')

# The Developer ID certificate with its private key, and Apple's intermediate so the chain validates.
echo "$APPLE_CERTIFICATE_P12_BASE64" | base64 --decode > "${RUNNER_TEMP}/developer-id.p12"
security import "${RUNNER_TEMP}/developer-id.p12" -k "$KEYCHAIN" -P "$APPLE_CERTIFICATE_PASSWORD" -T /usr/bin/codesign -T /usr/bin/security
security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$KEYCHAIN_PASSWORD" "$KEYCHAIN" > /dev/null
curl -fsSL -o "${RUNNER_TEMP}/DeveloperIDG2CA.cer" https://www.apple.com/certificateauthority/DeveloperIDG2CA.cer
security import "${RUNNER_TEMP}/DeveloperIDG2CA.cer" -k "$KEYCHAIN" > /dev/null

# Notarization credentials from the App Store Connect API key.
echo "$APPLE_API_KEY_P8_BASE64" | base64 --decode > "${RUNNER_TEMP}/AuthKey.p8"
xcrun notarytool store-credentials "$PROFILE" --key "${RUNNER_TEMP}/AuthKey.p8" --key-id "$APPLE_API_KEY_ID" --issuer "$APPLE_API_ISSUER_ID" --keychain "$KEYCHAIN"

# 1. The app: sign with the hardened runtime, notarize, staple.
codesign --force --options runtime --timestamp --deep --sign "$APPLE_SIGNING_IDENTITY" "$APP"
codesign --verify --deep --strict --verbose=2 "$APP"
ditto -c -k --keepParent "$APP" "${RUNNER_TEMP}/harflex-app.zip"
xcrun notarytool submit "${RUNNER_TEMP}/harflex-app.zip" --keychain-profile "$PROFILE" --wait
xcrun stapler staple "$APP"
xcrun stapler validate "$APP"

# 2. The installer, built from the stapled app: sign and notarize it too.
wails3 task darwin:create:dmg
codesign --force --timestamp --sign "$APPLE_SIGNING_IDENTITY" "$DMG"
codesign --verify --verbose=2 "$DMG"
xcrun notarytool submit "$DMG" --keychain-profile "$PROFILE" --wait
xcrun stapler staple "$DMG"
xcrun stapler validate "$DMG"

# Gatekeeper must accept both.
spctl --assess --type execute --verbose=2 "$APP"
spctl --assess --type open --context context:primary-signature --verbose=2 "$DMG"

# Clean up the secrets from the runner.
security delete-keychain "$KEYCHAIN" || true
rm -f "${RUNNER_TEMP}/developer-id.p12" "${RUNNER_TEMP}/AuthKey.p8"
echo "macOS app and installer signed, notarized and stapled."
