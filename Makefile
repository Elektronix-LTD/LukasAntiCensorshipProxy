# =============================================================================
#  Makefile — local build of Lukas-Anti-Censorship-Proxy (lacp)
# =============================================================================
#
# Usage (Linux, macOS, *BSD; on Windows it works with Git Bash / GNU make):
#
#   make            # tests + binary for the current OS
#   make test       # tests only
#   make build      # binary for the current OS only
#   make windows    # cross: Windows amd64 + arm64
#   make darwin     # cross: macOS Intel + Apple Silicon
#   make dist       # everything CI ships (win + mac), into dist/
#   make clean      # remove binaries
#
# Requires Go 1.22+ in PATH. CGO is not needed.
# =============================================================================

# Binary name on the current OS (GNU make still adds .exe when GOOS=windows).
BINARY ?= lacp

# Version stamped into `main.version`. Inside a git checkout we use the short
# SHA; in an unpacked source ZIP (no .git) it falls back to "dev".
VERSION ?= $(shell git describe --always --dirty 2>/dev/null || echo dev)

# -s -w  = drop symbol table and DWARF (smaller file, same as CI).
# -X     = overwrite package-main variables at link time.
LDFLAGS ?= -s -w -X main.version=$(VERSION)

# Disable CGO so the result does not depend on local gcc / libc.
export CGO_ENABLED := 0

.PHONY: all test build windows darwin dist clean help

all: test build

help:
	@echo "targets: all test build windows darwin dist clean"

# -count=1 disables the test cache — always run them for real.
test:
	go test -count=1 -timeout 60s ./...

# Binary for the OS you are running make on (Linux → ELF, macOS → Mach-O).
build:
	go build -buildvcs=false -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY) .

# Cross-compile for Windows (same file names as the GitHub Release).
windows:
	GOOS=windows GOARCH=amd64 go build -buildvcs=false -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY)-windows-amd64.exe .
	GOOS=windows GOARCH=arm64 go build -buildvcs=false -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY)-windows-arm64.exe .

# Cross-compile for macOS. No Xcode needed on Linux/Windows.
darwin:
	GOOS=darwin GOARCH=amd64 go build -buildvcs=false -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY)-darwin-amd64 .
	GOOS=darwin GOARCH=arm64 go build -buildvcs=false -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY)-darwin-arm64 .

# Recreate CI artifacts (except the source ZIP — that is `git archive`).
dist: test windows darwin
	mkdir -p dist
	mv -f $(BINARY)-windows-amd64.exe $(BINARY)-windows-arm64.exe dist/
	mv -f $(BINARY)-darwin-amd64 $(BINARY)-darwin-arm64 dist/

clean:
	rm -f $(BINARY) $(BINARY).exe
	rm -f $(BINARY)-windows-amd64.exe $(BINARY)-windows-arm64.exe
	rm -f $(BINARY)-darwin-amd64 $(BINARY)-darwin-arm64
	rm -rf dist
