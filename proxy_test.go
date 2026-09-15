// End-to-end tunnel tests: local TLS server + proxy (HTTP CONNECT and SOCKS5).
// After fragmentation the backend MUST still see SNI — proof that the
// transcript was left intact. TestDirectTLSKeepsSNI is the control that the
// client actually sends SNI when it does not go through the proxy.
package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPConnectHidesSNI(t *testing.T) {
	backend, saw := startTLSBackend(t)
	proxyAddr := startProxy(t)

	c, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", backend, backend)
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("CONNECT status %s", resp.Status)
	}

	var tunnel net.Conn = c
	if br.Buffered() > 0 {
		tunnel = &prefixConn{r: br, Conn: c}
	}

	cli := tls.Client(tunnel, &tls.Config{
		ServerName:         "secret.example",
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
	})
	defer cli.Close()
	if err := cli.Handshake(); err != nil {
		t.Fatalf("handshake through proxy: %v", err)
	}
	if s := saw.Load().(string); s != "secret.example" {
		t.Fatalf("backend saw SNI %q, want secret.example (fragment, not strip)", s)
	}
}

func TestDirectTLSKeepsSNI(t *testing.T) {
	backend, saw := startTLSBackend(t)
	c, err := tls.Dial("tcp", backend, &tls.Config{
		ServerName:         "secret.example",
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if s := saw.Load().(string); s != "secret.example" {
		t.Fatalf("direct SNI = %q, want secret.example", s)
	}
}

func TestSOCKS5HidesSNI(t *testing.T) {
	backend, saw := startTLSBackend(t)
	proxyAddr := startProxy(t)

	c, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	host, portStr, err := net.SplitHostPort(backend)
	if err != nil {
		t.Fatal(err)
	}
	p, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	var portBytes [2]byte
	portBytes[0] = byte(p >> 8)
	portBytes[1] = byte(p)

	if _, err := c.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	greet := make([]byte, 2)
	if _, err := io.ReadFull(c, greet); err != nil {
		t.Fatal(err)
	}
	if greet[0] != 0x05 || greet[1] != 0x00 {
		t.Fatalf("socks greeting %x", greet)
	}

	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))}
	req = append(req, host...)
	req = append(req, portBytes[:]...)
	if _, err := c.Write(req); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(c, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != 0x00 {
		t.Fatalf("socks reply status %d", reply[1])
	}

	cli := tls.Client(c, &tls.Config{
		ServerName:         "secret.example",
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
	})
	defer cli.Close()
	if err := cli.Handshake(); err != nil {
		t.Fatalf("handshake through SOCKS5: %v", err)
	}
	if s := saw.Load().(string); s != "secret.example" {
		t.Fatalf("backend saw SNI %q, want secret.example (fragment, not strip)", s)
	}
}

type prefixConn struct {
	r io.Reader
	net.Conn
}

func (c *prefixConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func startProxy(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &Proxy{DialTimeout: 5 * time.Second, Quiet: true}
	go func() { _ = p.Serve(ln) }()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}

func startTLSBackend(t *testing.T) (addr string, saw *atomic.Value) {
	t.Helper()
	cert := selfSigned(t)
	saw = &atomic.Value{}
	saw.Store("")
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		GetConfigForClient: func(chi *tls.ClientHelloInfo) (*tls.Config, error) {
			saw.Store(chi.ServerName)
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				tc, ok := c.(*tls.Conn)
				if !ok {
					return
				}
				_ = tc.Handshake()
			}(c)
		}
	}()
	return ln.Addr().String(), saw
}

func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "secret.example"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"secret.example"},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}
