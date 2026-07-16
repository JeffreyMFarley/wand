package proxy

import (
	"encoding/hex"
	"strings"

	"golang.org/x/crypto/blake2b"
)

// Hash returns a hyphen-segmented 32-char BLAKE2b digest for the input bytes.
func Hash(input []byte) string {
	digest := blake2b.Sum256(input)
	hexDigest := hex.EncodeToString(digest[:])
	return formatHash(hexDigest)
}

func formatHash(hexDigest string) string {
	if len(hexDigest) != 64 {
		return hexDigest
	}

	parts := make([]string, 0, 8)
	for i := 0; i < len(hexDigest); i += 4 {
		end := i + 4
		if end > len(hexDigest) {
			end = len(hexDigest)
		}
		parts = append(parts, hexDigest[i:end])
	}
	return strings.Join(parts, "-")
}
