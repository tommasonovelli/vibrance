package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// A token is "vb_" and 32 random bytes in unpadded base64url (DESIGN.md
// §7.3): 46 characters. It is the value of the session cookie and the bearer
// token alike. Only its SHA-256 is stored (I5): whoever reads the database
// cannot use a session.
const (
	tokenPrefix   = "vb_"
	tokenBytes    = 32
	tokenLen      = len(tokenPrefix) + 43
	tokenAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
)

// newToken makes a token and the hash to store for it.
func newToken() (token, hash string, err error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("reading random bytes for a token: %w", err)
	}
	token = tokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	return token, hashToken(token), nil
}

// hashToken is the SHA-256 of a token, in lowercase hexadecimal. A plain
// hash is enough: a token has 256 bits of entropy, so nothing can be
// guessed from it.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// wellFormed tells whether s has the form of a token. What has not is no
// session, and the database is not asked.
func wellFormed(s string) bool {
	if len(s) != tokenLen || !strings.HasPrefix(s, tokenPrefix) {
		return false
	}
	for _, c := range []byte(s[len(tokenPrefix):]) {
		if strings.IndexByte(tokenAlphabet, c) < 0 {
			return false
		}
	}
	return true
}
