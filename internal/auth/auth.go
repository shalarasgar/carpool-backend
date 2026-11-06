// Package auth creates OTP codes, phone numbers, and tokens.
// It does not decide HTTP status codes and it does not send SMS.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

var (
	// ErrInvalidPhone means the number is not a Turkmenistan mobile number we can text.
	ErrInvalidPhone = errors.New("invalid phone")
	// ErrInvalidCode means the OTP is missing, expired, used, or wrong.
	ErrInvalidCode = errors.New("invalid code")
	// ErrBlocked means the user account cannot sign in.
	ErrBlocked = errors.New("blocked")
	// ErrUnauthorized means a refresh token is missing, expired, or revoked.
	ErrUnauthorized = errors.New("unauthorized")
	// ErrValidation means a field failed a local check.
	ErrValidation = errors.New("validation")
)

// NormalizePhone accepts +993XXXXXXXX, 993XXXXXXXX, and an 8-digit national number.
// prefixes are the allowed first digits of the national number, for example 6 and 7.
func NormalizePhone(raw string, prefixes []string) (string, error) {
	s := strings.TrimSpace(raw)
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "-", "")
	switch {
	case strings.HasPrefix(s, "00993"):
		s = "+" + s[2:]
	case strings.HasPrefix(s, "993") && len(s) == 11:
		s = "+" + s
	case len(s) == 8 && s[0] >= '0' && s[0] <= '9':
		s = "+993" + s
	}
	if len(s) != 12 || !strings.HasPrefix(s, "+993") {
		return "", ErrInvalidPhone
	}
	national := s[4:]
	for _, c := range national {
		if c < '0' || c > '9' {
			return "", ErrInvalidPhone
		}
	}
	if len(prefixes) == 0 {
		return "", ErrInvalidPhone
	}
	ok := false
	for _, p := range prefixes {
		if p != "" && strings.HasPrefix(national, p) {
			ok = true
			break
		}
	}
	if !ok {
		return "", ErrInvalidPhone
	}
	return s, nil
}

// RandomCode returns a zero-padded 6-digit code from crypto/rand.
func RandomCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", fmt.Errorf("otp code: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// HashOTP is HMAC-SHA256 of the code. The raw code is never stored.
func HashOTP(secret, code string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(code))
	return hex.EncodeToString(mac.Sum(nil))
}

// OTPEqual compares a candidate code with a stored HMAC.
func OTPEqual(secret, code, storedHash string) bool {
	return hmac.Equal([]byte(HashOTP(secret, code)), []byte(storedHash))
}

// HashSecret is SHA-256 of a high-entropy token. Gateway API keys and refresh tokens use it.
func HashSecret(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// SecretEqual compares two secrets without leaking the length in the comparison itself.
func SecretEqual(got, want string) bool {
	g := sha256.Sum256([]byte(got))
	w := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(g[:], w[:]) == 1
}

// NewToken returns a URL-safe random token and its SHA-256 hex hash.
func NewToken(prefix string) (raw, hash string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("token: %w", err)
	}
	raw = prefix + base64.RawURLEncoding.EncodeToString(buf)
	return raw, HashSecret(raw), nil
}

// Locale picks tk, ru, or en from an Accept-Language header. The default is tk.
func Locale(header string) string {
	for _, part := range strings.Split(header, ",") {
		tag := strings.TrimSpace(strings.Split(part, ";")[0])
		tag = strings.ToLower(tag)
		switch {
		case strings.HasPrefix(tag, "tk"):
			return "tk"
		case strings.HasPrefix(tag, "ru"):
			return "ru"
		case strings.HasPrefix(tag, "en"):
			return "en"
		}
	}
	return "tk"
}

// Challenge is one OTP issuance and the SMS that carries it.
type Challenge struct {
	Phone           string
	CodeHash        string
	IP              string
	DeviceID        string
	Message         string
	ExpiresAt       time.Time
	Now             time.Time
	PhoneInterval   time.Duration
	PhoneDailyMax   int
	IPHourlyMax     int
	DeviceHourlyMax int
	// SkipLimits is set for the configured test numbers (OTP_TEST_PHONES).
	SkipLimits bool
}

// ChallengeResult reports whether the SMS was pinned to a live gateway.
type ChallengeResult struct {
	NoHealthyGateway bool
}

// VerifySpec is the input of one OTP check. Match stays in the caller so the HMAC secret
// never needs to be stored beside the SQL.
type VerifySpec struct {
	Phone         string
	DeviceID      string
	Locale        string
	Now           time.Time
	MaxAttempts   int
	RefreshHash   string
	RefreshExpiry time.Time
	Match         func(storedHash string) bool
}

// SessionUser is the account bound to a refresh token.
type SessionUser struct {
	ID     string
	Phone  string
	Name   string
	Locale string
	IsNew  bool
}

// ValidDeviceID restricts the client device id stored next to an OTP or refresh token.
func ValidDeviceID(id string) bool {
	if len(id) < 1 || len(id) > 128 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '.' || c == '_' || c == ':' || c == '-':
		default:
			return false
		}
	}
	return true
}

type accessClaims struct {
	Subject string `json:"sub"`
	Issued  int64  `json:"iat"`
	Expires int64  `json:"exp"`
	Type    string `json:"typ"`
}

// SignAccess creates a compact HS256 JWT. The header algorithm is fixed to HS256.
func SignAccess(secret, userID string, now time.Time, ttl time.Duration) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, err := json.Marshal(accessClaims{
		Subject: userID,
		Issued:  now.Unix(),
		Expires: now.Add(ttl).Unix(),
		Type:    "access",
	})
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	unsigned := header + "." + body
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// ParseAccess checks the HMAC and the expiry. It rejects any algorithm other than HS256.
func ParseAccess(secret, token string, now time.Time) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", ErrUnauthorized
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", ErrUnauthorized
	}
	var header struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil || header.Alg != "HS256" {
		return "", ErrUnauthorized
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		return "", ErrUnauthorized
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ErrUnauthorized
	}
	var claims accessClaims
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Type != "access" || claims.Subject == "" {
		return "", ErrUnauthorized
	}
	if now.Unix() >= claims.Expires {
		return "", ErrUnauthorized
	}
	return claims.Subject, nil
}
