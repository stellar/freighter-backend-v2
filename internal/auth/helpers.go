package auth

import (
	"crypto/sha256"
	"encoding/hex"
)

// HashBody returns the hex-encoded SHA-256 of body. It binds a JWT to the exact
// request payload (the `bodyHash` claim). A nil/empty body hashes to the
// well-known SHA-256 of the empty input, which is what clients use for GET
// requests that carry no body.
func HashBody(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
