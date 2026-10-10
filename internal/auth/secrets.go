package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

const (
	refreshTokenBytes = 32
	apiKeyBytes       = 32

	// APIKeyPrefix marks gateway keys so they are recognizable in config files.
	APIKeyPrefix = "gw_"
)

// NewRefreshToken returns a random opaque refresh token (base64url, no padding).
func NewRefreshToken() (string, error) {
	return randomToken(refreshTokenBytes, "")
}

// NewAPIKey returns a random gateway API key.
func NewAPIKey() (string, error) {
	return randomToken(apiKeyBytes, APIKeyPrefix)
}

// HashSecret returns the hex SHA-256 of an opaque token. Refresh tokens and
// API keys are random 256-bit values, so a plain hash is enough to store them.
func HashSecret(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// SecretsEqual compares two secrets in constant time.
func SecretsEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func randomToken(n int, prefix string) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("random token: %w", err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(buf), nil
}
