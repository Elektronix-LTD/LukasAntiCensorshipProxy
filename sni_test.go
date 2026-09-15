// ClientHello parser and fragmentation tests.
//
// Contract that must not be broken: concatenated payload of both records ==
// the original handshake message. Breaking it breaks live TLS.
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"testing"
)

func TestFragmentRemovesSNIFromFirstRecord(t *testing.T) {
	alpn := appendExt(nil, 0x0010, []byte{0x00, 0x02, 0x01, 'h'})
	rec := buildClientHello("example.com", alpn)

	rec1, rec2, sni, ok := fragmentClientHello(rec)
	if !ok {
		t.Fatal("expected fragmentation")
	}
	if sni != "example.com" {
		t.Fatalf("sni = %q", sni)
	}
	if bytes.Contains(rec1, []byte("example.com")) {
		t.Fatal("SNI hostname leaked into first record")
	}
	if hasExt(rec1, extServerName) {
		t.Fatal("SNI extension present in first record")
	}
	if !bytes.Contains(rec2, []byte("example.com")) {
		t.Fatal("SNI hostname missing from second record")
	}
	assertHandshakePreserved(t, rec, rec1, rec2)
	if !hasExt(reassembleRecord(t, rec1, rec2), 0x0010) {
		t.Fatal("ALPN lost after reassembly")
	}
}

func TestFragmentOnlySNI(t *testing.T) {
	rec := buildClientHello("only.sni.test", nil)
	rec1, rec2, sni, ok := fragmentClientHello(rec)
	if !ok || sni != "only.sni.test" {
		t.Fatalf("ok=%v sni=%q", ok, sni)
	}
	if bytes.Contains(rec1, []byte("only.sni.test")) {
		t.Fatal("hostname in first record")
	}
	assertHandshakePreserved(t, rec, rec1, rec2)
}

func TestFragmentNoSNI(t *testing.T) {
	alpn := appendExt(nil, 0x0010, []byte{0x00, 0x02, 0x01, 'h'})
	rec := buildClientHello("", alpn)
	if _, _, _, ok := fragmentClientHello(rec); ok {
		t.Fatal("should not fragment a ClientHello without SNI")
	}
}

func TestFragmentRejectsGarbage(t *testing.T) {
	cases := [][]byte{
		nil,
		{0x16, 0x03, 0x01},
		{0x17, 0x03, 0x03, 0x00, 0x01, 0x00},
		{0x16, 0x03, 0x01, 0x00, 0x04, 0x02, 0x00, 0x00, 0x00},
	}
	for i, in := range cases {
		if _, _, _, ok := fragmentClientHello(in); ok {
			t.Fatalf("case %d: unexpectedly fragmented", i)
		}
	}
}

func TestFragmentRealTLSClientHello(t *testing.T) {
	rec := captureClientHello(t, "real.example.com")
	if !hasExt(rec, extServerName) {
		t.Fatal("captured ClientHello has no SNI")
	}
	rec1, rec2, sni, ok := fragmentClientHello(rec)
	if !ok {
		t.Fatal("expected fragmentation")
	}
	if sni != "real.example.com" {
		t.Fatalf("sni = %q", sni)
	}
	if bytes.Contains(rec1, []byte("real.example.com")) {
		t.Fatal("SNI hostname leaked into first record")
	}
	assertHandshakePreserved(t, rec, rec1, rec2)
}

func TestHideAndCopyPassthroughAfterClientHello(t *testing.T) {
	hello := buildClientHello("a.example", appendExt(nil, 0x000d, []byte{0x00, 0x02, 0x04, 0x03}))
	rest := []byte{0x17, 0x03, 0x03, 0x00, 0x04, 'd', 'a', 't', 'a'}
	src := io.MultiReader(bytes.NewReader(hello), bytes.NewReader(rest))
	var dst bytes.Buffer
	var got string
	if err := hideAndCopy(&dst, src, 0, func(sni string) { got = sni }); err != nil {
		t.Fatal(err)
	}
	if got != "a.example" {
		t.Fatalf("sni = %q", got)
	}
	out := dst.Bytes()
	if !bytes.HasSuffix(out, rest) {
		t.Fatal("trailing appdata not forwarded")
	}
	if !bytes.Contains(out, []byte("a.example")) {
		t.Fatal("full stream missing SNI (server would not see it)")
	}
}

