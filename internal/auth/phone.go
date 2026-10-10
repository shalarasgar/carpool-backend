// Package auth implements phone normalization, OTP hashing, JWT access
// tokens, opaque refresh tokens and gateway API keys. It has no HTTP or
// database dependencies.
package auth

import (
	"errors"
	"strings"
)

// ErrInvalidPhone is returned for phone numbers that are not Turkmen mobile numbers.
var ErrInvalidPhone = errors.New("invalid phone number")

const (
	countryCode    = "+993"
	nationalDigits = 8
)

// PhoneNormalizer turns user input into E.164 (+993XXXXXXXX) and checks the
// national prefix against an allow-list.
type PhoneNormalizer struct {
	allowedPrefixes []string
}

// NewPhoneNormalizer accepts the digit prefixes that may follow +993, for
// example []string{"6", "7"} for the Turkmen mobile ranges.
func NewPhoneNormalizer(allowedPrefixes []string) *PhoneNormalizer {
	prefixes := make([]string, 0, len(allowedPrefixes))
	for _, p := range allowedPrefixes {
		p = strings.TrimSpace(p)
		if p != "" {
			prefixes = append(prefixes, p)
		}
	}
	return &PhoneNormalizer{allowedPrefixes: prefixes}
}

// Normalize accepts +993XXXXXXXX, 993XXXXXXXX, 00993XXXXXXXX, 8XXXXXXXX
// (domestic trunk prefix) and bare XXXXXXXX, with optional spaces, dashes,
// dots and parentheses. It returns ErrInvalidPhone for anything else.
func (n *PhoneNormalizer) Normalize(raw string) (string, error) {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '.', '(', ')', '\t':
			return -1
		}
		return r
	}, strings.TrimSpace(raw))

	if cleaned == "" || len(cleaned) > 16 {
		return "", ErrInvalidPhone
	}

	var national string
	switch {
	case strings.HasPrefix(cleaned, countryCode):
		national = cleaned[len(countryCode):]
	case strings.HasPrefix(cleaned, "00993"):
		national = cleaned[len("00993"):]
	case strings.HasPrefix(cleaned, "993") && len(cleaned) == 3+nationalDigits:
		national = cleaned[3:]
	case strings.HasPrefix(cleaned, "8") && len(cleaned) == 1+nationalDigits:
		national = cleaned[1:]
	case len(cleaned) == nationalDigits:
		national = cleaned
	default:
		return "", ErrInvalidPhone
	}

	if len(national) != nationalDigits || !isDigits(national) {
		return "", ErrInvalidPhone
	}
	if !n.prefixAllowed(national) {
		return "", ErrInvalidPhone
	}
	return countryCode + national, nil
}

func (n *PhoneNormalizer) prefixAllowed(national string) bool {
	for _, p := range n.allowedPrefixes {
		if strings.HasPrefix(national, p) {
			return true
		}
	}
	return false
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// MaskPhone hides the middle of an E.164 number for logs: +99365***456.
func MaskPhone(phone string) string {
	if len(phone) <= 6 {
		return "***"
	}
	return phone[:6] + "***" + phone[len(phone)-3:]
}
