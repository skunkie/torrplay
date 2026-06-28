# SPDX-FileCopyrightText: 2026 TorrPlay
#
# SPDX-License-Identifier: MIT

FROM golang:1.25-alpine

ENV MODULE=github.com/torrplay/torrplay
ENV NAME=torrplay

WORKDIR /build

RUN apk add --no-cache yq-go \
    && go install github.com/tc-hib/go-winres@v0.3.3

COPY go.mod go.sum ./
RUN go mod download

COPY --exclude=./bin/ ./ ./

CMD ["./application-entrypoint.sh"]
