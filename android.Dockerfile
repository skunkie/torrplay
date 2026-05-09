# SPDX-FileCopyrightText: 2026 TorrPlay
#
# SPDX-License-Identifier: MIT

FROM golang:1.25-trixie

ARG LDFLAGS

ENV ANDROID_BUILD_TOOLS_VERSION=36.1.0
ENV ANDROID_SDK_VERSION=14742923
ENV ANDROID_HOME=/opt/android-sdk
ENV ANDROID_PLATFORMS_VERSION=36
ENV ANDROID_NDK_BUNDLE_VERSION=29.0.14206865
ENV DEBIAN_FRONTEND=noninteractive
ENV PATH=$PATH:${ANDROID_HOME}/cmdline-tools/bin:${ANDROID_HOME}/platform-tools

RUN apt-get update && apt-get install -y --no-install-recommends \
    openjdk-21-jdk \
    unzip \
    && curl -fsSL https://deb.nodesource.com/setup_24.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && mkdir -p ${ANDROID_HOME} \
    && curl -fsSL "https://dl.google.com/android/repository/commandlinetools-linux-${ANDROID_SDK_VERSION}_latest.zip" -o cmdline-tools.zip \
    && unzip cmdline-tools.zip -d ${ANDROID_HOME} \
    && rm cmdline-tools.zip \
    && yes | $ANDROID_HOME/cmdline-tools/bin/sdkmanager --sdk_root=$ANDROID_HOME --licenses \
    && $ANDROID_HOME/cmdline-tools/bin/sdkmanager --sdk_root=$ANDROID_HOME \
        "platform-tools" \
        "build-tools;${ANDROID_BUILD_TOOLS_VERSION}" \
        "platforms;android-${ANDROID_PLATFORMS_VERSION}" \
        "ndk;${ANDROID_NDK_BUNDLE_VERSION}" \
    && rm -rf /var/lib/apt/lists/*

# Setup pnpm
RUN corepack prepare pnpm@latest --activate && corepack enable

# Install gomobile
RUN go install golang.org/x/mobile/cmd/gomobile@latest && gomobile init

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENTRYPOINT [ "./android-entrypoint.sh" ]
