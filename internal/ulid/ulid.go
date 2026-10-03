// Package ulid makes the ULIDs the lab's simulators put in msg_id
// (envelope/v1: Crockford base32, 26 characters, 48-bit milliseconds then
// 80 random bits). Lab-internal; nothing here parses a ULID it did not
// make.
package ulid

import (
	"crypto/rand"
	"encoding/binary"
	"time"
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Make returns the ULID of t with 80 bits from crypto/rand.
func Make(t time.Time) string {
	var b [16]byte
	ms := uint64(t.UnixMilli())
	binary.BigEndian.PutUint16(b[0:2], uint16(ms>>32))
	binary.BigEndian.PutUint32(b[2:6], uint32(ms))
	_, _ = rand.Read(b[6:]) // crypto/rand.Read never returns an error (Go 1.24+)
	return Encode(b)
}

// Encode is the 26-character Crockford encoding of 128 bits.
func Encode(b [16]byte) string {
	var out [26]byte
	hi := binary.BigEndian.Uint64(b[0:8])
	lo := binary.BigEndian.Uint64(b[8:16])
	for i := 25; i >= 0; i-- {
		out[i] = crockford[lo&0x1f]
		lo = lo>>5 | hi<<59
		hi >>= 5
	}
	return string(out[:])
}
