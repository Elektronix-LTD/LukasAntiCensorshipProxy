package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// Max HTTP header size before CONNECT. 64 KiB covers long
	// Proxy-Authorization / User-Agent lists; anything bigger is dropped.
	maxHeaderBytes = 64 << 10
	// io.Copy buffer between client and server after the handshake.
	copyBufSize = 32 << 10
)

// Buffer pool so many parallel tunnels do not each allocate 32 KiB from
// scratch (less GC pressure).
var copyBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, copyBufSize)
		return &b
	},
}

// Proxy is the listening server. One instance handles both HTTP CONNECT
// (Chrome/Vivaldi with --proxy-server=http://…) and SOCKS5
// (--proxy-server=socks5://…) — distinguished by the first byte.
type Proxy struct {
	DialTimeout time.Duration // TCP timeout to the CONNECT host
	FragmentGap time.Duration // pause between the two ClientHello writes
	Quiet       bool          // no "hid SNI …" logs
}

func (p *Proxy) Serve(ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		setTCPOpts(c)
		go p.handle(c)
	}
}

func (p *Proxy) handle(c net.Conn) {
	defer c.Close()
	// Deadline only for the "client must speak" phase (CONNECT / SOCKS
	// greeting). After the tunnel is up we clear it — HTTPS keep-alive
	// may sit idle for minutes.
	_ = c.SetDeadline(time.Now().Add(p.dialTimeout()))

	br := bufio.NewReaderSize(c, 16*1024)
	peek, err := br.Peek(1)
	if err != nil {
		return
	}
	// SOCKS5 starts with VER=0x05. HTTP starts with a letter ('C' from
	// CONNECT, 'G' from GET).
	if peek[0] == 0x05 {
		p.handleSOCKS5(c, br)
		return
	}
	p.handleHTTP(c, br)
}

// handleHTTP is a regular HTTP proxy. Browser HTTPS is always CONNECT
// (a TCP tunnel). GET/POST with an absolute URL is leftover plaintext HTTP,
// forwarded without touching TLS.
func (p *Proxy) handleHTTP(client net.Conn, br *bufio.Reader) {
	head, err := readHTTPHead(br)
	if err != nil {
		return
	}
	first, _, _ := bytes.Cut(head, []byte("\n"))
	first = bytes.TrimRight(first, "\r")
	parts := bytes.SplitN(first, []byte(" "), 3)
	if len(parts) < 2 {
		return
	}
	method := strings.ToUpper(string(parts[0]))
	target := string(parts[1])

	var hostport string
	if method == "CONNECT" {
		hostport = target
	} else {
		u, err := url.Parse(target)
		if err != nil || u.Host == "" {
			fmt.Fprintf(client, "HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n")
			return
		}
		hostport = u.Host
		if _, _, err := net.SplitHostPort(hostport); err != nil {
			hostport = net.JoinHostPort(hostport, "80")
		}
	}

	server, err := p.dial(hostport)
	if err != nil {
		if method == "CONNECT" {
			fmt.Fprintf(client, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
		}
		p.logf("dial %s: %v", hostport, err)
		return
	}
	defer server.Close()

	if method == "CONNECT" {
		if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		_ = client.SetDeadline(time.Time{})
		p.relay(client, br, server, hostport)
		return
	}

	if _, err := server.Write(head); err != nil {
		return
	}
	_ = client.SetDeadline(time.Time{})
	p.pipe(br, client, server)
}

// handleSOCKS5: CONNECT only, no password (a local proxy does not need auth).
// UDP ASSOCIATE / BIND return 0x07 (command not supported).
func (p *Proxy) handleSOCKS5(client net.Conn, br *bufio.Reader) {
	// greeting: VER, NMETHODS, METHODS
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(br, hdr); err != nil {
		return
	}
	if hdr[0] != 0x05 {
		return
	}
	methods := make([]byte, int(hdr[1]))
	if _, err := io.ReadFull(br, methods); err != nil {
		return
	}
	if _, err := client.Write([]byte{0x05, 0x00}); err != nil { // no auth
		return
	}

	req := make([]byte, 4)
	if _, err := io.ReadFull(br, req); err != nil {
		return
	}
	if req[0] != 0x05 {
		return
	}
	if req[1] != 0x01 { // CONNECT only
		socks5Reply(client, 0x07) // command not supported
		return
	}

	host, port, err := readSOCKS5Addr(br, req[3])
	if err != nil {
		socks5Reply(client, 0x01)
		return
	}
	hostport := net.JoinHostPort(host, port)

	server, err := p.dial(hostport)
	if err != nil {
		socks5Reply(client, 0x05) // connection refused
		p.logf("dial %s: %v", hostport, err)
		return
	}
	defer server.Close()

	if err := socks5Reply(client, 0x00); err != nil {
		return
	}
	_ = client.SetDeadline(time.Time{})
	p.relay(client, br, server, hostport)
}

func (p *Proxy) dial(hostport string) (net.Conn, error) {
	d := net.Dialer{
		Timeout:   p.dialTimeout(),
		KeepAlive: 30 * time.Second,
	}
	c, err := d.Dial("tcp", hostport)
	if err != nil {
		return nil, err
	}
	setTCPOpts(c)
	return c, nil
}

// relay is the tunnel after CONNECT/SOCKS: client→server with SNI
// fragmentation, server→client unchanged. clientR is a bufio.Reader because
// some bytes (start of ClientHello) may already have been buffered by peek
// / header parsing.
func (p *Proxy) relay(client net.Conn, clientR io.Reader, server net.Conn, hostport string) {
	onHide := func(sni string) {
		p.logf("%s  hid SNI %q (ClientHello fragmented)", hostport, sni)
	}

	errc := make(chan struct{})
	go func() {
		_ = hideAndCopy(server, clientR, p.FragmentGap, onHide)
		closeWrite(server)
		errc <- struct{}{}
	}()
	go func() {
		_, _ = copyBuf(client, server)
		closeWrite(client)
		errc <- struct{}{}
	}()
	<-errc
	<-errc
}

func (p *Proxy) pipe(clientR io.Reader, clientW net.Conn, server net.Conn) {
	errc := make(chan struct{})
	go func() {
		_, _ = copyBuf(server, clientR)
		closeWrite(server)
		errc <- struct{}{}
	}()
	go func() {
		_, _ = copyBuf(clientW, server)
		closeWrite(clientW)
		errc <- struct{}{}
	}()
	<-errc
	<-errc
}

func (p *Proxy) dialTimeout() time.Duration {
	if p.DialTimeout > 0 {
		return p.DialTimeout
	}
	return 15 * time.Second
}

func (p *Proxy) logf(format string, args ...any) {
	if p.Quiet {
		return
	}
	log.Printf(format, args...)
}

func readHTTPHead(br *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			return nil, err
		}
		buf = append(buf, line...)
		if len(buf) > maxHeaderBytes {
			return nil, io.ErrShortBuffer
		}
		if bytes.HasSuffix(buf, []byte("\r\n\r\n")) || bytes.HasSuffix(buf, []byte("\n\n")) {
			return buf, nil
		}
	}
}

