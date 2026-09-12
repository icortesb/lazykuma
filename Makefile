BIN := lazykuma
PKG := ./cmd/lazykuma

# A binary built from an uncommitted tree says so, through the -dirty marker.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

# CGO off and the symbol table stripped: one static binary that runs anywhere.
BUILDFLAGS := CGO_ENABLED=0
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test integration vet run install clean

build:
	$(BUILDFLAGS) go build -ldflags="$(LDFLAGS)" -o $(BIN) $(PKG)

test:
	go test -race ./...

# Starts a throwaway Kuma v2 (podman or docker), runs the test against it, and
# removes it again.
integration:
	scripts/kuma-up.sh
	go test -tags=integration -count=1 -v ./internal/kuma/ ; status=$$? ; scripts/kuma-up.sh down ; exit $$status

vet:
	go vet ./...

run: build
	./$(BIN)

install:
	$(BUILDFLAGS) go install -ldflags="$(LDFLAGS)" $(PKG)

clean:
	rm -f $(BIN)
