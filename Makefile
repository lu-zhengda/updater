APP     := updater
PKG     := ./cmd/updater
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PREFIX  := /usr/local

.PHONY: build build-menubar install uninstall clean test

build:
	go build -ldflags "$(LDFLAGS)" -o $(APP) $(PKG)

build-menubar: build
	go build -ldflags "$(LDFLAGS)" -o $(APP)-menubar ./cmd/updater-menubar

install: build
	install -d $(PREFIX)/bin
	install -m 755 $(APP) $(PREFIX)/bin/$(APP)

uninstall:
	rm -f $(PREFIX)/bin/$(APP)

clean:
	rm -f $(APP) $(APP)-menubar

test:
	go test -race ./...
