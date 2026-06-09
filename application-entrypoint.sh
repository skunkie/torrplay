#!/usr/bin/env sh

# SPDX-FileCopyrightText: 2026 TorrPlay
#
# SPDX-License-Identifier: MIT

set -e

mkdir -p /build/bin

cd /build/cmd/${NAME}

for osarch in linux-amd64 linux-arm linux-arm64 darwin-arm64 windows-amd64 windows-arm64; do
    goos="${osarch%-*}"
    goarch="${osarch#*-}"
    name=${NAME}-${goos}-${goarch}
    if [ "${goos}" = "windows" ]; then
        name=${name}.exe
        /go/bin/go-winres make --arch ${goarch} --in /build/winres/winres.json --out /build/cmd/${NAME}/rsrc --product-version=${VERSION} --file-version=${VERSION}
    fi
    CGO_ENABLED=0 GOOS=${goos} GOARCH=${goarch} go build -buildvcs=false -ldflags "-s -w ${LDFLAGS}" -trimpath -o /build/bin/${name} .
done
