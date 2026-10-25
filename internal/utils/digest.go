package utils

import (
	"crypto/sha256"
	"encoding/base64"
)

func DigestToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}
