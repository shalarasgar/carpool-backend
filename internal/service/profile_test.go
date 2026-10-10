package service

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeVehicle(t *testing.T) {
	t.Parallel()

	ok, err := NormalizeVehicle("  Toyota ", "Camry", "ak", " ag  1234 ab ")
	require.NoError(t, err)
	require.Equal(t, VehicleFields{Brand: "Toyota", Model: "Camry", Color: "ak", Plate: "AG 1234 AB"}, ok)

	digits, err := NormalizeVehicle("Lada", "2107", "gara", "AB-1234")
	require.NoError(t, err)
	require.Equal(t, "2107", digits.Model)
	require.Equal(t, "AB-1234", digits.Plate)

	cyr, err := NormalizeVehicle("Газель", "Next", "белый", "аг 1234")
	require.NoError(t, err)
	require.Equal(t, "Газель", cyr.Brand)
	require.Equal(t, "АГ 1234", cyr.Plate)

	cases := []struct {
		name                       string
		brand, model, color, plate string
		field                      string
	}{
		{"empty brand", "", "Camry", "ak", "AG1234", "brand"},
		{"brand symbols", "Toyota!", "Camry", "ak", "AG1234", "brand"},
		{"digits only brand", "1234", "Camry", "ak", "AG1234", "brand"},
		{"empty model", "Toyota", "   ", "ak", "AG1234", "model"},
		{"color digits", "Toyota", "Camry", "red1", "AG1234", "color"},
		{"plate no digit", "Toyota", "Camry", "ak", "ABCD", "plate"},
		{"plate no letter", "Toyota", "Camry", "ak", "1234", "plate"},
		{"plate too short", "Toyota", "Camry", "ak", "A1", "plate"},
		{"plate too long", "Toyota", "Camry", "ak", "AB123456789", "plate"},
		{"plate punctuation", "Toyota", "Camry", "ak", "AG.1234", "plate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizeVehicle(tc.brand, tc.model, tc.color, tc.plate)
			require.ErrorIs(t, err, ErrValidation)
			require.Contains(t, err.(*Error).Details, tc.field)
		})
	}
}

func TestRequireOwner(t *testing.T) {
	t.Parallel()
	require.NoError(t, requireOwner("user-a", "user-a"))
	require.ErrorIs(t, requireOwner("user-a", "user-b"), ErrNotFound)
	require.ErrorIs(t, requireOwner("", "user-b"), ErrNotFound)
	require.ErrorIs(t, requireOwner("user-a", ""), ErrNotFound)
}

func TestVehicleLimit(t *testing.T) {
	t.Parallel()
	require.NoError(t, vehicleLimit(0, 3))
	require.NoError(t, vehicleLimit(2, 3))
	require.ErrorIs(t, vehicleLimit(3, 3), ErrVehicleLimit)
	require.ErrorIs(t, vehicleLimit(4, 3), ErrVehicleLimit)
}

func TestNormalizePersonNameAndLocale(t *testing.T) {
	t.Parallel()
	name, err := NormalizePersonName("  Aýna  Amanowa ", 80)
	require.NoError(t, err)
	require.Equal(t, "Aýna Amanowa", name)

	_, err = NormalizePersonName("123", 80)
	require.ErrorIs(t, err, ErrValidation)

	_, err = NormalizePersonName(strings.Repeat("a", 81), 80)
	require.ErrorIs(t, err, ErrValidation)

	locale, err := NormalizeLocale("ru")
	require.NoError(t, err)
	require.Equal(t, "ru", locale)
	_, err = NormalizeLocale("RU")
	require.ErrorIs(t, err, ErrValidation)
}

func TestAnonymizedPhone(t *testing.T) {
	t.Parallel()
	const (
		secret = "integration-otp-secret-value"
		phone  = "+99365123456"
		id     = "11111111-1111-4111-8111-111111111111"
	)
	got := AnonymizedPhone(secret, phone, id)
	require.NotContains(t, got, phone)
	require.Regexp(t, `^anon:[0-9a-f]{64}:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`, got)
	require.Equal(t, got, AnonymizedPhone(secret, phone, id))
	require.NotEqual(t, got, AnonymizedPhone(secret, phone, "22222222-2222-4222-8222-222222222222"))
	require.NotEqual(t, got, AnonymizedPhone("other-secret-value-xxxx", phone, id))
}

func TestNormalizePhoto(t *testing.T) {
	t.Parallel()
	allowed := map[string]struct{}{
		"image/jpeg": {},
		"image/png":  {},
		"image/webp": {},
	}

	jpg := mustJPEG(t, 8, 8)
	marked := withJPEGComment(jpg, "EXIFSECRET-do-not-keep")
	require.Contains(t, string(marked), "EXIFSECRET-do-not-keep")

	out, ct, ext, err := NormalizePhoto(marked, allowed, 1<<20)
	require.NoError(t, err)
	require.Equal(t, "image/jpeg", ct)
	require.Equal(t, "jpg", ext)
	require.NotContains(t, string(out), "EXIFSECRET-do-not-keep")
	_, err = jpeg.Decode(bytes.NewReader(out))
	require.NoError(t, err)

	pngBytes := mustPNG(t, true)
	out, ct, ext, err = NormalizePhoto(pngBytes, allowed, 1<<20)
	require.NoError(t, err)
	require.Equal(t, "image/png", ct)
	require.Equal(t, "png", ext)

	_, _, _, err = NormalizePhoto([]byte("this is plain text, not an image"), allowed, 1<<20)
	require.ErrorIs(t, err, ErrPhotoType)

	_, _, _, err = NormalizePhoto([]byte{0xff, 0xd8, 0xff, 0x00, 0x11, 0x22}, allowed, 1<<20)
	require.ErrorIs(t, err, ErrPhotoType)

	_, _, _, err = NormalizePhoto(jpg, allowed, int64(len(jpg)-1))
	require.ErrorIs(t, err, ErrPhotoTooLarge)

	jpegOnly := map[string]struct{}{"image/jpeg": {}}
	_, _, _, err = NormalizePhoto(pngBytes, jpegOnly, 1<<20)
	require.ErrorIs(t, err, ErrPhotoType)

	webpBytes := must1x1WebP(t)
	out, ct, ext, err = NormalizePhoto(webpBytes, allowed, 1<<20)
	require.NoError(t, err)
	require.Equal(t, "image/jpeg", ct)
	require.Equal(t, "jpg", ext)
	require.NotEqual(t, webpBytes, out)
}

