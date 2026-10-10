package i18n

import (
	"context"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"":                               "tk",
		"*":                              "tk",
		"fr-FR":                          "tk",
		"ru":                             "ru",
		"ru-RU,ru;q=0.9,en;q=0.8":        "ru",
		"en-US,en;q=0.9":                 "en",
		"tk-TM":                          "tk",
		"fr;q=1, en;q=0.5, ru;q=0.8":     "ru",
		"EN":                             "en",
		"de, ru;q=0, en;q=0.1":           "en",
		"zh-CN,zh;q=0.9,tk;q=0.2,ru;q=1": "ru",
	}
	for header, want := range cases {
		if got := Resolve(header); got != want {
			t.Errorf("Resolve(%q) = %q, want %q", header, got, want)
		}
	}
}

func TestMessageFallbacks(t *testing.T) {
	t.Parallel()

	if got := Message("ru", "INVALID_PHONE"); got != "Неверный номер телефона." {
		t.Fatalf("ru message = %q", got)
	}
	if got := Message("xx", "INVALID_PHONE"); got != Message("tk", "INVALID_PHONE") {
		t.Fatalf("unknown locale should fall back to tk, got %q", got)
	}
	if got := Message("tk", "NO_SUCH_CODE"); got != "NO_SUCH_CODE" {
		t.Fatalf("unknown code = %q", got)
	}
	for code, m := range messages {
		for _, locale := range []string{LocaleTK, LocaleRU, LocaleEN} {
			if m[locale] == "" {
				t.Errorf("code %s has no %s translation", code, locale)
			}
		}
	}
}

func TestOTPSMSIsASCII(t *testing.T) {
	t.Parallel()

	for _, locale := range []string{LocaleTK, LocaleRU, LocaleEN} {
		text := OTPSMS(locale, "Carpool", "123456")
		if !strings.Contains(text, "123456") || !strings.HasPrefix(text, "Carpool: ") {
			t.Fatalf("%s sms = %q", locale, text)
		}
		for _, r := range text {
			if r > 127 {
				t.Fatalf("%s sms contains non-ASCII rune %q: %q", locale, r, text)
			}
		}
		if len(text) > 160 {
			t.Fatalf("%s sms longer than one segment: %d", locale, len(text))
		}
	}
}

func TestContextLocale(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	if got := FromContext(ctx); got != DefaultLocale {
		t.Fatalf("empty context locale = %q", got)
	}
	if got := FromContext(WithLocale(ctx, "en")); got != "en" {
		t.Fatalf("locale = %q", got)
	}
	if got := FromContext(WithLocale(ctx, "xx")); got != DefaultLocale {
		t.Fatalf("unsupported locale = %q", got)
	}
}
