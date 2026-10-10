package integration

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func (e *env) signIn(phone, device string) (session, bool) {
	e.t.Helper()
	e.advance(61 * time.Second)
	req := e.call(http.MethodPost, "/v1/auth/request-otp", map[string]any{"phone": phone, "device_id": device}, nil)
	require.Equal(e.t, http.StatusOK, req.Status, string(req.Body))
	code := req.str("dev_code")
	require.Len(e.t, code, 6)
	res := e.call(http.MethodPost, "/v1/auth/verify-otp", map[string]any{
		"phone": phone, "code": code, "device_id": device,
	}, nil)
	require.Equal(e.t, http.StatusOK, res.Status, string(res.Body))
	user := res.decoded["user"].(map[string]any)
	return session{
		access:  res.str("access_token"),
		refresh: res.str("refresh_token"),
		userID:  user["id"].(string),
	}, res.field("is_new_user") == true
}

func TestProfileUpdateAnonymizeAndReregister(t *testing.T) {
	e := newEnv(t)
	const phone = "+99365111111"
	first, isNew := e.signIn(phone, "device-a")
	require.True(t, isNew)

	unauth := e.call(http.MethodGet, "/v1/me", nil, nil)
	require.Equal(t, http.StatusUnauthorized, unauth.Status)

	patched := e.call(http.MethodPatch, "/v1/me", map[string]any{"name": "Aýna Amanowa", "locale": "ru"}, bearer(first.access))
	require.Equal(t, http.StatusOK, patched.Status, string(patched.Body))
	me := patched.decoded
	require.Equal(t, "Aýna Amanowa", me["name"])
	require.Equal(t, "ru", me["locale"])
	require.Equal(t, phone, me["phone"])

	got := e.call(http.MethodGet, "/v1/me", nil, bearer(first.access))
	require.Equal(t, http.StatusOK, got.Status, string(got.Body))
	require.Equal(t, "Aýna Amanowa", got.decoded["name"])

	created := e.call(http.MethodPost, "/v1/me/vehicles", map[string]any{
		"brand": "Toyota", "model": "Camry", "color": "ak", "plate": "ag 1234 ab",
	}, bearer(first.access))
	require.Equal(t, http.StatusCreated, created.Status, string(created.Body))

	e.exec(`
		INSERT INTO trips (driver_id, from_location_id, to_location_id, departs_at, seats_total, seats_left, status)
		VALUES ($1, 'c2000000-0000-4000-8000-000000000001', 'c2000000-0000-4000-8000-000000000008', now() - interval '1 day', 2, 2, 'completed')
	`, first.userID)
	e.exec(`
		INSERT INTO trips (driver_id, from_location_id, to_location_id, departs_at, seats_total, seats_left, status)
		VALUES ($1, 'c2000000-0000-4000-8000-000000000001', 'c2000000-0000-4000-8000-000000000008', now() + interval '1 day', 2, 2, 'active')
	`, first.userID)
	e.exec(`UPDATE users SET rating_count = 4, rating_avg = 4.50 WHERE id = $1`, first.userID)

	pub := e.call(http.MethodGet, "/v1/users/"+first.userID, nil, bearer(first.access))
	require.Equal(t, http.StatusOK, pub.Status, string(pub.Body))
	require.NotContains(t, string(pub.Body), phone)
	_, hasPhone := pub.decoded["phone"]
	require.False(t, hasPhone)
	require.Equal(t, "Aýna Amanowa", pub.decoded["name"])
	require.Equal(t, float64(4), pub.decoded["rating_count"])
	require.Equal(t, float64(1), pub.decoded["completed_trips"])
	vehicle := pub.decoded["vehicle"].(map[string]any)
	require.Equal(t, "AG 1234 AB", vehicle["plate"])
	require.Equal(t, "Toyota", vehicle["brand"])

	photo := e.uploadPhoto(first.access, "face.jpg", "image/jpeg", tinyJPEG(t))
	require.Equal(t, http.StatusOK, photo.Status, string(photo.Body))
	photoURL, _ := photo.decoded["photo_url"].(string)
	require.Contains(t, photoURL, "/v1/media/photos/")
	stored := e.call(http.MethodGet, photoURL, nil, nil)
	require.Equal(t, http.StatusOK, stored.Status, string(stored.Body))

	deleted := e.call(http.MethodDelete, "/v1/me", nil, bearer(first.access))
	require.Equal(t, http.StatusNoContent, deleted.Status, string(deleted.Body))

	again := e.call(http.MethodGet, "/v1/me", nil, bearer(first.access))
	require.Equal(t, http.StatusUnauthorized, again.Status)
	require.Equal(t, "UNAUTHORIZED", again.Err.Error.Code)
	refresh := e.call(http.MethodPost, "/v1/auth/refresh", map[string]any{"refresh_token": first.refresh}, nil)
	require.Equal(t, http.StatusUnauthorized, refresh.Status)
	gone := e.call(http.MethodGet, photoURL, nil, nil)
	require.Equal(t, http.StatusNotFound, gone.Status)
	viewer, _ := e.signIn("+99365777777", "other-device")
	hidden := e.call(http.MethodGet, "/v1/users/"+first.userID, nil, bearer(viewer.access))
	require.Equal(t, http.StatusNotFound, hidden.Status)

	require.Equal(t, 0, e.queryInt(`SELECT count(*) FROM refresh_tokens WHERE user_id = $1 AND revoked_at IS NULL`, first.userID))
	require.Equal(t, "", e.queryString(`SELECT name FROM users WHERE id = $1`, first.userID))
	tombstone := e.queryString(`SELECT phone FROM users WHERE id = $1`, first.userID)
	require.NotEqual(t, phone, tombstone)
	require.Regexp(t, `^anon:[0-9a-f]{64}:`, tombstone)
	require.Equal(t, 4, e.queryInt(`SELECT rating_count FROM users WHERE id = $1`, first.userID))

	second, isNewAgain := e.signIn(phone, "device-b")
	require.True(t, isNewAgain)
	require.NotEqual(t, first.userID, second.userID)
	me2 := e.call(http.MethodGet, "/v1/me", nil, bearer(second.access))
	require.Equal(t, http.StatusOK, me2.Status, string(me2.Body))
	require.Equal(t, "", me2.decoded["name"])
	require.Equal(t, phone, me2.decoded["phone"])
	require.Equal(t, float64(0), me2.decoded["rating_count"])
	require.Equal(t, 1, e.queryInt(`SELECT count(*) FROM users WHERE phone = $1 AND deleted_at IS NULL`, phone))
	require.Equal(t, 1, e.queryInt(`SELECT count(*) FROM users WHERE id = $1 AND deleted_at IS NOT NULL`, first.userID))
}

