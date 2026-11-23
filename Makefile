.PHONY: help build run notion-init fmt vet tidy check mac-build mac-run mac-app mac-open mac-install clean

help:
	@echo "Crawler (Go):"
	@echo "  make build         build the crawler binary to bin/crawler"
	@echo "  make run           run the crawler locally (loads .env for NOTION_TOKEN etc.)"
	@echo "  make notion-init   provision an empty Notion database's schema (same env vars)"
	@echo "  make fmt           gofmt -w every .go file"
	@echo "  make vet           go vet ./..."
	@echo "  make tidy          go mod tidy"
	@echo "  make check         fmt + vet + build"
	@echo ""
	@echo "macOS app (Swift):"
	@echo "  make mac-build     swift build in mac/ (compile-check only, no bundle)"
	@echo "  make mac-run       swift run in mac/ (terminal foreground, rebuilds every time)"
	@echo "  make mac-app       package mac/ into a real NotifyMe.app you can double-click"
	@echo "  make mac-open      mac-app, then open it — closest to a normal app launch"
	@echo "  make mac-install   mac-app, then copy it into /Applications (Launchpad/Spotlight)"
	@echo ""
	@echo "  make clean         remove build artifacts"

build:
	go build -o bin/crawler ./cmd/crawler

run:
	set -a && source .env && set +a && go run ./cmd/crawler

notion-init:
	set -a && source .env && set +a && go run ./cmd/notion-init

fmt:
	gofmt -w .

vet:
	go vet ./...

tidy:
	go mod tidy

check: fmt vet build

mac-build:
	cd mac && swift build

mac-run:
	cd mac && swift run

mac-app:
	cd mac && ./build-app.sh release

mac-open: mac-app
	open mac/.build/release-app/NotifyMe.app

# Launchpad and Finder's Applications list only show apps that live in an
# Applications folder; mac-app/mac-open leave the bundle inside mac/.build.
# Override with `make mac-install INSTALL_DIR=~/Applications` if /Applications
# isn't writable.
INSTALL_DIR ?= /Applications

mac-install: mac-app
	osascript -e 'quit app "NotifyMe"' 2>/dev/null || true
	@# Wait for the old instance to exit, or `open` may hand off to it and
	@# leave nothing running once it quits.
	@for i in 1 2 3 4 5 6 7 8 9 10; do pgrep -x NotifyMe >/dev/null || break; sleep 0.5; done
	rm -rf "$(INSTALL_DIR)/NotifyMe.app"
	cp -R mac/.build/release-app/NotifyMe.app "$(INSTALL_DIR)/"
	open "$(INSTALL_DIR)/NotifyMe.app"

clean:
	rm -rf bin mac/.build
