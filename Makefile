##
# a-kerno
#
# @file
# @version 0.1

# Go Makefile

# Variables
PRJ=a-kerno
APP=a-kerno
BINDIR=build
PREFIX?=/usr/local/bin

# Replace it with "sudo", "doas" or somethat, that allows root privileges on your
# system.
# SUDO=sudo
SUDO?=

# Version information
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "v0.0.0-dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
FLAGS := -buildvcs=false -ldflags "-X main.version=$(VERSION) -X main.gitCommit=$(COMMIT)"

.PHONY: all
all: build

ACTIVE_CMDS := cmd/a-kerno

.PHONY: build
build:
	$(foreach dir,$(ACTIVE_CMDS), echo "$(dir) building..."; go build $(FLAGS) -o $(BINDIR)/ ./$(dir);)

.PHONY: test
test:
	go tool ginkgo ./...

.PHONY: run
run: build
	./$(BINDIR)/$(APP)

.PHONY: run-race
run-race: tidy
	go run -race $(LDFLAGS) ./cmd/$(APP)

.PHONY: lint
lint:
	go tool golangci-lint run ./...

.PHONY: lint-fix
lint-fix:
	go tool golangci-lint run --fix ./...

.PHONY: tidy
tidy:
	go mod tidy

# Build under regular user, only install under root!
.PHONY: install
install: build
	@echo "Don't forget to set SUDO=sudo (or SUDO=doas) before this command!"
	@echo "for example: SUDO=doas make install"
	$(SUDO) install ./build/$(APP) $(PREFIX)

.PHONY: sloc
sloc:
	cloc * >sloc.stats

.PHONY: clean
clean:
	go clean
	rm -rf $(BINDIR)

# end