func readSOCKS5Addr(r io.Reader, atyp byte) (host, port string, err error) {
	var addr []byte
	switch atyp {
	case 0x01: // IPv4
		addr = make([]byte, 4)
		if _, err = io.ReadFull(r, addr); err != nil {
			return "", "", err
		}
		host = net.IP(addr).String()
	case 0x04: // IPv6
		addr = make([]byte, 16)
		if _, err = io.ReadFull(r, addr); err != nil {
			return "", "", err
		}
		host = net.IP(addr).String()
	case 0x03: // domain
		var n [1]byte
		if _, err = io.ReadFull(r, n[:]); err != nil {
			return "", "", err
		}
		addr = make([]byte, n[0])
		if _, err = io.ReadFull(r, addr); err != nil {
			return "", "", err
		}
		host = string(addr)
	default:
		return "", "", fmt.Errorf("unsupported SOCKS ATYP %d", atyp)
	}
	var pb [2]byte
	if _, err = io.ReadFull(r, pb[:]); err != nil {
		return "", "", err
	}
	port = fmt.Sprintf("%d", binary.BigEndian.Uint16(pb[:]))
	return host, port, nil
}

func socks5Reply(w io.Writer, status byte) error {
	_, err := w.Write([]byte{0x05, status, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	return err
}

// setTCPOpts: TCP_NODELAY is essential — without it Nagle would glue both
// ClientHello fragments into one packet and the -gap trick would do nothing.
func setTCPOpts(c net.Conn) {
	tcp, ok := c.(*net.TCPConn)
	if !ok {
		return
	}
	_ = tcp.SetNoDelay(true)
	_ = tcp.SetKeepAlive(true)
	_ = tcp.SetKeepAlivePeriod(30 * time.Second)
}

func closeWrite(c net.Conn) {
	if tcp, ok := c.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	}
}

func copyBuf(dst io.Writer, src io.Reader) (int64, error) {
	bufp := copyBufPool.Get().(*[]byte)
	defer copyBufPool.Put(bufp)
	return io.CopyBuffer(dst, src, *bufp)
}
