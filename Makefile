.DEFAULT_GOAL := help
.NOTPARALLEL:

VITE_PORT ?= 9245
TARGET_OS ?=
BIN_DIR := $(CURDIR)/bin

ifeq ($(OS),Windows_NT)
WAILS_EXE := .exe
else
WAILS_EXE :=
endif

WAILS_BIN := $(BIN_DIR)/wails3$(WAILS_EXE)
WAILS_TARGET := $(if $(TARGET_OS),GOOS=$(TARGET_OS),)

ifneq ($(strip $(TARGET_OS)),)
ifneq ($(words $(TARGET_OS)),1)
$(error TARGET_OS deve ser darwin, windows ou linux)
endif
ifeq ($(filter $(TARGET_OS),darwin windows linux),)
$(error TARGET_OS deve ser darwin, windows ou linux)
endif
endif

.PHONY: help setup setup-e2e wails-cli bindings dev build package installers test-web test-go test-e2e test lint check doctor

help:
	@printf '%s\n' \
	  'Harflex — desenvolvimento local' \
	  '  make setup          Baixar módulos Go e instalar dependências npm' \
	  '  make setup-e2e      Instalar Chromium para Playwright' \
	  '  make dev            Iniciar Wails (VITE_PORT=9245 por padrão)' \
	  '  make bindings       Regenerar bindings TypeScript' \
	  '  make build          Compilar app desktop (TARGET_OS opcional)' \
	  '  make package        Empacotar app (macOS: assinatura ad hoc)' \
	  '  make installers     Gerar instaladores (macOS .dmg, Windows .exe NSIS, Linux AppImage/deb/rpm)' \
	  '                      TARGET_OS opcional; UNIVERSAL=1 gera .dmg para Apple Silicon e Intel' \
	  '  make test-web       Vitest, typecheck e build frontend' \
	  '  make test-go        Go -race e vet (inclui pré-build frontend)' \
	  '  make test-e2e       Playwright' \
	  '  make test           test-go + test-e2e' \
	  '  make lint           Go vet e TypeScript typecheck' \
	  '  make check          Todos os gates locais do Taskfile/CI' \
	  '  make doctor         Diagnóstico Wails'

setup:
	go mod download
	cd frontend && npm ci

setup-e2e:
	cd frontend && npx playwright install chromium

$(WAILS_BIN): go.mod go.sum
	@mkdir -p "$(BIN_DIR)"
	go build -o "$@" github.com/wailsapp/wails/v3/cmd/wails3

wails-cli: $(WAILS_BIN)

bindings:
	go tool wails3 generate bindings -ts -i

dev: wails-cli
	@PATH="$(BIN_DIR):$$PATH" "$(WAILS_BIN)" dev -config ./build/config.yml -port "$(VITE_PORT)"

build: wails-cli
	@PATH="$(BIN_DIR):$$PATH" "$(WAILS_BIN)" build $(WAILS_TARGET)

package: wails-cli
	@PATH="$(BIN_DIR):$$PATH" "$(WAILS_BIN)" package $(WAILS_TARGET)

# Installers for TARGET_OS (default: this machine). Windows needs makensis (brew install makensis on macOS);
# the .dmg is signed ad hoc, so other Macs need right-click > Abrir on first launch.
installers: wails-cli
	@# Start clean: go build will not overwrite a universal binary, and the bundle keeps stale resources.
	@rm -rf bin/harflex bin/harflex.app
	@PATH="$(BIN_DIR):$$PATH" "$(WAILS_BIN)" task installers $(WAILS_TARGET) $(if $(UNIVERSAL),UNIVERSAL=$(UNIVERSAL),)
	@printf '%s\n' 'Instaladores gerados:'
	@ls -1 bin/*.dmg bin/*.AppImage bin/*.deb bin/*.rpm bin/*.pkg.tar.zst build/windows/nsis/*-installer.exe 2>/dev/null || true

test-web: wails-cli
	@PATH="$(BIN_DIR):$$PATH" "$(WAILS_BIN)" task test:frontend

test-go: wails-cli
	@PATH="$(BIN_DIR):$$PATH" "$(WAILS_BIN)" task test:go

test-e2e: wails-cli
	@PATH="$(BIN_DIR):$$PATH" "$(WAILS_BIN)" task test:e2e

test: test-go test-e2e

lint:
	go vet ./...
	cd frontend && npm run typecheck

check: wails-cli
	@PATH="$(BIN_DIR):$$PATH" "$(WAILS_BIN)" task check
	@if git show-ref --verify --quiet refs/heads/main; then git diff --check main...HEAD; fi

doctor: wails-cli
	@PATH="$(BIN_DIR):$$PATH" "$(WAILS_BIN)" doctor
