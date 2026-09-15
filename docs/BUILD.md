# Building Lukas-Anti-Censorship-Proxy from source

This document is for people who downloaded `lacp-source.zip` from GitHub
Releases (Linux, or a custom build on any OS) and for anyone who cloned
the repository.

Short binary name: **lacp** / **lacp.exe**.

## Requirements

- **Go 1.22 or newer** — [https://go.dev/dl/](https://go.dev/dl/)
- C developer tools are **not** required. The project uses `CGO_ENABLED=0`.
- On Windows: PowerShell or cmd. `go` must be on `PATH`
  (after `winget install GoLang.Go`, **open a new** terminal).

Check:

```bash
go version
```

It should print `go1.22` or higher.

## Quick start (Linux / macOS)

```bash
unzip lacp-source.zip
cd lacp-<sha>          # directory name is inside the ZIP
go test ./...
go build -ldflags="-s -w" -o lacp .
./lacp
```

Or with the Makefile:

```bash
make            # tests + binary for the current OS
./lacp
```

## Quick start (Windows, PowerShell)

```powershell
Expand-Archive .\lacp-source.zip -DestinationPath .
cd lacp-<sha>
go test ./...
go build -ldflags="-s -w" -o lacp.exe .
.\lacp.exe
```

`go build` **only compiles**. It prints nothing on success and returns
immediately to the prompt. The server starts only when you run `.\lacp.exe`.

## `go build` flags used by CI

| Flag | Why |
| --- | --- |
| `-trimpath` | Builder machine paths do not leak into the binary (reproducibility). |
| `-ldflags="-s -w"` | Drops the symbol table and DWARF — smaller file. |
| `-X main.version=…` | Stamps the release tag into `version` (shown at startup). |
| `-X main.commit=…` | Stamps the full commit SHA. |
| `CGO_ENABLED=0` | Zero libc / gcc dependency. |

Same as CI, for your own OS:

```bash
CGO_ENABLED=0 go build -trimpath \
  -ldflags="-s -w -X main.version=dev -X main.commit=$(git rev-parse HEAD 2>/dev/null || echo unknown)" \
  -o lacp .
```

## Cross-compilation (from Linux / macOS / Windows)

Go does not need macOS to produce a macOS binary, nor the Windows SDK to
produce an `.exe`. Change `GOOS` and `GOARCH`.

```bash
# Windows 64-bit Intel/AMD
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
  -o lacp-windows-amd64.exe .

# Windows ARM64
GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
  -o lacp-windows-arm64.exe .

# macOS Intel
GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
  -o lacp-darwin-amd64 .

# macOS Apple Silicon
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
  -o lacp-darwin-arm64 .

# Linux 64-bit (when compiling e.g. on Windows)
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
  -o lacp-linux-amd64 .
```

Shortcuts: `make windows`, `make darwin`, or `make dist`.

### GOOS / GOARCH table

| System | GOOS | GOARCH |
| --- | --- | --- |
| Windows Intel/AMD 64-bit | `windows` | `amd64` |
| Windows ARM64 | `windows` | `arm64` |
| macOS Intel | `darwin` | `amd64` |
| macOS Apple Silicon | `darwin` | `arm64` |
| Linux Intel/AMD 64-bit | `linux` | `amd64` |
| Linux ARM64 (Raspberry Pi 4/5, servers) | `linux` | `arm64` |

## Tests

```bash
go test -count=1 -timeout 60s -v ./...
```

- `sni_test.go` — ClientHello parser, fragmentation, TLS transcript identity.
- `proxy_test.go` — real handshake through HTTP CONNECT and SOCKS5
  (a local TLS server checks that SNI **still reaches** the server after
  the records are reassembled — because deleting SNI breaks TLS).

CI runs the same suite before every release. If a test fails, the release
**is not created**.

## Source layout

```
.
├── lacp.go                         # CLI flags, config load, listen loop, version
├── lacp.json                       # default settings (host, port, timeout, gap, quiet)
├── config.go                       # JSON config load / create / validate
├── config_test.go                  # config unit tests
├── proxy.go                        # HTTP CONNECT + SOCKS5, TCP splice
├── sni.go                          # SNI location and ClientHello fragmentation
├── sni_test.go / proxy_test.go     # unit and e2e tests
├── go.mod                          # module `lacp`, minimum Go version
├── Makefile                        # local build targets
├── README.md                       # English user guide
├── README-pl.md                    # Polish user guide
├── .github/workflows/release.yml   # GitHub Actions pipeline
└── docs/                           # this documentation
```

External dependencies: **none**. Standard library only.

## Troubleshooting

**`go: command not found` / “term 'go' is not recognized”**
: Go is not on `PATH`. On Windows, close and reopen the terminal after install.

**`go build` finishes immediately, nothing happens**
: That is a successful compile. Run the resulting file (`./lacp` or
`.\lacp.exe`).

**`listen: bind: address already in use`**
: That TCP port is taken. Another proxy instance or some other program.
Kill the old process, or change `"port"` in `lacp.json` (or pass `-port`).

**macOS: “cannot be opened because the developer cannot be verified”**
: The binary is not signed by Apple (CI does not notarize).
`xattr -cr lacp-darwin-arm64` and run again, or
*Privacy & Security → Open Anyway*.

**Windows SmartScreen: “Windows protected your PC”**
: Same story — unsigned `.exe`. *More info → Run anyway*.

**Tests `handshake through proxy: …`**
: On a very slow machine increase the timeout: `go test -timeout 120s ./...`.
Normally they finish in a fraction of a second.
