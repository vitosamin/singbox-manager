# Sing-box Manager — Makefile
# Сборка одного статического бинарника (без CGO).

BINARY      := singbox-manager
VERSION     := $(shell cat VERSION)
BUILD_TIME  := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS     := -s -w -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)

DIST_DIR    := dist
GO          := go
GOFLAGS     := -trimpath

.PHONY: all build-amd64 build-arm64 build-mipsle build-all clean test dev help

all: build-arm64

build-amd64:
	@echo ">> Сборка $(BINARY) v$(VERSION) для linux/amd64"
	@mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build \
		$(GOFLAGS) \
		-ldflags="$(LDFLAGS)" \
		-o $(DIST_DIR)/$(BINARY)-amd64 \
		./cmd/manager

build-arm64:
	@echo ">> Сборка $(BINARY) v$(VERSION) для linux/arm64"
	@mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build \
		$(GOFLAGS) \
		-ldflags="$(LDFLAGS)" \
		-o $(DIST_DIR)/$(BINARY)-arm64 \
		./cmd/manager

build-mipsle:
	@echo ">> Сборка $(BINARY) v$(VERSION) для linux/mipsle-softfloat"
	@mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=mipsle GOMIPS=softfloat $(GO) build \
		$(GOFLAGS) \
		-ldflags="$(LDFLAGS)" \
		-o $(DIST_DIR)/$(BINARY)-mipsle \
		./cmd/manager

build-all: build-amd64 build-arm64 build-mipsle

dev:
	SINGBOX_DIR=./test-data/sing-box \
	RULESET_DIR=./test-data/rulesets \
	STATE_FILE=./test-data/state.json \
	$(GO) run ./cmd/manager

test:
	$(GO) test ./...

clean:
	@echo ">> Очистка"
	@rm -rf $(DIST_DIR)

help:
	@echo "Доступные цели:"
	@echo "  make build-amd64   — сборка под linux/amd64 (Ubuntu)"
	@echo "  make build-arm64   — сборка под linux/arm64 (Keenetic)"
	@echo "  make build-mipsle  — сборка под linux/mipsle-softfloat (старые Keenetic)"
	@echo "  make build-all     — все платформы"
	@echo "  make dev           — локальный запуск на Ubuntu"
	@echo "  make test          — прогон тестов"
	@echo "  make clean         — очистка dist/"
