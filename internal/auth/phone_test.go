package auth

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizePhone(t *testing.T) {
	t.Parallel()

	n := NewPhoneNormalizer([]string{"6", "7"})

	valid := map[string]string{
		"+99365123456":     "+99365123456",
		"+993 65 12 34 56": "+99365123456",
		"+993-65-123456":   "+99365123456",
		"99365123456":      "+99365123456",
		"0099365123456":    "+99365123456",
		"865123456":        "+99365123456",
		"8 (65) 12-34-56":  "+99365123456",
		"65123456":         "+99365123456",
		"+99371234567":     "+99371234567",
		"  +99362000000 ":  "+99362000000",
	}
	for in, want := range valid {
		got, err := n.Normalize(in)
		require.NoError(t, err, "input %q", in)
		require.Equal(t, want, got, "input %q", in)
	}

	invalid := []string{
		"",
		"   ",
		"+99312345678",      // landline prefix 1 is not allowed
		"+99355123456",      // prefix 5 is not allowed
		"+9936512345",       // too short
		"+993651234567",     // too long
		"+7 912 345 67 89",  // wrong country
		"+99365a23456",      // letters
		"65123456x",         // trailing junk
		"+993+65123456",     // double plus
		"99365123456789012", // far too long
		"12345678",          // bare national with bad prefix
	}
	for _, in := range invalid {
		_, err := n.Normalize(in)
		require.Truef(t, errors.Is(err, ErrInvalidPhone), "input %q: err = %v", in, err)
	}
}

func TestNormalizePhoneConfigurablePrefixes(t *testing.T) {
	t.Parallel()

	n := NewPhoneNormalizer([]string{"65", "71"})
	_, err := n.Normalize("+99365123456")
	require.NoError(t, err)
	_, err = n.Normalize("+99371123456")
	require.NoError(t, err)
	_, err = n.Normalize("+99362123456")
	require.ErrorIs(t, err, ErrInvalidPhone)

	empty := NewPhoneNormalizer(nil)
	_, err = empty.Normalize("+99365123456")
	require.ErrorIs(t, err, ErrInvalidPhone, "no prefixes means nothing is accepted")
}

func TestMaskPhone(t *testing.T) {
	t.Parallel()

	require.Equal(t, "+99365***456", MaskPhone("+99365123456"))
	require.Equal(t, "***", MaskPhone("12345"))
	require.NotContains(t, MaskPhone("+99365123456"), "123456")
}
