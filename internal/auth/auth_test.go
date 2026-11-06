package auth

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizePhone(t *testing.T) {
	t.Parallel()
	prefixes := []string{"6", "7"}
	cases := map[string]string{
		"+993 61 123456": "+99361123456",
		"99371123456":    "+99371123456",
		"61234567":       "+99361234567",
		"0099361234567":  "+99361234567",
	}
	for in, want := range cases {
		got, err := NormalizePhone(in, prefixes)
		if err != nil || got != want {
			t.Fatalf("NormalizePhone(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"+905551112233", "+99351123456", "123", "+9936112345", ""} {
		if _, err := NormalizePhone(bad, prefixes); err != ErrInvalidPhone {
			t.Fatalf("NormalizePhone(%q) err = %v", bad, err)
		}
	}
}

func TestOTPHashDoesNotStoreCodeAndRejectsMismatch(t *testing.T) {
	t.Parallel()
	code, err := RandomCode()
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 6 {
		t.Fatalf("code length %d", len(code))
	}
	hash := HashOTP("secret-value-at-least", code)
	if strings.Contains(hash, code) {
		t.Fatal("hash contains the code")
	}
	if !OTPEqual("secret-value-at-least", code, hash) {
		t.Fatal("equal code rejected")
	}
	if OTPEqual("secret-value-at-least", "000000", hash) && code != "000000" {
		t.Fatal("wrong code accepted")
	}
}

func TestAccessTokenRejectsAlgorithmSwap(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)
	token, err := SignAccess("jwt-secret-value-xx", "user-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := ParseAccess("jwt-secret-value-xx", token, now.Add(time.Second))
	if err != nil || sub != "user-1" {
		t.Fatalf("parse = %q %v", sub, err)
	}
	if _, err := ParseAccess("jwt-secret-value-xx", token, now.Add(2*time.Minute)); err != ErrUnauthorized {
		t.Fatalf("expired err = %v", err)
	}
	parts := strings.Split(token, ".")
	parts[2] = strings.Repeat("A", len(parts[2]))
	if _, err := ParseAccess("jwt-secret-value-xx", strings.Join(parts, "."), now); err != ErrUnauthorized {
		t.Fatalf("bad sig err = %v", err)
	}
}

func TestLocaleAndDeviceID(t *testing.T) {
	t.Parallel()
	if Locale("ru-RU,ru;q=0.9,en;q=0.8") != "ru" {
		t.Fatal("locale")
	}
	if Locale("") != "tk" {
		t.Fatal("default locale")
	}
	if !ValidDeviceID("phone-1") || ValidDeviceID("bad id") || ValidDeviceID("") {
		t.Fatal("device id")
	}
}
