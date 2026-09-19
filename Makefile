.PHONY: help build run notion-init fmt vet tidy check mac-build mac-run mac-app mac-open clean

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

clean:
	rm -rf bin mac/.build
