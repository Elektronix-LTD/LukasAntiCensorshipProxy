package main

import (
	"encoding/binary"
	"errors"
	"io"
	"time"
)

// TLS record types (first byte of TLSPlaintext). Source: RFC 5246 / 8446.
const (
	recordTypeCCS       = 0x14 // ChangeCipherSpec
	recordTypeAlert     = 0x15
	recordTypeHandshake = 0x16
	recordTypeAppData   = 0x17

	handshakeClientHello = 0x01
	extServerName        = 0x0000 // RFC 6066 — the only extension we split on

	// TLSPlaintext: max 2^14 bytes of fragment + 2 KiB cipher-expansion slack.
	maxTLSRecord = 18432
)

var errNotTLSRecord = errors.New("not a TLS record")

// sniLoc is the position of the SNI extension inside a complete TLS record.
type sniLoc struct {
	name    string
	extOff  int // record offset of the extension type field (0x0000)
	nameOff int // record offset of the ASCII hostname
}

// fragmentClientHello splits a complete ClientHello record into TWO valid
// handshake records. Concatenated payloads are byte-for-byte identical to the
// original — required, because the TLS transcript hashes the handshake
// message, not the record headers. The first record ends just before the SNI
// extension, so DPI looking at the "first packet" never sees server_name.
func fragmentClientHello(record []byte) (rec1, rec2 []byte, sni string, ok bool) {
	loc, ok := locateSNI(record)
	if !ok {
		return nil, nil, "", false
	}
	recLen := int(binary.BigEndian.Uint16(record[3:5]))
	if 5+recLen > len(record) || recLen < 4 {
		return nil, nil, "", false
	}
	hs := record[5 : 5+recLen]
	// Split at the SNI extension so record 1 has no server_name type or hostname.
	split := loc.extOff - 5
	if split < 4 || split >= len(hs) {
		return nil, nil, "", false
	}
	ver := record[1:3]
	return makeHandshakeRecord(ver, hs[:split]), makeHandshakeRecord(ver, hs[split:]), loc.name, true
}

func makeHandshakeRecord(ver, payload []byte) []byte {
	out := make([]byte, 5+len(payload))
	out[0] = recordTypeHandshake
	out[1] = ver[0]
	out[2] = ver[1]
	binary.BigEndian.PutUint16(out[3:5], uint16(len(payload)))
	copy(out[5:], payload)
	return out
}

func locateSNI(record []byte) (sniLoc, bool) {
	var z sniLoc
	if len(record) < 9 || record[0] != recordTypeHandshake {
		return z, false
	}
	recLen := int(binary.BigEndian.Uint16(record[3:5]))
	if recLen < 4 || 5+recLen > len(record) {
		return z, false
	}
	body := record[5 : 5+recLen]
	if body[0] != handshakeClientHello {
		return z, false
	}
	hsLen := int(body[1])<<16 | int(body[2])<<8 | int(body[3])
	if hsLen < 1 || 4+hsLen > len(body) {
		return z, false
	}

	// Offsets below are within the ClientHello body (after handshake header).
	ch := body[4 : 4+hsLen]
	chBase := 5 + 4 // record header + handshake header
	pos := 34
	if len(ch) < pos+1 {
		return z, false
	}
	pos += 1 + int(ch[34])
	if pos+2 > len(ch) {
		return z, false
	}
	pos += 2 + int(binary.BigEndian.Uint16(ch[pos:pos+2]))
	if pos+1 > len(ch) {
		return z, false
	}
	pos += 1 + int(ch[pos])
	if pos+2 > len(ch) {
		return z, false
	}
	extLen := int(binary.BigEndian.Uint16(ch[pos : pos+2]))
	pos += 2
	end := pos + extLen
	if end > len(ch) {
		return z, false
	}
	for pos+4 <= end {
		etype := binary.BigEndian.Uint16(ch[pos : pos+2])
		elen := int(binary.BigEndian.Uint16(ch[pos+2 : pos+4]))
		if pos+4+elen > end {
			return z, false
		}
		if etype == extServerName {
			z.extOff = chBase + pos
			z.name = parseSNI(ch[pos+4 : pos+4+elen])
			if z.name != "" {
				// hostname starts 5 bytes into SNI data (list_len(2)+type(1)+name_len(2)).
				z.nameOff = chBase + pos + 4 + 5
			}
			return z, z.name != ""
		}
		pos += 4 + elen
	}
	return z, false
}

func parseSNI(data []byte) string {
	if len(data) < 5 {
		return ""
	}
	listLen := int(binary.BigEndian.Uint16(data[0:2]))
	pos := 2
	end := 2 + listLen
	if end > len(data) {
		end = len(data)
	}
	for pos+3 <= end {
		nameType := data[pos]
		nameLen := int(binary.BigEndian.Uint16(data[pos+1 : pos+3]))
		pos += 3
		if pos+nameLen > end {
			return ""
		}
		if nameType == 0 {
			return string(data[pos : pos+nameLen])
		}
		pos += nameLen
	}
	return ""
}

func readTLSRecord(r io.Reader, hdr []byte) ([]byte, error) {
	if len(hdr) < 5 {
		hdr = make([]byte, 5)
	}
	if _, err := io.ReadFull(r, hdr[:5]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(hdr[3:5]))
	if n < 0 || n > maxTLSRecord {
		return nil, errNotTLSRecord
	}
	rec := make([]byte, 5+n)
	copy(rec, hdr[:5])
	if _, err := io.ReadFull(r, rec[5:]); err != nil {
		return nil, err
	}
	return rec, nil
}

// hideAndCopy copies the client stream to the server. Every cleartext
// ClientHello is fragmented (SNI drops out of the first record). The loop
// runs until the client leaves the unencrypted handshake (CCS, appdata, or
// alert) — then the rest is a raw io.Copy. A second ClientHello after a
// TLS 1.3 HelloRetryRequest is split too, because it is still handshake type.
func hideAndCopy(dst io.Writer, src io.Reader, gap time.Duration, onHide func(sni string)) error {
	hdr := make([]byte, 5)
	for {
		rec, err := readTLSRecord(src, hdr)
		if err != nil {
			if errors.Is(err, errNotTLSRecord) {
				if _, werr := dst.Write(hdr[:5]); werr != nil {
					return werr
				}
				_, err = io.Copy(dst, src)
				return err
			}
			return err
		}

		if rec[0] == recordTypeHandshake {
			if rec1, rec2, sni, ok := fragmentClientHello(rec); ok {
				if onHide != nil {
					onHide(sni)
				}
				if _, err := dst.Write(rec1); err != nil {
					return err
				}
				if gap > 0 {
					time.Sleep(gap)
				}
				if _, err := dst.Write(rec2); err != nil {
					return err
				}
				continue
			}
		}

		if _, err := dst.Write(rec); err != nil {
			return err
		}
		switch rec[0] {
		case recordTypeCCS, recordTypeAppData, recordTypeAlert:
			_, err := io.Copy(dst, src)
			return err
		}
	}
}
