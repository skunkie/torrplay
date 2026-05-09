# SPDX-FileCopyrightText: 2026 TorrPlay
#
# SPDX-License-Identifier: MIT

FROM golang:1.25-alpine

ENV MODULE=github.com/torrplay/torrplay
ENV NAME=torrplay
ENV VERSION=1.0.0

WORKDIR /build

RUN go install github.com/tc-hib/go-winres@v0.3.3

COPY go.mod go.sum ./
RUN go mod download

COPY . .

CMD [ "./entrypoint.sh" ]