func TestPhotoUploadValidation(t *testing.T) {
	e := newEnv(t)
	s, _ := e.signIn("+99365222222", "photo-device")

	pngBytes := tinyPNG(t)
	ok := e.uploadPhoto(s.access, "x.jpg", "image/jpeg", pngBytes)
	require.Equal(t, http.StatusOK, ok.Status, string(ok.Body))
	photoURL := ok.decoded["photo_url"].(string)
	require.Contains(t, photoURL, ".png", "sniffed type wins over the multipart header")
	got := e.call(http.MethodGet, photoURL, nil, nil)
	require.Equal(t, http.StatusOK, got.Status)
	require.Equal(t, "image/png", got.Header.Get("Content-Type"))
	require.Equal(t, "nosniff", got.Header.Get("X-Content-Type-Options"))
	require.True(t, bytes.HasPrefix(got.Body, []byte{0x89, 'P', 'N', 'G'}))

	fake := e.uploadPhoto(s.access, "fake.jpg", "image/jpeg", []byte("this is plain text pretending to be a jpeg"))
	require.Equal(t, http.StatusUnprocessableEntity, fake.Status, string(fake.Body))
	require.Equal(t, "PHOTO_TYPE_UNSUPPORTED", fake.Err.Error.Code)

	over := e.uploadPhoto(s.access, "big.jpg", "image/jpeg", bytes.Repeat([]byte{0xff, 0xd8, 0xff, 0x00}, 5<<10))
	require.Equal(t, http.StatusRequestEntityTooLarge, over.Status, string(over.Body))
	require.Equal(t, "PHOTO_TOO_LARGE", over.Err.Error.Code)
}

