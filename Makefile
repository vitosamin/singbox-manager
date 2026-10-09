# Sing-box Manager — Makefile
# Сборка одного статического бинарника (без CGO).

BINARY      := singbox-manager
VERSION     := $(shell cat VERSION)
BUILD_TIME  := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS     := -s -w -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)

DIST_DIR    := dist
GO          := go
GOFLAGS     := -trimpath

.PHONY: all build-amd64 build-arm64 build-all clean test dev help

all: build-arm64

# --- Сборка под amd64 (для локального теста на Ubuntu) ---
build-amd64:
	@echo ">> Сборка $(BINARY) v$(VERSION) для linux/amd64"
	@mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build \
		$(GOFLAGS) \
		-ldflags="$(LDFLAGS)" \
		-o $(DIST_DIR)/$(BINARY)-amd64 \
		./cmd/manager

# --- Сборка под arm64 (для Keenetic aarch64) ---
build-arm64:
	@echo ">> Сборка $(BINARY) v$(VERSION) для linux/arm64"
	@mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build \
		$(GOFLAGS) \
		-ldflags="$(LDFLAGS)" \
		-o $(DIST_DIR)/$(BINARY)-arm64 \
		./cmd/manager

# --- Сборка под mipsle (старые Keenetic) ---
build-mipsle:
	@echo ">> Сборка $(BINARY) v$(VERSION) для linux/mipsle"
	@mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=mipsle GOMIPS=softfloat $(GO) build \
		$(GOFLAGS) \
		-ldflags="$(LDFLAGS)" \
		-o $(DIST_DIR)/$(BINARY)-mipsle \
		./cmd/manager

# --- Сборка под все платформы ---
build-all: build-amd64 build-arm64 build-mipsle

# --- Локальный запуск (для отладки на Ubuntu) ---
dev:
	SINGBOX_DIR=./test-data/sing-box \
	RULESET_DIR=./test-data/rulesets \
	STATE_FILE=./test-data/state.json \
	$(GO) run ./cmd/manager

# --- Тесты ---
test:
	$(GO) test ./...

# --- Очистка ---
clean:
	@echo ">> Очистка"
	@rm -rf $(DIST_DIR)

# --- Помощь ---
help:
	@echo "Доступные цели:"
	@echo "  make build-amd64   — сборка под linux/amd64 (Ubuntu)"
	@echo "  make build-arm64   — сборка под linux/arm64 (Keenetic)"
	@echo "  make build-mipsle  — сборка под linux/mipsle (старые Keenetic)"
	@echo "  make build-all     — все платформы"
	@echo "  make dev           — локальный запуск на Ubuntu"
	@echo "  make test          — прогон тестов"
	@echo "  make clean         — очистка dist/"
