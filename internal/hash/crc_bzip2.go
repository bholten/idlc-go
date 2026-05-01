// Package hash computes the 32-bit name hash that idlc bakes into generated
// C++ for persistent fields and on-the-wire dispatch.
//
// The algorithm is CRC-32/BZIP2:
//
//	polynomial:  0x04C11DB7
//	init:        0xFFFFFFFF
//	reflect in:  no
//	reflect out: no
//	final XOR:   0xFFFFFFFF
//
// Go's hash/crc32 doesn't ship a preconfigured BZIP2 variant (it uses
// bit-reflected CRCs; BZIP2 doesn't), so we build the lookup table by hand.
//
// The hash input is the ASCII string "ClassName.fieldName" — the same
// string the JAR emits as a comment next to each hash constant in the
// generated C++ (e.g. "0xbd8f57ac //ChatMessage.message"). Reproducing
// these values byte-for-byte is a hard requirement: existing BerkeleyDB
// records are keyed by them.
package hash

var crcTable = func() (t [256]uint32) {
	const poly uint32 = 0x04C11DB7

	for i := 0; i < 256; i++ {
		c := uint32(i) << 24

		for j := 0; j < 8; j++ {
			if c&0x80000000 != 0 {
				c = (c << 1) ^ poly
			} else {
				c <<= 1
			}
		}

		t[i] = c
	}

	return
}()

// NameHash returns the CRC-32/BZIP2 of s. Pass the literal string idlc
// uses as the hash input — typically "ClassName.fieldName".
func NameHash(s string) uint32 {
	crc := uint32(0xFFFFFFFF)

	for i := 0; i < len(s); i++ {
		crc = crcTable[((crc>>24)^uint32(s[i]))&0xFF] ^ (crc << 8)
	}

	return ^crc
}
