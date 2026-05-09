# SPDX-FileCopyrightText: 2026 TorrPlay
#
# SPDX-License-Identifier: MIT

.PHONY: all
all: build run

.PHONY: build
build:
	docker build --tag torrplay-client:latest .

.PHONY: run
run:
	docker run --platform=linux/amd64 --rm --volume `pwd`:/build torrplay-client:latest
