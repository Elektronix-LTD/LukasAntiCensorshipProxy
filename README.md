# Lukas-Anti-Censorship-Proxy

Short name: **lacp**. Local, transparent HTTP CONNECT + SOCKS5 proxy.
**It works locally on Your computer and it does not decrypt TLS.** 

## What is it for?
It is for bypassing Censorship filters (for example United Kingdom's ones).
So You will be able to visit websites which are blocked by the government 
without need for VPN.

## Is it hard to setup?
No, just one minute. You run the program and You change two things in Windows network settings.

1. Run the program (lacp.exe). You should see command line console. Minimize it and forget about it.
2. Go to Windows Settings > Network & Internet > Proxy server (on the left)
3. In the section "Manual configuration of proxy server" turn proxy server on by switching "Turn proxy server on" on.
4. Fill these two fields like on the picture below:
![How to configure proxy in Windows](how-to-configure.png)
5. You can optionally check the checkbox "Don't use proxy server for local addresses (intranet)"
6. You might need to change Windows DNS servers to some uncensored ones as well:

   Try:
    - https://blog.uncensoreddns.org
    - https://dns.watch
    - 
   Or IT giants ones:
    - [CloudFlare](https://one.one.one.one)
    - [Google DNS](https://developers.google.com/speed/public-dns?hl=pl)
 


## How it exactly works?
On the handshake it splits the `ClientHello` record in two, so the
 **first TLS record and the first TCP packet do not contain SNI**
(the `server_name` extension). The server reassembles the original 
`ClientHello` in full — the TLS transcript stays intact and
websites keep working.

> Removing SNI from `ClientHello` **breaks HTTPS**. That field is hashed
> into the TLS 1.2/1.3 transcript; client and server then compute different
> checksums and the handshake fails (`bad record MAC`). This tool **hides**
> SNI from simple DPI; it does not cut it out of the handshake message.

Polish documentation: [README-pl.md](README-pl.md).

## Quick start

### 1. Download a pre-built binary (Windows / macOS)

After the repo is on GitHub, every commit on `master` creates a Release:

`https://github.com/<user>/<repo>/releases/latest`

| File | System |
| --- | --- |
| `lacp-windows-amd64.exe` | Windows 64-bit (typical PC) |
| `lacp-windows-arm64.exe` | Windows ARM64 |
| `lacp-darwin-amd64` | macOS Intel |
| `lacp-darwin-arm64` | macOS Apple Silicon (M1+) |
| `lacp-source.zip` | Linux and self-compilation |

### 2. Run it and **leave the window open**

Windows (PowerShell) — the `.\` prefix is required:

```powershell
.\lacp-windows-amd64.exe
```

or, after a local build:

```powershell
.\lacp.exe
```

macOS / Linux:

```bash
chmod +x lacp-darwin-arm64
./lacp-darwin-arm64
```

Success looks like this (the process **does not** return to the prompt):

```
Lukas-Anti-Censorship-Proxy (lacp) master-a1b2c3d (commit a1b2c3d4e5f6…)
listening on 127.0.0.1:45777
Vivaldi/Chrome shortcut: --proxy-server=http://127.0.0.1:45777
SOCKS5 (same port):      --proxy-server=socks5://127.0.0.1:45777
```

`go build` / `make` **does not** start the server — it only compiles. A
command that returns immediately with no error is normal.


## Configuration (`lacp.json`)

Settings live in **`lacp.json`** next to `lacp.exe` (or in the working
directory). On first run, if the file is missing, lacp writes one with
defaults so you can edit it and restart.

```json
{
  "host": "127.0.0.1",
  "port": 45777,
  "timeout": "15s",
  "gap": "10ms",
  "quiet": false
}
```

| Key | Default | Meaning |
| --- | --- | --- |
| `host` | `127.0.0.1` | Address to bind. Keep loopback; `0.0.0.0` would make this an open proxy. |
| `port` | `45777` | TCP port the browser proxy setting must match. |
| `timeout` | `15s` | Outbound connection (dial) timeout. Go duration (`15s`, `1m`, …). |
| `gap` | `10ms` | Pause between the two ClientHello fragments so the TCP stack does not merge them into one packet. |
| `quiet` | `false` | `true` turns off per-connection logs. |

Change `"port": 45777` to whatever you want, save, restart lacp, and point
Vivaldi/Chrome at the same port (`--proxy-server=http://127.0.0.1:PORT`).

You can still override the file from the command line (only flags you
actually pass win; the rest stay as in JSON):

```powershell
.\lacp.exe -config .\lacp.json -port 9999
```

| Flag | Meaning |
| --- | --- |
| `-config` | Path to the JSON file (default: auto-discover `lacp.json`) |
| `-host` | Override `host` |
| `-port` | Override `port` |
| `-timeout` | Override `timeout` |
| `-gap` | Override `gap` |
| `-quiet` | Override `quiet` |

Ctrl+C stops the server.

## How it works (short)

1. The browser issues `CONNECT example.com:443` (HTTPS) or SOCKS5 CONNECT.
2. The proxy dials the target and returns `200 Connection Established`.
3. It reads the first TLS record. If that is a `ClientHello` with SNI:
   - it splits the handshake message **at that extension**,
   - wraps both halves in separate TLS records (legal fragmentation per
     RFC 5246 / 8446 — the transcript hashes the reassembled message, not
     the records),
   - writes record 1, waits `-gap`, writes record 2 (`TCP_NODELAY`).
4. The rest of the bytes in both directions pass through unchanged (splice).

DPI that only inspects the first record / first packet never sees the host
name. The destination server **still receives SNI** after reassembly —
virtual hosting and CDN certificates work.

## What it does not do

- It is not a VPN. **The destination IP is still visible.**
- It will not beat DPI that reassembles the full TCP stream and reads the
  second record.
- It does not bypass IP-based blocks.
- HTTP/3 (QUIC) often skips an HTTP proxy; Chrome/Vivaldi typically fall
  back to HTTP/2 when a proxy is set.
- It does not install a certificate and does not MITM.

## Documentation

| Document | Topic |
| --- | --- |
| [README-pl.md](README-pl.md) | Polish version of this readme |
| [docs/BUILD.md](docs/BUILD.md) | Building from source, `GOOS`/`GOARCH`, tests, troubleshooting |
| [docs/RELEASES.md](docs/RELEASES.md) | GitHub Actions: every commit on `master` = a new release |
| [`.github/workflows/release.yml`](.github/workflows/release.yml) | The pipeline itself, step by step, with comments |

## Build in one line

```bash
go test ./... && go build -ldflags="-s -w" -o lacp .
```

On Windows the output is `lacp.exe`. Details, cross-compilation, Makefile:
[docs/BUILD.md](docs/BUILD.md).

## Developer requirements

- Go 1.22+
- Zero dependencies outside the Go standard library (`go.mod` has no `require`)

## License

This code is provided as-is, without warranty. Use at your own risk. The
tool is for privacy of your own traffic, not for attacking other systems.
