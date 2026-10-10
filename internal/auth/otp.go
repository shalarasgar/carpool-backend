package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"time"
)

// OTPLength is the number of digits in a one-time code.
const OTPLength = 6

// OTPOutcome is the result of checking a code against a stored record.
type OTPOutcome int

const (
	// OTPOK means the code matched and the record should be marked used.
	OTPOK OTPOutcome = iota
	// OTPInvalid means the code did not match; attempts should be incremented.
	OTPInvalid
	// OTPExpired means the record is past its expiry.
	OTPExpired
	// OTPAttemptsExceeded means the record has no attempts left.
	OTPAttemptsExceeded
)

// OTPRecord is the stored state of a code. It mirrors the otp_codes row.
type OTPRecord struct {
	Phone     string
	CodeHash  string
	ExpiresAt time.Time
	Attempts  int
	Used      bool
}

// OTP generates and verifies one-time codes. Only an HMAC of the code is
// stored; the HMAC covers the phone so a code cannot be replayed for another number.
type OTP struct {
	secret      []byte
	maxAttempts int
}

// NewOTP returns a verifier. maxAttempts is the number of wrong codes allowed
// before the record is invalidated.
func NewOTP(secret string, maxAttempts int) *OTP {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	return &OTP{secret: []byte(secret), maxAttempts: maxAttempts}
}

// MaxAttempts returns the configured limit.
func (o *OTP) MaxAttempts() int { return o.maxAttempts }

// Generate returns a zero-padded 6-digit code from crypto/rand.
func (o *OTP) Generate() (string, error) {
	limit := big.NewInt(1)
	for i := 0; i < OTPLength; i++ {
		limit.Mul(limit, big.NewInt(10))
	}
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return "", fmt.Errorf("generate otp: %w", err)
	}
	return fmt.Sprintf("%0*d", OTPLength, n.Int64()), nil
}

// Hash returns the hex HMAC-SHA256 of phone and code under the server secret.
func (o *OTP) Hash(phone, code string) string {
	mac := hmac.New(sha256.New, o.secret)
	mac.Write([]byte(phone))
	mac.Write([]byte{0})
	mac.Write([]byte(code))
	return hex.EncodeToString(mac.Sum(nil))
}

// Check evaluates a submitted code against a stored record at time now. It is
// pure: the caller persists the resulting state change. The comparison is
// constant-time and runs even when the record is already unusable so that
// timing does not reveal record state.
func (o *OTP) Check(rec OTPRecord, code string, now time.Time) OTPOutcome {
	expected := []byte(rec.CodeHash)
	actual := []byte(o.Hash(rec.Phone, code))
	match := hmac.Equal(expected, actual)

	switch {
	case rec.Used:
		return OTPInvalid
	case !now.Before(rec.ExpiresAt):
		return OTPExpired
	case rec.Attempts >= o.maxAttempts:
		return OTPAttemptsExceeded
	case !ValidOTPFormat(code):
		return OTPInvalid
	case match:
		return OTPOK
	default:
		return OTPInvalid
	}
}

// ValidOTPFormat reports whether code is exactly OTPLength ASCII digits.
func ValidOTPFormat(code string) bool {
	return len(code) == OTPLength && isDigits(code)
}
