# SPDX-FileCopyrightText: 2026 TorrPlay
#
# SPDX-License-Identifier: MIT

NAME = torrplay
BUILD_DIR = build
VERSION ?= 1.0.0
MODULE = github.com/torrplay/torrplay

COMMIT = $(shell git rev-parse --short=7 HEAD)
BUILD_DATE = $(shell date +%F)
LDFLAGS = -X '$(MODULE)/internal/buildinfo.BuildDate=$(BUILD_DATE)' -X '$(MODULE)/internal/buildinfo.Commit=$(COMMIT)' -X '$(MODULE)/internal/buildinfo.Version=$(VERSION)'

all: android docker

client:
	@if command -v pnpm >/dev/null 2>&1; then \
		echo "Building client using local pnpm..."; \
		(cd client && pnpm install --frozen-lockfile && pnpm build) && \
		mkdir -p ./web/static && \
		find ./web/static -mindepth 1 -not -path ./web/static/README.md -delete && \
		cp -r ./client/out/. ./web/static/; \
	elif command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then \
		echo "pnpm not available. Falling back to Docker build..."; \
		docker build --file ./client.Dockerfile --tag $(NAME)-client:$(VERSION) . && \
		docker create --name $(NAME)-client-build-$(VERSION) $(NAME)-client:$(VERSION) && \
		docker start --attach $(NAME)-client-build-$(VERSION) && \
		mkdir -p ./web/static && \
		find ./web/static -mindepth 1 -not -path ./web/static/README.md -delete && \
		docker cp $(NAME)-client-build-$(VERSION):/$(BUILD_DIR)/client/out/. ./web/static/ && \
		docker rm $(NAME)-client-build-$(VERSION); \
	else \
		echo "Error: Neither pnpm nor Docker is available to build client." >&2; \
		exit 1; \
	fi

android: client
	docker buildx build --platform=linux/amd64 --file ./android.Dockerfile --tag $(NAME)-android:$(VERSION) .
	docker create --platform=linux/amd64 --name $(NAME)-android-build-$(VERSION) \
		--env LDFLAGS="$(LDFLAGS)" --env VERSION=$(VERSION) \
		--env SIGNING_KEY_BASE64=$(SIGNING_KEY_BASE64) \
		--env KEY_ALIAS=$(KEY_ALIAS) \
		--env KEY_PASSWORD=$(KEY_PASSWORD) \
		--env STORE_PASSWORD=$(STORE_PASSWORD) \
		$(NAME)-android:$(VERSION)
	docker start --attach $(NAME)-android-build-$(VERSION)
	mkdir -p ./bin
	docker cp $(NAME)-android-build-$(VERSION):/$(BUILD_DIR)/bin/. ./bin/
	docker rm $(NAME)-android-build-$(VERSION)

application: client
	docker build --file ./application.Dockerfile --tag $(NAME):$(VERSION) .
	docker create --name $(NAME)-build-$(VERSION) --env LDFLAGS="$(LDFLAGS)" --env VERSION=$(VERSION) $(NAME):$(VERSION)
	docker start --attach $(NAME)-build-$(VERSION)
	mkdir -p ./bin
	docker cp $(NAME)-build-$(VERSION):/$(BUILD_DIR)/bin/. ./bin/
	docker rm $(NAME)-build-$(VERSION)

docker: application
	docker build --file ./Dockerfile --tag $(NAME):$(VERSION) .

generate:
	go generate ./...

lint:
	@if ! command -v golangci-lint >/dev/null 2>&1 && ! [ -x "$$(go env GOPATH)/bin/golangci-lint" ]; then \
		echo "golangci-lint not found. Installing..."; \
		curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b "$$(go env GOPATH)/bin" v2.13.2; \
	fi; \
	if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		"$$(go env GOPATH)/bin/golangci-lint" run; \
	fi

help:
	@echo "TorrPlay Build System"
	@echo "====================="
	@echo ""
	@echo "Targets:"
	@echo "  all          - Build all artifacts (application, android, docker) (default)"
	@echo "  client       - Build web artifacts"
	@echo "  application  - Build the application"
	@echo "  android      - Build the Android APK files"
	@echo "  docker       - Build the multi-platform Docker image"
	@echo "  generate     - Run go generate to generate code"
	@echo "  lint         - Run golangci-lint static analysis"
	@echo "  help         - Show this help message"
	@echo ""
	@echo "Variables:"
	@echo "  VERSION         - Version tag for application and Docker images (default: 1.0.0)"
	@echo ""
	@echo "Examples:"
	@echo "  make all                           # Build everything with default settings"
	@echo "  make VERSION=2.0.0 application     # Build with specific version"
	@echo "  make android                       # Build the Android APK"
	@echo "  make docker                        # Build the Docker image"
	@echo "  make lint                          # Run linter"
	@echo ""

.PHONY: all android application client docker generate help lint
