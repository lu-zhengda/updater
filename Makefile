APP     := updater
PKG     := ./cmd/updater
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PREFIX  := /usr/local

.PHONY: build app install install-app uninstall clean test

build:
	go build -ldflags "$(LDFLAGS)" -o $(APP) $(PKG)

# Assemble Updater.app (menu bar app; same binary also provides CLI/TUI).
app: build
	sh scripts/mkapp.sh $(APP) $(VERSION) .

# Install Updater.app to /Applications and launch it.
install-app: app
	rm -rf /Applications/Updater.app
	cp -R Updater.app /Applications/Updater.app
	open /Applications/Updater.app

install: build
	install -d $(PREFIX)/bin
	install -m 755 $(APP) $(PREFIX)/bin/$(APP)

uninstall:
	rm -f $(PREFIX)/bin/$(APP)

clean:
	rm -f $(APP)
	rm -rf Updater.app

test:
	go test -race ./...
