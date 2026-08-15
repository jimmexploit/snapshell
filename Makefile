# snapshell — Makefile
#
#   make build     build the binary to bin/snapshell
#   make test      run the full test suite
#   make vet       run go vet
#   make fmt       reformat all Go sources
#   make check     fmt-check + vet + test (CI-style gate)
#   make install   install the binary to ~/go/bin/snapshell
#   make setup     install system deps + binary + shell hook (scripts/setup.sh)
#   make uninstall remove the installed binary
#   make clean     remove build artifacts

BIN     := bin/snapshell
PREFIX  := $(HOME)/go/bin
INSTALL := $(PREFIX)/snapshell

.PHONY: all build test vet fmt fmt-check check install uninstall setup clean

all: build

build:
	go build -o $(BIN) ./cmd/snapshell

test:
	go test -count=1 ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

fmt-check:
	@if [ -n "$$(gofmt -l .)" ]; then \
		echo "gofmt needed on:"; gofmt -l .; exit 1; \
	fi

check: fmt-check vet test

install: build
	install -d $(PREFIX)
	install -m 0755 $(BIN) $(INSTALL)
	@echo "installed $(INSTALL)"

uninstall:
	rm -f $(INSTALL)
	@echo "removed $(INSTALL)"

setup:
	./scripts/setup.sh

clean:
	rm -rf bin