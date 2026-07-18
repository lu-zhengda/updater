APP     := updater
PKG     := ./cmd/updater
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PREFIX  := /usr/local

.PHONY: build install uninstall clean test

build:
	go build -ldflags "$(LDFLAGS)" -o $(APP) $(PKG)

install: build
	install -d $(PREFIX)/bin
	install -m 755 $(APP) $(PREFIX)/bin/$(APP)

uninstall:
	rm -f $(PREFIX)/bin/$(APP)

clean:
	rm -f $(APP)

test:
	go test -race ./...
