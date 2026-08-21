BINARY := d9s
PKG    := ./cmd/d9s

# Build metadata stamped into the binary. A local build never claims a release
# version: it reports dev-<short commit>, with -dirty appended when the working
# tree has uncommitted changes. Tagged releases are stamped by GoReleaser
# instead, which overrides these with the real tag.
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DIRTY   := $(shell test -z "$$(git status --porcelain 2>/dev/null)" || echo -dirty)
VERSION ?= dev-$(COMMIT)$(DIRTY)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build test vet lint run snapshot clean

build:
	go build -ldflags '$(LDFLAGS)' -o $(BINARY) $(PKG)

test:
	go test ./...

vet:
	go vet ./...

lint: vet
	gofmt -l . | (! grep .)
	golangci-lint run

run: build
	./$(BINARY)

# Exercise the full release build locally without publishing anything.
snapshot:
	goreleaser release --snapshot --clean

clean:
	rm -f $(BINARY)
	rm -rf dist
