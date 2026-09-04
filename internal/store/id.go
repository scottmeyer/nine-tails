package store

import (
	"crypto/rand"
	"fmt"
)

// Identifiers are <prefix>_<ULID> (DESIGN §2): 48 bits of clock milliseconds
// followed by 80 random bits, Crockford base32, 26 characters. They are not
// meant to be read. Two stores can never mint the same id, so a copied or
// forked store merges back without renumbering.
const ulidAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewID returns a fresh identifier with the given prefix. The time part
// comes from Clock, so a fixed NINE_TAILS_NOW pins it; the random part is
// never repeated.
func NewID(prefix string) (string, error) {
	var id [16]byte
	ms := uint64(Clock().UnixMilli())
	id[0], id[1], id[2] = byte(ms>>40), byte(ms>>32), byte(ms>>24)
	id[3], id[4], id[5] = byte(ms>>16), byte(ms>>8), byte(ms)
	if _, err := rand.Read(id[6:]); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return prefix + "_" + encodeULID(id), nil
}

// encodeULID is the canonical ULID text form: 128 bits into 26 base32
// characters, the first carrying only the top three bits.
func encodeULID(id [16]byte) string {
	const enc = ulidAlphabet
	var dst [26]byte
	dst[0] = enc[(id[0]&224)>>5]
	dst[1] = enc[id[0]&31]
	dst[2] = enc[(id[1]&248)>>3]
	dst[3] = enc[((id[1]&7)<<2)|((id[2]&192)>>6)]
	dst[4] = enc[(id[2]&62)>>1]
	dst[5] = enc[((id[2]&1)<<4)|((id[3]&240)>>4)]
	dst[6] = enc[((id[3]&15)<<1)|((id[4]&128)>>7)]
	dst[7] = enc[(id[4]&124)>>2]
	dst[8] = enc[((id[4]&3)<<3)|((id[5]&224)>>5)]
	dst[9] = enc[id[5]&31]
	dst[10] = enc[(id[6]&248)>>3]
	dst[11] = enc[((id[6]&7)<<2)|((id[7]&192)>>6)]
	dst[12] = enc[(id[7]&62)>>1]
	dst[13] = enc[((id[7]&1)<<4)|((id[8]&240)>>4)]
	dst[14] = enc[((id[8]&15)<<1)|((id[9]&128)>>7)]
	dst[15] = enc[(id[9]&124)>>2]
	dst[16] = enc[((id[9]&3)<<3)|((id[10]&224)>>5)]
	dst[17] = enc[id[10]&31]
	dst[18] = enc[(id[11]&248)>>3]
	dst[19] = enc[((id[11]&7)<<2)|((id[12]&192)>>6)]
	dst[20] = enc[(id[12]&62)>>1]
	dst[21] = enc[((id[12]&1)<<4)|((id[13]&240)>>4)]
	dst[22] = enc[((id[13]&15)<<1)|((id[14]&128)>>7)]
	dst[23] = enc[(id[14]&124)>>2]
	dst[24] = enc[((id[14]&3)<<3)|((id[15]&224)>>5)]
	dst[25] = enc[id[15]&31]
	return string(dst[:])
}
