package service

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shalarasgar/carpool-backend/internal/i18n"
)

func parseUUID(s string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(strings.TrimSpace(s)); err != nil || !id.Valid {
		return pgtype.UUID{}, ErrNotFound
	}
	return id, nil
}

func parseUserID(s string) (pgtype.UUID, error) {
	id, err := parseUUID(s)
	if err != nil {
		return pgtype.UUID{}, ErrUnauthorized
	}
	return id, nil
}

func canonicalUUID(s string) (string, bool) {
	id, err := parseUUID(s)
	if err != nil {
		return "", false
	}
	return id.String(), true
}

// requireOwner reports whether callerID owns the resource. A foreign resource
// is ErrNotFound, the same result as a missing id, so callers cannot tell
// another user's ids apart from unknown ones.
func requireOwner(ownerID, callerID string) error {
	if ownerID == "" || callerID == "" || ownerID != callerID {
		return ErrNotFound
	}
	return nil
}

func vehicleLimit(count, max int) error {
	if max < 1 || count >= max {
		return ErrVehicleLimit
	}
	return nil
}

// VehicleFields is a vehicle after plate, brand, model and color normalization.
type VehicleFields struct {
	Brand string
	Model string
	Color string
	Plate string
}

// NormalizeVehicle trims and checks brand, model, color and plate.
// Plate is uppercased. It must contain at least one letter and one digit,
// and 4 to 10 of those characters. Spaces and hyphens are separators only.
func NormalizeVehicle(brand, model, color, plate string) (VehicleFields, error) {
	details := map[string]string{}
	b, berr := normalizeLabel(brand, 40, true, true)
	m, merr := normalizeLabel(model, 40, true, false)
	c, cerr := normalizeLabel(color, 24, false, true)
	p, perr := normalizePlate(plate)
	if berr != nil {
		details["brand"] = "invalid"
	}
	if merr != nil {
		details["model"] = "invalid"
	}
	if cerr != nil {
		details["color"] = "invalid"
	}
	if perr != nil {
		details["plate"] = "invalid"
	}
	if len(details) > 0 {
		return VehicleFields{}, validationError(details)
	}
	return VehicleFields{Brand: b, Model: m, Color: c, Plate: p}, nil
}

// NormalizePersonName trims a display name and requires 1..maxRunes letters
// (spaces, hyphens, apostrophes and dots allowed).
func NormalizePersonName(name string, maxRunes int) (string, error) {
	s, err := normalizeLabel(name, maxRunes, false, true)
	if err != nil {
		return "", validationError(map[string]string{"name": "invalid"})
	}
	return s, nil
}

// NormalizeLocale accepts tk, ru or en.
func NormalizeLocale(locale string) (string, error) {
	if !i18n.Supported(locale) {
		return "", validationError(map[string]string{"locale": "oneof"})
	}
	return locale, nil
}

func normalizeLabel(raw string, maxRunes int, allowDigits, requireLetter bool) (string, error) {
	s := strings.Join(strings.Fields(raw), " ")
	n := utf8.RuneCountInString(s)
	if n < 1 || n > maxRunes {
		return "", errInvalid
	}
	letters, digits := 0, 0
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsMark(r):
			letters++
		case unicode.IsDigit(r):
			if !allowDigits {
				return "", errInvalid
			}
			digits++
		case r == ' ' || r == '-' || r == '\'' || r == '.' || r == '’':
		default:
			return "", errInvalid
		}
	}
	if requireLetter && letters < 1 {
		return "", errInvalid
	}
	if !requireLetter && letters+digits < 1 {
		return "", errInvalid
	}
	return s, nil
}

func normalizePlate(raw string) (string, error) {
	s := strings.ToUpper(strings.Join(strings.Fields(raw), " "))
	if s == "" {
		return "", errInvalid
	}
	letters, digits := 0, 0
	prevSep := true
	for _, r := range s {
		switch {
		case unicode.IsLetter(r):
			letters++
			prevSep = false
		case unicode.IsDigit(r):
			digits++
			prevSep = false
		case r == ' ' || r == '-':
			if prevSep {
				return "", errInvalid
			}
			prevSep = true
		default:
			return "", errInvalid
		}
	}
	if prevSep || letters < 1 || digits < 1 || letters+digits < 4 || letters+digits > 10 {
		return "", errInvalid
	}
	return s, nil
}

var errInvalid = &Error{Code: "VALIDATION_ERROR"}
