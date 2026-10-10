package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOTPGenerate(t *testing.T) {
	t.Parallel()

	o := NewOTP("test-secret-value-long-enough", 5)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		code, err := o.Generate()
		require.NoError(t, err)
		require.Len(t, code, OTPLength)
		require.True(t, ValidOTPFormat(code), "code %q", code)
		seen[code] = true
	}
	require.Greater(t, len(seen), 150, "codes should be random")
}

func TestOTPHashBindsPhoneAndSecret(t *testing.T) {
	t.Parallel()

	a := NewOTP("secret-a-secret-a-secret-a", 5)
	b := NewOTP("secret-b-secret-b-secret-b", 5)

	h := a.Hash("+99365123456", "123456")
	require.Len(t, h, 64)
	require.Equal(t, h, a.Hash("+99365123456", "123456"))
	require.NotEqual(t, h, a.Hash("+99365123457", "123456"), "different phone")
	require.NotEqual(t, h, a.Hash("+99365123456", "123457"), "different code")
	require.NotEqual(t, h, b.Hash("+99365123456", "123456"), "different secret")
	require.NotContains(t, h, "123456")
}

func TestOTPCheck(t *testing.T) {
	t.Parallel()

	o := NewOTP("test-secret-value-long-enough", 5)
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	phone := "+99365123456"
	rec := OTPRecord{
		Phone:     phone,
		CodeHash:  o.Hash(phone, "482913"),
		ExpiresAt: now.Add(5 * time.Minute),
	}

	require.Equal(t, OTPOK, o.Check(rec, "482913", now))
	require.Equal(t, OTPOK, o.Check(rec, "482913", now.Add(5*time.Minute-time.Second)))
	require.Equal(t, OTPInvalid, o.Check(rec, "482914", now))
	require.Equal(t, OTPInvalid, o.Check(rec, "", now))
	require.Equal(t, OTPInvalid, o.Check(rec, "48291", now))
	require.Equal(t, OTPInvalid, o.Check(rec, "4829133", now))
	require.Equal(t, OTPInvalid, o.Check(rec, "48291a", now))

	expired := rec
	require.Equal(t, OTPExpired, o.Check(expired, "482913", now.Add(5*time.Minute)))
	require.Equal(t, OTPExpired, o.Check(expired, "482913", now.Add(time.Hour)))

	used := rec
	used.Used = true
	require.Equal(t, OTPInvalid, o.Check(used, "482913", now))

	exhausted := rec
	exhausted.Attempts = 5
	require.Equal(t, OTPAttemptsExceeded, o.Check(exhausted, "482913", now), "correct code after 5 failures is rejected")

	almost := rec
	almost.Attempts = 4
	require.Equal(t, OTPOK, o.Check(almost, "482913", now))

	otherPhone := rec
	otherPhone.Phone = "+99365123457"
	require.Equal(t, OTPInvalid, o.Check(otherPhone, "482913", now), "code is bound to the phone")
}

func TestOTPMaxAttemptsFloor(t *testing.T) {
	t.Parallel()

	require.Equal(t, 1, NewOTP("s", 0).MaxAttempts())
	require.Equal(t, 3, NewOTP("s", 3).MaxAttempts())
}
