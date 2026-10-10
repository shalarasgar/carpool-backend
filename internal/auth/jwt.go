package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrInvalidToken is returned for access tokens that fail validation.
var ErrInvalidToken = errors.New("invalid access token")

// Claims are the JWT claims of an access token. Subject is the user id.
type Claims struct {
	jwt.RegisteredClaims
	DeviceID string `json:"did,omitempty"`
}

// JWT signs and verifies HS256 access tokens.
type JWT struct {
	secret []byte
	issuer string
	ttl    time.Duration
	now    func() time.Time
}

// NewJWT returns a signer. ttl is the access token lifetime.
func NewJWT(secret, issuer string, ttl time.Duration) *JWT {
	return &JWT{secret: []byte(secret), issuer: issuer, ttl: ttl, now: time.Now}
}

// TTL returns the access token lifetime.
func (j *JWT) TTL() time.Duration { return j.ttl }

// Issue signs a token for userID bound to deviceID.
func (j *JWT) Issue(userID, deviceID string) (string, error) {
	now := j.now()
	jti, err := randomToken(12, "")
	if err != nil {
		return "", err
	}
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			Issuer:    j.issuer,
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-30 * time.Second)),
			ExpiresAt: jwt.NewNumericDate(now.Add(j.ttl)),
		},
		DeviceID: deviceID,
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(j.secret)
	if err != nil {
		return "", fmt.Errorf("sign access token: %w", err)
	}
	return signed, nil
}

// Verify parses and validates a token. Only HS256 is accepted.
func (j *JWT) Verify(token string) (*Claims, error) {
	claims := &Claims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return j.secret, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(j.issuer),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(j.now),
	)
	if err != nil || !parsed.Valid || claims.Subject == "" {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