func TestParseActiveOnlyAndLocationFilter(t *testing.T) {
	t.Parallel()

	active, err := ParseActiveOnly("", false)
	require.NoError(t, err)
	require.True(t, active)
	active, err = ParseActiveOnly("false", true)
	require.NoError(t, err)
	require.False(t, active)
	_, err = ParseActiveOnly("maybe", true)
	require.ErrorIs(t, err, ErrValidation)

	_, err = (LocationQuery{Type: "village"}).normalized()
	require.ErrorIs(t, err, ErrValidation)
	_, err = (LocationQuery{ParentID: "not-a-uuid"}).normalized()
	require.ErrorIs(t, err, ErrValidation)

	rows := []locationRecord{
		{ID: "w-mary", Type: "welayat", NameTK: "Mary", NameRU: "Мары", NameEN: "Mary", IsActive: true, SortOrder: 5},
		{ID: "c-ash", Type: "city", NameTK: "Aşgabat", NameRU: "Ашхабад", NameEN: "Ashgabat", IsActive: true, SortOrder: 10},
		{ID: "c-dash", Type: "city", ParentID: "w-dash", HasParent: true, NameTK: "Daşoguz", NameRU: "Дашогуз", NameEN: "Dashoguz", IsActive: true, SortOrder: 15},
		{ID: "e-test", Type: "etrap", ParentID: "w-ahal", HasParent: true, NameTK: "TestEtrap", NameRU: "ТестЭтрап", NameEN: "TestEtrap", IsActive: false, SortOrder: 99},
	}

	all := filterLocations(rows, LocationQuery{ActiveOnly: true, Locale: "en"})
	require.Len(t, all, 3)

	withInactive := filterLocations(rows, LocationQuery{ActiveOnly: false, Locale: "tk"})
	require.Len(t, withInactive, 4)
	require.Equal(t, "TestEtrap", withInactive[3].Name)

	mary := filterLocations(rows, LocationQuery{ActiveOnly: true, Q: "МАРЫ", Locale: "ru"})
	require.Len(t, mary, 1)
	require.Equal(t, "Мары", mary[0].Name)

	dash := filterLocations(rows, LocationQuery{ActiveOnly: true, Q: "daş", Locale: "tk"})
	require.Len(t, dash, 1)
	require.Equal(t, "Daşoguz", dash[0].Name)
	require.Equal(t, "w-dash", *dash[0].ParentID)

	en := filterLocations(rows, LocationQuery{ActiveOnly: true, Q: "ashg", Locale: "en"})
	require.Len(t, en, 1)
	require.Equal(t, "Ashgabat", en[0].Name)

	cities := filterLocations(rows, LocationQuery{ActiveOnly: true, Type: "city", Locale: "en"})
	require.Len(t, cities, 2)

	children := filterLocations(rows, LocationQuery{ActiveOnly: false, ParentID: "w-ahal", Locale: "en"})
	require.Len(t, children, 1)
	require.Equal(t, "e-test", children[0].ID)
}

func mustJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 200, A: 255})
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}))
	return buf.Bytes()
}

func mustPNG(t *testing.T, transparent bool) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: 10, G: 20, B: 30, A: 255})
	if transparent {
		img.SetNRGBA(1, 1, color.NRGBA{R: 10, G: 20, B: 30, A: 0})
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func withJPEGComment(jpg []byte, comment string) []byte {
	payload := []byte(comment)
	seg := []byte{0xff, 0xfe, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}
	seg = append(seg, payload...)
	out := append([]byte{}, jpg[:2]...)
	out = append(out, seg...)
	out = append(out, jpg[2:]...)
	return out
}

func must1x1WebP(t *testing.T) []byte {
	t.Helper()
	// 1x1 lossy WebP. http.DetectContentType recognizes the RIFF/WEBP header
	// and golang.org/x/image/webp can decode the frame.
	raw := []byte{
		0x52, 0x49, 0x46, 0x46, 0x24, 0x00, 0x00, 0x00, 0x57, 0x45, 0x42, 0x50,
		0x56, 0x50, 0x38, 0x20, 0x18, 0x00, 0x00, 0x00, 0x30, 0x01, 0x00, 0x9d,
		0x01, 0x2a, 0x01, 0x00, 0x01, 0x00, 0x02, 0x00, 0x34, 0x25, 0xa4, 0x00,
		0x03, 0x70, 0x00, 0xfe, 0xfb, 0xfd, 0x50, 0x00,
	}
	return raw
}
