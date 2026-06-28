BINARY  := tc
PREFIX  ?= $(HOME)/.local
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: build install uninstall test vet fmt run clean check

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/tc

install: build
	install -d "$(PREFIX)/bin"
	install -m 0755 bin/$(BINARY) "$(PREFIX)/bin/$(BINARY)"
	@echo "installed $(BINARY) $(VERSION) to $(PREFIX)/bin/$(BINARY)"

uninstall:
	rm -f "$(PREFIX)/bin/$(BINARY)"

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

check: fmt vet test

run: build
	./bin/$(BINARY) serve

clean:
	rm -rf bin