func TestVehicleCRUDAndCrossUserDenial(t *testing.T) {
	e := newEnv(t)
	owner, _ := e.signIn("+99365333333", "owner-device")
	other, _ := e.signIn("+99365444444", "other-device")

	created := e.call(http.MethodPost, "/v1/me/vehicles", map[string]any{
		"brand": "Lada", "model": "2107", "color": "белый", "plate": "ag-12",
	}, bearer(owner.access))
	require.Equal(t, http.StatusCreated, created.Status, string(created.Body))
	require.Equal(t, "AG-12", created.str("plate"))
	require.Equal(t, "2107", created.str("model"))
	id := created.str("id")

	second := e.call(http.MethodPost, "/v1/me/vehicles", map[string]any{
		"brand": "Toyota", "model": "Camry", "color": "gara", "plate": "MR 5678",
	}, bearer(owner.access))
	require.Equal(t, http.StatusCreated, second.Status, string(second.Body))

	limited := e.call(http.MethodPost, "/v1/me/vehicles", map[string]any{
		"brand": "Hyundai", "model": "Accent", "color": "ak", "plate": "LB 1111",
	}, bearer(owner.access))
	require.Equal(t, http.StatusUnprocessableEntity, limited.Status, string(limited.Body))
	require.Equal(t, "VEHICLE_LIMIT", limited.Err.Error.Code)

	list := e.call(http.MethodGet, "/v1/me/vehicles", nil, bearer(owner.access))
	require.Equal(t, http.StatusOK, list.Status, string(list.Body))
	require.Len(t, list.decoded["vehicles"].([]any), 2)

	otherList := e.call(http.MethodGet, "/v1/me/vehicles", nil, bearer(other.access))
	require.Len(t, otherList.decoded["vehicles"].([]any), 0)

	crossPatch := e.call(http.MethodPatch, "/v1/me/vehicles/"+id, map[string]any{"color": "gok"}, bearer(other.access))
	require.Equal(t, http.StatusNotFound, crossPatch.Status, string(crossPatch.Body))
	require.Equal(t, "NOT_FOUND", crossPatch.Err.Error.Code)
	crossDelete := e.call(http.MethodDelete, "/v1/me/vehicles/"+id, nil, bearer(other.access))
	require.Equal(t, http.StatusNotFound, crossDelete.Status, string(crossDelete.Body))
	missing := e.call(http.MethodPatch, "/v1/me/vehicles/not-a-uuid", map[string]any{"color": "gok"}, bearer(owner.access))
	require.Equal(t, http.StatusNotFound, missing.Status)

	patched := e.call(http.MethodPatch, "/v1/me/vehicles/"+id, map[string]any{"color": "gök"}, bearer(owner.access))
	require.Equal(t, http.StatusOK, patched.Status, string(patched.Body))
	require.Equal(t, "gök", patched.str("color"))
	require.Equal(t, "Lada", patched.str("brand"))

	deleted := e.call(http.MethodDelete, "/v1/me/vehicles/"+id, nil, bearer(owner.access))
	require.Equal(t, http.StatusNoContent, deleted.Status)
	gone := e.call(http.MethodDelete, "/v1/me/vehicles/"+id, nil, bearer(owner.access))
	require.Equal(t, http.StatusNotFound, gone.Status)
	require.Equal(t, 1, e.queryInt(`SELECT count(*) FROM vehicles WHERE user_id = $1`, owner.userID))
}

