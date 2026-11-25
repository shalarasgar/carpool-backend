package staff

import (
	"errors"
	"strings"
	"testing"
)

func TestLoginRules(t *testing.T) {
	t.Parallel()
	if got := NormalizeLogin("  Muhammet.B "); got != "muhammet.b" {
		t.Fatalf("normalized = %q", got)
	}
	for _, ok := range []string{"abc", "muhammet.b", "a-1_2", strings.Repeat("a", 32)} {
		if err := ValidateLogin(ok); err != nil {
			t.Fatalf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "ab", ".abc", "Abc", "a b c", "ыыы", strings.Repeat("a", 33)} {
		if err := ValidateLogin(bad); !errors.Is(err, ErrValidation) {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestPasswordAndNameRules(t *testing.T) {
	t.Parallel()
	var field *FieldError
	if err := ValidatePassword("short pass"); err != nil {
		t.Fatalf("ten characters: %v", err)
	}
	if err := ValidatePassword("ninechars"); !errors.As(err, &field) || field.Reason != "too_short" {
		t.Fatalf("nine characters: %v", err)
	}
	// bcrypt reads 72 bytes: longer passwords would be silently cut.
	if err := ValidatePassword(strings.Repeat("я", 37)); !errors.As(err, &field) || field.Reason != "too_long" {
		t.Fatalf("74 bytes: %v", err)
	}
	if err := ValidateName(""); !errors.Is(err, ErrValidation) {
		t.Fatal("empty name accepted")
	}
	if err := ValidateName(strings.Repeat("ý", MaxName)); err != nil {
		t.Fatalf("name of the limit in runes: %v", err)
	}
	if !RoleAdmin.Valid() || !RoleModerator.Valid() || Role("owner").Valid() {
		t.Fatal("roles")
	}
	if !TokenActor.IsAdmin() || TokenActor.ID != "" {
		t.Fatalf("token actor = %+v", TokenActor)
	}
}
