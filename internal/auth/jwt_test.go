package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestJWTIssueVerify(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	j := NewJWT("jwt-secret-value-long-enough", "carpool", 15*time.Minute)
	j.now = func() time.Time { return now }

	token, err := j.Issue("user-1", "device-1")
	require.NoError(t, err)

	token2, err := j.Issue("user-1", "device-1")
	require.NoError(t, err)
	require.NotEqual(t, token, token2, "jti makes tokens unique even within the same second")

	claims, err := j.Verify(token)
	require.NoError(t, err)
	require.NotEmpty(t, claims.ID)
	require.Equal(t, "user-1", claims.Subject)
	require.Equal(t, "device-1", claims.DeviceID)
	require.Equal(t, "carpool", claims.Issuer)
	require.Equal(t, now.Add(15*time.Minute).Unix(), claims.ExpiresAt.Unix())

	j.now = func() time.Time { return now.Add(15*time.Minute + time.Second) }
	_, err = j.Verify(token)
	require.ErrorIs(t, err, ErrInvalidToken, "expired")

	j.now = func() time.Time { return now }
	other := NewJWT("another-secret-value-long-enough", "carpool", 15*time.Minute)
	_, err = other.Verify(token)
	require.ErrorIs(t, err, ErrInvalidToken, "wrong secret")

	otherIssuer := NewJWT("jwt-secret-value-long-enough", "someone-else", 15*time.Minute)
	otherIssuer.now = j.now
	_, err = otherIssuer.Verify(token)
	require.ErrorIs(t, err, ErrInvalidToken, "wrong issuer")

	_, err = j.Verify("not-a-token")
	require.ErrorIs(t, err, ErrInvalidToken)
}

func TestJWTRejectsNoneAlgorithm(t *testing.T) {
	t.Parallel()

	j := NewJWT("jwt-secret-value-long-enough", "carpool", 15*time.Minute)
	unsigned := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"iss": "carpool",
		"sub": "user-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	token, err := unsigned.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	_, err = j.Verify(token)
	require.ErrorIs(t, err, ErrInvalidToken)
}

func TestSecrets(t *testing.T) {
	t.Parallel()

	rt, err := NewRefreshToken()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(rt), 43)

	rt2, err := NewRefreshToken()
	require.NoError(t, err)
	require.NotEqual(t, rt, rt2)

	key, err := NewAPIKey()
	require.NoError(t, err)
	require.True(t, len(key) > len(APIKeyPrefix) && key[:len(APIKeyPrefix)] == APIKeyPrefix)

	require.Len(t, HashSecret(rt), 64)
	require.Equal(t, HashSecret(rt), HashSecret(rt))
	require.NotEqual(t, HashSecret(rt), HashSecret(rt2))

	require.True(t, SecretsEqual("abc", "abc"))
	require.False(t, SecretsEqual("abc", "abd"))
	require.False(t, SecretsEqual("abc", "abcd"))
}