func TestBlockedUserRejectedOnAuthenticatedRoutes(t *testing.T) {
	e := newEnv(t)
	s, _ := e.signIn("+99365555555", "blocked-device")
	e.exec(`UPDATE users SET is_blocked = true WHERE id = $1`, s.userID)

	for _, call := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/v1/me", nil},
		{http.MethodPatch, "/v1/me", map[string]any{"name": "Nope"}},
		{http.MethodDelete, "/v1/me", nil},
		{http.MethodGet, "/v1/users/" + s.userID, nil},
		{http.MethodGet, "/v1/me/vehicles", nil},
		{http.MethodPost, "/v1/me/vehicles", map[string]any{"brand": "Lada", "model": "2107", "color": "ak", "plate": "AG1234"}},
		{http.MethodPost, "/v1/auth/logout", nil},
	} {
		res := e.call(call.method, call.path, call.body, bearer(s.access))
		require.Equal(t, http.StatusForbidden, res.Status, "%s %s: %s", call.method, call.path, res.Body)
		require.Equal(t, "USER_BLOCKED", res.Err.Error.Code)
	}

	open := e.call(http.MethodGet, "/v1/locations", nil, nil)
	require.Equal(t, http.StatusOK, open.Status, string(open.Body))

	photo := e.uploadPhoto(s.access, "a.jpg", "image/jpeg", tinyJPEG(t))
	require.Equal(t, http.StatusForbidden, photo.Status, string(photo.Body))
	require.Equal(t, "USER_BLOCKED", photo.Err.Error.Code)
}

func TestLocationsFilterLocaleETagAndGzip(t *testing.T) {
	e := newEnv(t)
	e.exec(`
		INSERT INTO locations (parent_id, type, name_tk, name_ru, name_en, is_active, sort_order)
		VALUES ('a1000000-0000-4000-8000-000000000001', 'etrap', 'TestEtrap', 'ТестЭтрап', 'TestEtrap', false, 99)
	`)

	all := e.locations("/v1/locations", "en")
	require.NotEmpty(t, all.Locations)
	for _, loc := range all.Locations {
		require.True(t, loc.IsActive)
		require.NotEqual(t, "TestEtrap", loc.Name)
	}

	inactive := e.locations("/v1/locations?active_only=false&q=testetrap", "en")
	require.Len(t, inactive.Locations, 1)
	require.Equal(t, "TestEtrap", inactive.Locations[0].Name)
	require.False(t, inactive.Locations[0].IsActive)

	hidden := e.locations("/v1/locations?q=testetrap", "en")
	require.Empty(t, hidden.Locations)

	ruHit := e.locations("/v1/locations?q="+urlQuery("ТестЭтрап")+"&active_only=false", "ru")
	require.Len(t, ruHit.Locations, 1)
	require.Equal(t, "ТестЭтрап", ruHit.Locations[0].Name)

	welayats := e.locations("/v1/locations?type=welayat", "tk")
	require.Len(t, welayats.Locations, 5)

	children := e.locations("/v1/locations?parent_id=a1000000-0000-4000-8000-000000000001", "en")
	require.NotEmpty(t, children.Locations)
	for _, loc := range children.Locations {
		require.NotNil(t, loc.ParentID)
		require.Equal(t, "a1000000-0000-4000-8000-000000000001", *loc.ParentID)
	}

	mary := e.locations("/v1/locations?q=MARY", "en")
	require.NotEmpty(t, mary.Locations)
	maryLower := e.locations("/v1/locations?q=mary", "ru")
	require.Equal(t, len(mary.Locations), len(maryLower.Locations))
	var sawRU bool
	for _, loc := range maryLower.Locations {
		if loc.ID == "a1000000-0000-4000-8000-000000000005" {
			require.Equal(t, "Мары", loc.Name)
			sawRU = true
		}
	}
	require.True(t, sawRU)

	dash := e.locations("/v1/locations?q="+urlQuery("daş"), "tk")
	require.NotEmpty(t, dash.Locations)
	var sawDash bool
	for _, loc := range dash.Locations {
		if loc.Name == "Daşoguz" {
			sawDash = true
		}
	}
	require.True(t, sawDash)

	const ashgabat = "c2000000-0000-4000-8000-000000000001"
	require.Equal(t, "Ashgabat", locationName(e.locations("/v1/locations?type=city", "en"), ashgabat))
	require.Equal(t, "Ашхабад", locationName(e.locations("/v1/locations?type=city", "ru"), ashgabat))
	require.Equal(t, "Aşgabat", locationName(e.locations("/v1/locations?type=city", "tk"), ashgabat))

	badType := e.call(http.MethodGet, "/v1/locations?type=village", nil, nil)
	require.Equal(t, http.StatusUnprocessableEntity, badType.Status)
	require.Equal(t, "VALIDATION_ERROR", badType.Err.Error.Code)
	badParent := e.call(http.MethodGet, "/v1/locations?parent_id=nope", nil, nil)
	require.Equal(t, http.StatusUnprocessableEntity, badParent.Status)

	first := e.call(http.MethodGet, "/v1/locations?type=welayat", nil, map[string]string{"Accept-Language": "en"})
	require.Equal(t, http.StatusOK, first.Status, string(first.Body))
	etag := first.Header.Get("ETag")
	require.NotEmpty(t, etag)
	require.Equal(t, "public, max-age=300", first.Header.Get("Cache-Control"))

	notModified := e.call(http.MethodGet, "/v1/locations?type=welayat", nil, map[string]string{
		"Accept-Language": "en",
		"If-None-Match":   etag,
	})
	require.Equal(t, http.StatusNotModified, notModified.Status)
	require.Empty(t, notModified.Body)
	require.Equal(t, etag, notModified.Header.Get("ETag"))

	otherLang := e.call(http.MethodGet, "/v1/locations?type=welayat", nil, map[string]string{"Accept-Language": "ru"})
	require.NotEqual(t, etag, otherLang.Header.Get("ETag"))

	gzReq, err := http.NewRequest(http.MethodGet, e.srv.URL+"/v1/locations?type=city", nil)
	require.NoError(t, err)
	gzReq.Header.Set("Accept-Encoding", "gzip")
	gzReq.Header.Set("Accept-Language", "en")
	gzRes, err := e.srv.Client().Do(gzReq)
	require.NoError(t, err)
	defer func() { _ = gzRes.Body.Close() }()
	require.Equal(t, http.StatusOK, gzRes.StatusCode)
	require.Equal(t, "gzip", gzRes.Header.Get("Content-Encoding"))
	raw, err := io.ReadAll(gzRes.Body)
	require.NoError(t, err)
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	require.NoError(t, err)
	defer func() { _ = zr.Close() }()
	unzipped, err := io.ReadAll(zr)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(unzipped, &payload))
	require.NotEmpty(t, payload["locations"])
}

