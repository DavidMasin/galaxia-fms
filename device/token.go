// Device tokens: the password a device presents when it connects.

package device

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
)

// GenerateToken returns a new random token for a device created by an operator: 32 random bytes in URL-safe
// base64, so that it can be pasted into firmware and URLs alike.
func GenerateToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// HashToken returns the hex SHA-256 of a token, which is all the FMS stores. Tokens are long random strings, so a
// slow password hash would add nothing.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// tokenMatches reports whether token hashes to the stored hash, in constant time. Nothing matches an empty hash: a
// device approved without ever presenting a token (one on a serial port, say) cannot be impersonated over MQTT.
func tokenMatches(hash, token string) bool {
	if hash == "" {
		return false
	}
	presented := HashToken(token)
	return subtle.ConstantTimeCompare([]byte(hash), []byte(presented)) == 1
}
