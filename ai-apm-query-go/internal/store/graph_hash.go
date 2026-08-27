package store

import (
	"crypto/sha256"
	"encoding/hex"
)

func sha256HexBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