type locationList struct {
	Locations []locationItem `json:"locations"`
}

type locationItem struct {
	ID       string  `json:"id"`
	ParentID *string `json:"parent_id"`
	Type     string  `json:"type"`
	Name     string  `json:"name"`
	IsActive bool    `json:"is_active"`
}

func (e *env) locations(path, lang string) locationList {
	e.t.Helper()
	res := e.call(http.MethodGet, path, nil, map[string]string{"Accept-Language": lang})
	require.Equal(e.t, http.StatusOK, res.Status, "%s: %s", path, res.Body)
	var out locationList
	require.NoError(e.t, json.Unmarshal(res.Body, &out), string(res.Body))
	return out
}

func locationName(list locationList, id string) string {
	for _, loc := range list.Locations {
		if loc.ID == id {
			return loc.Name
		}
	}
	return ""
}

func urlQuery(s string) string {
	return url.QueryEscape(s)
}

func (e *env) uploadPhoto(token, filename, contentType string, body []byte) response {
	e.t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	hdr := make(textproto.MIMEHeader)
	hdr.Set("Content-Disposition", `form-data; name="photo"; filename="`+filename+`"`)
	hdr.Set("Content-Type", contentType)
	part, err := w.CreatePart(hdr)
	require.NoError(e.t, err)
	_, err = part.Write(body)
	require.NoError(e.t, err)
	require.NoError(e.t, w.Close())

	req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/v1/me/photo", &buf)
	require.NoError(e.t, err)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := e.srv.Client().Do(req)
	require.NoError(e.t, err)
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	require.NoError(e.t, err)
	out := response{Status: res.StatusCode, Header: res.Header, Body: raw}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out.Err)
		_ = json.Unmarshal(raw, &out.decoded)
	}
	return out
}

func tinyJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, img, &jpeg.Options{Quality: 70}))
	return buf.Bytes()
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: 1, G: 2, B: 3, A: 255})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}
