# netbox-glean Makefile.
# Targets are convenience wrappers; GitHub Actions is the source of truth.

BINARY  := netbox-glean
VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo unknown)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
CMD     := ./cmd/netbox-glean

GOOS   ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)

.PHONY: all build run test vet fmt tidy clean dist docker

all: build

build: tidy
	@mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(CMD)

run: tidy
	go run $(CMD) --listen 127.0.0.1:8080 --data-dir ./data

test: tidy
	go test ./...

vet: tidy
	go vet ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './dist/*')

tidy:
	go mod tidy

clean:
	rm -rf bin dist

# Cross compile release archives. Mirrors .github/workflows/release.yml.
dist: tidy
	@mkdir -p dist
	@for target in \
	  linux/amd64 linux/arm64 \
	  darwin/amd64 darwin/arm64 \
	  windows/amd64 windows/arm64 \
	  freebsd/amd64 freebsd/arm64; do \
	  os=$${target%/*}; arch=$${target#*/}; \
	  ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
	  echo "building $$os/$$arch"; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" \
	    -o "dist/$(BINARY)_$(VERSION)_$${os}_$${arch}$${ext}" $(CMD) || exit 1; \
	done

docker:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_DATE=$(DATE) -t $(BINARY):$(VERSION) .