func captureClientHello(t *testing.T, serverName string) []byte {
	t.Helper()
	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})
	go func() {
		cfg := &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}
		_ = tls.Client(c1, cfg).Handshake()
	}()
	buf := make([]byte, 16*1024)
	n, err := c2.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	rec := buf[:n]
	if len(rec) < 5 {
		t.Fatalf("short read %d", n)
	}
	recLen := 5 + int(binary.BigEndian.Uint16(rec[3:5]))
	if recLen > len(rec) {
		t.Fatalf("incomplete ClientHello record %d > %d", recLen, len(rec))
	}
	return rec[:recLen]
}

func buildClientHello(sni string, extraExts []byte) []byte {
	var exts []byte
	if sni != "" {
		name := []byte(sni)
		entry := []byte{0x00} // hostname
		var nl [2]byte
		binary.BigEndian.PutUint16(nl[:], uint16(len(name)))
		entry = append(entry, nl[:]...)
		entry = append(entry, name...)
		var listLen [2]byte
		binary.BigEndian.PutUint16(listLen[:], uint16(len(entry)))
		exts = appendExt(exts, extServerName, append(listLen[:], entry...))
	}
	exts = append(exts, extraExts...)

	ch := make([]byte, 0, 128+len(exts))
	ch = append(ch, 0x03, 0x03)
	ch = append(ch, make([]byte, 32)...)
	ch = append(ch, 0x00) // empty session id
	ch = append(ch, 0x00, 0x02, 0x00, 0x2f)
	ch = append(ch, 0x01, 0x00) // null compression
	if len(exts) > 0 {
		var el [2]byte
		binary.BigEndian.PutUint16(el[:], uint16(len(exts)))
		ch = append(ch, el[:]...)
		ch = append(ch, exts...)
	}

	hs := []byte{handshakeClientHello, byte(len(ch) >> 16), byte(len(ch) >> 8), byte(len(ch))}
	hs = append(hs, ch...)
	rec := []byte{recordTypeHandshake, 0x03, 0x01, byte(len(hs) >> 8), byte(len(hs))}
	return append(rec, hs...)
}

func appendExt(dst []byte, typ uint16, data []byte) []byte {
	var hdr [4]byte
	binary.BigEndian.PutUint16(hdr[0:2], typ)
	binary.BigEndian.PutUint16(hdr[2:4], uint16(len(data)))
	dst = append(dst, hdr[:]...)
	return append(dst, data...)
}

func assertHandshakePreserved(t *testing.T, orig, rec1, rec2 []byte) {
	t.Helper()
	origHS := payload(t, orig)
	got := append(append([]byte{}, payload(t, rec1)...), payload(t, rec2)...)
	if !bytes.Equal(origHS, got) {
		t.Fatalf("handshake payload changed (%d -> %d bytes)", len(origHS), len(got))
	}
}

func reassembleRecord(t *testing.T, rec1, rec2 []byte) []byte {
	t.Helper()
	hs := append(payload(t, rec1), payload(t, rec2)...)
	return makeHandshakeRecord(rec1[1:3], hs)
}

func payload(t *testing.T, rec []byte) []byte {
	t.Helper()
	if len(rec) < 5 || rec[0] != recordTypeHandshake {
		t.Fatalf("not a handshake record: len=%d", len(rec))
	}
	n := int(binary.BigEndian.Uint16(rec[3:5]))
	if 5+n != len(rec) {
		t.Fatalf("record length %d != %d", n, len(rec)-5)
	}
	return rec[5:]
}

func hasExt(rec []byte, typ uint16) bool {
	if len(rec) < 9 || rec[0] != recordTypeHandshake {
		return false
	}
	recLen := int(binary.BigEndian.Uint16(rec[3:5]))
	if 5+recLen > len(rec) {
		return false
	}
	body := rec[5 : 5+recLen]
	if body[0] != handshakeClientHello {
		return false
	}
	hsLen := int(body[1])<<16 | int(body[2])<<8 | int(body[3])
	if 4+hsLen > len(body) {
		return false
	}
	ch := body[4 : 4+hsLen]
	pos := 34
	if len(ch) < pos+1 {
		return false
	}
	pos += 1 + int(ch[34])
	if pos+2 > len(ch) {
		return false
	}
	pos += 2 + int(binary.BigEndian.Uint16(ch[pos:pos+2]))
	if pos+1 > len(ch) {
		return false
	}
	pos += 1 + int(ch[pos])
	if pos+2 > len(ch) {
		return false
	}
	extLen := int(binary.BigEndian.Uint16(ch[pos : pos+2]))
	pos += 2
	end := pos + extLen
	for pos+4 <= end {
		etype := binary.BigEndian.Uint16(ch[pos : pos+2])
		elen := int(binary.BigEndian.Uint16(ch[pos+2 : pos+4]))
		if etype == typ {
			return true
		}
		pos += 4 + elen
	}
	return false
}
