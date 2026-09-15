// Lukas-Anti-Censorship-Proxy (short name: lacp) is a local, transparent
// HTTP CONNECT + SOCKS5 proxy.
//
// Quickly, how it works. I was intrigued that the government's censorship
// is not DNS based. So I started to check how it is built. During the process,
// I've disovered (using openssl s_client) tool, that spoofing SNI (Server Name
// Identification) works, You are able to finally reach the website with browser
// warnings, without getting redirected to censor webpage (like Vodafone), but it
// breaks TLS (obviously). Using Grok, I discovered that by fragmenting TCP packets
// there, where SNI begins I can bypass filters because they are not gluing packets :)
//
// So this tool does not terminate TLS (no MITM, no custom certificate).
// It works fully locally. The only change it makes to HTTPS traffic is splitting
// the ClientHello TLS record into two records so the first one does not contain
// the SNI (server_name) extension.
// The destination server reassembles both halves into the original handshake
// message, so the TLS transcript stays identical.
//
// Deleting SNI from ClientHello is cryptographically impossible in
// pass-through mode: that field is hashed into the handshake transcript and
// the client would see "bad record MAC". Hence fragmentation, not stripping.
//
// Settings live in lacp.json (next to the executable). CLI flags override
// the file when given. Start (Windows): .\lacp.exe   Start (Unix): ./lacp
package main

import (
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
)

const (
	appName  = "Lukas-Anti-Censorship-Proxy"
	appShort = "lacp"
)

// version and commit are "dev" / "unknown" for a plain `go build`.
// GitHub Actions overwrites them at link time:
//
//	go build -ldflags "-X main.version=master-abc1234 -X main.commit=<full SHA>"
//
// A release binary can then print which commit it was built from.
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	configPath := flag.String("config", "",
		"path to lacp.json (default: file next to the executable, then the working directory)")
	host := flag.String("host", "", "override config host (default 127.0.0.1)")
	port := flag.Int("port", 0, "override config port (default 45777)")
	timeout := flag.Duration("timeout", 0, "override config outbound dial timeout")
	gap := flag.Duration("gap", 0, "override config pause between ClientHello fragments")
	quiet := flag.Bool("quiet", false, "override config: disable per-connection logs")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	cfg, usedPath, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Only flags the user actually passed beat the file. Unmentioned flags
	// keep the JSON / default values (so -quiet false does not clobber
	// `"quiet": true` unless the user typed -quiet).
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "host":
			cfg.Host = *host
		case "port":
			cfg.Port = *port
		case "timeout":
			cfg.Timeout = *timeout
		case "gap":
			cfg.Gap = *gap
		case "quiet":
			cfg.Quiet = *quiet
		}
	})
	if err := cfg.validate(); err != nil {
		log.Fatalf("config: %v", err)
	}

	listen := cfg.ListenAddr()
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		log.Fatalf("listen %s: %v", listen, err)
	}

	p := &Proxy{
		DialTimeout: cfg.Timeout,
		FragmentGap: cfg.Gap,
		Quiet:       cfg.Quiet,
	}

	log.Printf("%s (%s) %s (commit %s)", appName, appShort, version, commit)
	if usedPath != "" {
		log.Printf("config file: %s", usedPath)
	} else {
		log.Printf("config file: none (built-in defaults)")
	}
	log.Printf("listening on %s", ln.Addr())
	log.Printf("Vivaldi/Chrome shortcut: --proxy-server=http://%s", ln.Addr())
	log.Printf("SOCKS5 (same port):      --proxy-server=socks5://%s", ln.Addr())
	log.Printf("Transparent: does not decrypt TLS. Splits ClientHello so SNI is not in the first record/packet.")

	// Ctrl+C / SIGTERM closes the listener → Serve returns → the process
	// exits after Accept() unblocks. The OS tears down open tunnels on exit.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		_ = ln.Close()
	}()

	if err := p.Serve(ln); err != nil {
		log.Printf("stopped: %v", err)
	}
}
