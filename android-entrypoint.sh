#!/usr/bin/env sh

# SPDX-FileCopyrightText: 2026 TorrPlay
#
# SPDX-License-Identifier: MIT

set -e

mkdir -p /build/bin

go get golang.org/x/mobile/bind
CGO_LDFLAGS="-static-libstdc++" gomobile bind \
    -target=android \
    -androidapi 24 \
    -ldflags="-s -w -checklinkname=0 ${LDFLAGS}" \
    -trimpath \
    -o /build/bin/torrplay.aar \
    /build/pkg/torrplay
go mod tidy

cd /build/client
pnpm install --frozen-lockfile
pnpm build
npx cap telemetry off
npx cap sync android

# Calculate versionCode from VERSION
MAJOR=$(echo $VERSION | cut -d. -f1)
MINOR=$(echo $VERSION | cut -d. -f2)
PATCH=$(echo $VERSION | cut -d. -f3 | cut -d- -f1)
VERSION_CODE=$((MAJOR * 10000 + MINOR * 100 + PATCH))

sed -i \
    -e "s/versionCode 1/versionCode ${VERSION_CODE}/g" \
    -e "s/versionName \"1.0\"/versionName \"${VERSION}\"/g" \
    ./android/app/build.gradle

mkdir -p ./android/app/libs && cp /build/bin/torrplay.aar ./android/app/libs/

cd ./android

if [ -n "$SIGNING_KEY_BASE64" ] && [ -n "$KEY_ALIAS" ] && [ -n "$KEY_PASSWORD" ] && [ -n "$STORE_PASSWORD" ]; then
    echo "Signing configuration detected. Building release APK."
    echo "$SIGNING_KEY_BASE64" | base64 -d > /tmp/keystore.jks
    ./gradlew clean assembleRelease \
        -Pandroid.injected.signing.store.file=/tmp/keystore.jks \
        -Pandroid.injected.signing.store.password=$STORE_PASSWORD \
        -Pandroid.injected.signing.key.alias=$KEY_ALIAS \
        -Pandroid.injected.signing.key.password=$KEY_PASSWORD

    for apk in ./app/build/outputs/apk/release/*.apk; do
        arch=$(basename "$apk" | sed -E 's/.*-(armeabi-v7a|arm64-v8a|x86|x86_64|universal)-.*/\1/')
        if [ -n "$arch" ]; then
            cp "$apk" "/build/bin/torrplay-android-${arch}.apk"
        fi
    done
else
    echo "No signing configuration found. Building debug APK."
    ./gradlew clean assembleDebug

    for apk in ./app/build/outputs/apk/debug/*.apk; do
        arch=$(basename "$apk" | sed -E 's/.*-(armeabi-v7a|arm64-v8a|x86|x86_64|universal)-.*/\1/')
        if [ -n "$arch" ]; then
            cp "$apk" "/build/bin/torrplay-android-${arch}-debug.apk"
        fi
    done
fi
