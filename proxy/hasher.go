package proxy

import (
	"encoding/hex"
	"strings"

	"golang.org/x/crypto/blake2b"
)

// digestSize is the BLAKE2b output length in bytes. Frozen at 16 (128 bits):
// the fixture v1 format encodes a 16-byte digest as 8 hyphen-separated 4-char
// hex groups (e.g. 0b2d-c84a-bed5-1060-0a97-4328-ed90-42db). Changing this
// invalidates every fixture, so it must never change within a major version.
const digestSize = 16

// Hash returns a hyphen-segmented BLAKE2b digest for the input bytes:
// a 16-byte digest rendered as 8 groups of 4 hex chars joined by hyphens.
func Hash(input []byte) string {
	h, err := blake2b.New(digestSize, nil)
	if err != nil {
		// Only errors on an invalid size/key; digestSize is a valid constant.
		panic(err)
	}
	h.Write(input)
	hexDigest := hex.EncodeToString(h.Sum(nil))
	return formatHash(hexDigest)
}

func formatHash(hexDigest string) string {
	if len(hexDigest) != digestSize*2 {
		return hexDigest
	}

	parts := make([]string, 0, digestSize/2)
	for i := 0; i < len(hexDigest); i += 4 {
		end := i + 4
		if end > len(hexDigest) {
			end = len(hexDigest)
		}
		parts = append(parts, hexDigest[i:end])
	}
	return strings.Join(parts, "-")
}
