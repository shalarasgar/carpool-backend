package integration

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestFullOTPLoginFlow covers request-otp -> gateway pending -> report ->
// verify-otp -> refresh -> logout over real HTTP and PostgreSQL.
func TestFullOTPLoginFlow(t *testing.T) {
	e := newEnv(t)
	const phone = "+99365123456"
	const device = "flutter-device-1"

	_, apiKey := e.createGateway("android-phone")
	hb := e.call(http.MethodPost, "/gateway/v1/heartbeat", nil, bearer(apiKey))
	require.Equal(t, http.StatusOK, hb.Status)

	// 1. request-otp: DEV_MODE returns the code; the SMS lands in the queue.
	req := e.call(http.MethodPost, "/v1/auth/request-otp", map[string]any{"phone": "8 65 12-34-56"}, map[string]string{"Accept-Language": "ru"})
	require.Equal(t, http.StatusOK, req.Status, string(req.Body))
	require.Equal(t, float64(300), req.field("expires_in"))
	require.Equal(t, float64(60), req.field("retry_after"))
	code := req.str("dev_code")
	require.Len(t, code, 6)
	require.Equal(t, "ru", req.Header.Get("Content-Language"))

	// Only the HMAC is stored.
	require.Equal(t, 0, e.queryInt(`SELECT count(*) FROM otp_codes WHERE code_hash = $1`, code))
	require.Equal(t, 1, e.queryInt(`SELECT count(*) FROM otp_codes WHERE phone = $1 AND used = false AND length(code_hash) = 64`, phone))

	// 2. A second request inside the cooldown is 429 with Retry-After.
	again := e.call(http.MethodPost, "/v1/auth/request-otp", map[string]any{"phone": phone}, nil)
	require.Equal(t, http.StatusTooManyRequests, again.Status)
	require.Equal(t, "RATE_LIMITED", again.Err.Error.Code)
	require.Equal(t, "60", again.Header.Get("Retry-After"))
	require.Equal(t, "Haýyşlar örän köp. Soňrak synanyşyň.", again.Err.Error.Message, "default locale is tk")

	// 3. The gateway polls and gets the SMS with the code in the Russian template.
	pending := e.call(http.MethodGet, "/gateway/v1/pending?limit=10", nil, bearer(apiKey))
	require.Equal(t, http.StatusOK, pending.Status, string(pending.Body))
	msgs := pending.decoded["messages"].([]any)
	require.Len(t, msgs, 1)
	msg := msgs[0].(map[string]any)
	require.Equal(t, phone, msg["phone"])
	require.Equal(t, "Carpool: kod "+code+". Nikomu ne soobshchaite ego.", msg["message"])
	require.NotEmpty(t, msg["locked_until"])

	// Polling again returns nothing while the message is locked.
	pending = e.call(http.MethodGet, "/gateway/v1/pending", nil, bearer(apiKey))
	require.Empty(t, pending.decoded["messages"])

	report := e.call(http.MethodPost, "/gateway/v1/report", map[string]any{"id": msg["id"], "status": "sent"}, bearer(apiKey))
	require.Equal(t, http.StatusOK, report.Status, string(report.Body))
	require.Equal(t, "sent", report.str("status"))
	require.Equal(t, 1, e.queryInt(`SELECT count(*) FROM sms_queue WHERE status = 'sent' AND sent_at IS NOT NULL AND locked_until IS NULL`))

	ready := e.call(http.MethodGet, "/readyz", nil, nil)
	require.Equal(t, true, ready.decoded["sms_gateway_healthy"])

	// 4. A wrong code is rejected, localized, and counted.
	wrongCode := "000000"
	if wrongCode == code {
		wrongCode = "000001"
	}
	bad := e.call(http.MethodPost, "/v1/auth/verify-otp", map[string]any{"phone": phone, "code": wrongCode, "device_id": device}, map[string]string{"Accept-Language": "en-US,en;q=0.9"})
	require.Equal(t, http.StatusUnauthorized, bad.Status)
	require.Equal(t, "OTP_INVALID", bad.Err.Error.Code)
	require.Equal(t, "Invalid code.", bad.Err.Error.Message)
	require.Equal(t, 1, e.queryInt(`SELECT attempts FROM otp_codes WHERE phone = $1`, phone))
	require.Equal(t, 0, e.queryInt(`SELECT count(*) FROM users`), "no user is created on a failed verification")

	// 5. The right code logs in and creates the user.
	login := e.call(http.MethodPost, "/v1/auth/verify-otp", map[string]any{"phone": phone, "code": code, "device_id": device}, map[string]string{"Accept-Language": "ru"})
	require.Equal(t, http.StatusOK, login.Status, string(login.Body))
	require.Equal(t, true, login.field("is_new_user"))
	require.Equal(t, "Bearer", login.str("token_type"))
	require.Equal(t, float64(900), login.field("expires_in"))
	access := login.str("access_token")
	refresh := login.str("refresh_token")
	require.NotEmpty(t, access)
	require.NotEmpty(t, refresh)
	user := login.decoded["user"].(map[string]any)
	require.Equal(t, phone, user["phone"])
	require.Equal(t, "ru", user["locale"])
	require.NotEmpty(t, user["id"])

	require.Equal(t, 1, e.queryInt(`SELECT count(*) FROM users WHERE phone = $1 AND is_blocked = false`, phone))
	require.Equal(t, 1, e.queryInt(`SELECT count(*) FROM otp_codes WHERE phone = $1 AND used = true`, phone))
	require.Equal(t, 1, e.queryInt(`SELECT count(*) FROM refresh_tokens WHERE device_id = $1 AND revoked_at IS NULL`, device))
	require.Equal(t, 0, e.queryInt(`SELECT count(*) FROM refresh_tokens WHERE token_hash = $1`, refresh), "refresh token stored hashed")

	// The code cannot be replayed.
	replay := e.call(http.MethodPost, "/v1/auth/verify-otp", map[string]any{"phone": phone, "code": code, "device_id": device}, nil)
	require.Equal(t, http.StatusUnauthorized, replay.Status)
	require.Equal(t, "OTP_INVALID", replay.Err.Error.Code)

	// 6. Logout requires a valid access token.
	noAuth := e.call(http.MethodPost, "/v1/auth/logout", nil, nil)
	require.Equal(t, http.StatusUnauthorized, noAuth.Status)
	require.Equal(t, "UNAUTHORIZED", noAuth.Err.Error.Code)
	badAuth := e.call(http.MethodPost, "/v1/auth/logout", nil, bearer(access+"x"))
	require.Equal(t, http.StatusUnauthorized, badAuth.Status)

	// 7. Refresh rotates: the old token dies, a new pair comes back.
	rotated := e.call(http.MethodPost, "/v1/auth/refresh", map[string]any{"refresh_token": refresh}, nil)
	require.Equal(t, http.StatusOK, rotated.Status, string(rotated.Body))
	access2 := rotated.str("access_token")
	refresh2 := rotated.str("refresh_token")
	require.NotEmpty(t, access2)
	require.NotEqual(t, refresh, refresh2)
	require.Equal(t, 1, e.queryInt(`SELECT count(*) FROM refresh_tokens WHERE device_id = $1 AND revoked_at IS NULL`, device))
	require.Equal(t, 2, e.queryInt(`SELECT count(*) FROM refresh_tokens WHERE device_id = $1`, device))

	garbage := e.call(http.MethodPost, "/v1/auth/refresh", map[string]any{"refresh_token": "not-a-token"}, nil)
	require.Equal(t, http.StatusUnauthorized, garbage.Status)
	require.Equal(t, "REFRESH_TOKEN_INVALID", garbage.Err.Error.Code)

	// 8. Logout with the new access token revokes the device.
	logout := e.call(http.MethodPost, "/v1/auth/logout", nil, bearer(access2))
	require.Equal(t, http.StatusNoContent, logout.Status)
	require.Equal(t, 0, e.queryInt(`SELECT count(*) FROM refresh_tokens WHERE device_id = $1 AND revoked_at IS NULL`, device))

	afterLogout := e.call(http.MethodPost, "/v1/auth/refresh", map[string]any{"refresh_token": refresh2}, nil)
	require.Equal(t, http.StatusUnauthorized, afterLogout.Status)
	require.Equal(t, "REFRESH_TOKEN_INVALID", afterLogout.Err.Error.Code)

	// 9. Second login for the same phone is not a new user.
	e.advance(61 * time.Second)
	req2 := e.call(http.MethodPost, "/v1/auth/request-otp", map[string]any{"phone": phone}, nil)
	require.Equal(t, http.StatusOK, req2.Status, string(req2.Body))
	login2 := e.call(http.MethodPost, "/v1/auth/verify-otp", map[string]any{"phone": phone, "code": req2.str("dev_code"), "device_id": "second-device"}, nil)
	require.Equal(t, http.StatusOK, login2.Status, string(login2.Body))
	require.Equal(t, false, login2.field("is_new_user"))
	require.Equal(t, user["id"], login2.decoded["user"].(map[string]any)["id"])
}

func TestRefreshTokenReuseRevokesDevice(t *testing.T) {
	e := newEnv(t)
	first := e.login("+99365000010", "dev-reuse")

	rotated := e.call(http.MethodPost, "/v1/auth/refresh", map[string]any{"refresh_token": first.refresh}, nil)
	require.Equal(t, http.StatusOK, rotated.Status)
	second := rotated.str("refresh_token")

	// Presenting the rotated-away token is treated as theft.
	reuse := e.call(http.MethodPost, "/v1/auth/refresh", map[string]any{"refresh_token": first.refresh}, nil)
	require.Equal(t, http.StatusUnauthorized, reuse.Status)
	require.Equal(t, "REFRESH_TOKEN_INVALID", reuse.Err.Error.Code)

	// ...so the newest token of that device is dead too.
	after := e.call(http.MethodPost, "/v1/auth/refresh", map[string]any{"refresh_token": second}, nil)
	require.Equal(t, http.StatusUnauthorized, after.Status)
	require.Equal(t, 0, e.queryInt(`SELECT count(*) FROM refresh_tokens WHERE device_id = 'dev-reuse' AND revoked_at IS NULL`))

	// Expired tokens get their own code.
	other := e.login("+99365000011", "dev-exp")
	e.exec(`UPDATE refresh_tokens SET expires_at = now() - interval '1 second' WHERE device_id = 'dev-exp'`)
	expired := e.call(http.MethodPost, "/v1/auth/refresh", map[string]any{"refresh_token": other.refresh}, nil)
	require.Equal(t, http.StatusUnauthorized, expired.Status)
	require.Equal(t, "REFRESH_TOKEN_EXPIRED", expired.Err.Error.Code)
}

func TestOTPAttemptLimitAndExpiry(t *testing.T) {
	e := newEnv(t)
	const phone = "+99362000001"

	req := e.call(http.MethodPost, "/v1/auth/request-otp", map[string]any{"phone": phone}, nil)
	require.Equal(t, http.StatusOK, req.Status)
	code := req.str("dev_code")
	wrong := "999999"
	if wrong == code {
		wrong = "999998"
	}
	for i := 0; i < 5; i++ {
		res := e.call(http.MethodPost, "/v1/auth/verify-otp", map[string]any{"phone": phone, "code": wrong, "device_id": "d"}, nil)
		require.Equal(t, http.StatusUnauthorized, res.Status)
		require.Equal(t, "OTP_INVALID", res.Err.Error.Code, "attempt %d", i+1)
	}
	// The correct code no longer works: the record was invalidated after 5 misses.
	res := e.call(http.MethodPost, "/v1/auth/verify-otp", map[string]any{"phone": phone, "code": code, "device_id": "d"}, nil)
	require.Equal(t, http.StatusUnauthorized, res.Status)
	require.Equal(t, "OTP_INVALID", res.Err.Error.Code)
	require.Equal(t, 1, e.queryInt(`SELECT count(*) FROM otp_codes WHERE phone = $1 AND used = true AND attempts = 5`, phone))

	// A new code replaces the old one; an expired code is reported as such.
	e.advance(61 * time.Second)
	req = e.call(http.MethodPost, "/v1/auth/request-otp", map[string]any{"phone": phone}, nil)
	require.Equal(t, http.StatusOK, req.Status)
	e.exec(`UPDATE otp_codes SET expires_at = now() - interval '1 second' WHERE phone = $1 AND used = false`, phone)
	res = e.call(http.MethodPost, "/v1/auth/verify-otp", map[string]any{"phone": phone, "code": req.str("dev_code"), "device_id": "d"}, nil)
	require.Equal(t, http.StatusUnauthorized, res.Status)
	require.Equal(t, "OTP_EXPIRED", res.Err.Error.Code)

	// A phone that never requested a code gets the same OTP_INVALID as a wrong code.
	res = e.call(http.MethodPost, "/v1/auth/verify-otp", map[string]any{"phone": "+99362000002", "code": "123456", "device_id": "d"}, nil)
	require.Equal(t, http.StatusUnauthorized, res.Status)
	require.Equal(t, "OTP_INVALID", res.Err.Error.Code)
}

func TestBlockedUserCannotAuthenticate(t *testing.T) {
	e := newEnv(t)
	const phone = "+99371000001"
	session := e.login(phone, "dev-blocked")
	e.exec(`UPDATE users SET is_blocked = true WHERE phone = $1`, phone)

	// request-otp looks exactly like it does for anyone else.
	e.advance(61 * time.Second)
	req := e.call(http.MethodPost, "/v1/auth/request-otp", map[string]any{"phone": phone}, nil)
	require.Equal(t, http.StatusOK, req.Status)
	require.Len(t, req.str("dev_code"), 6)
	require.Equal(t, float64(300), req.field("expires_in"))

	// verify-otp with a correct code is refused.
	res := e.call(http.MethodPost, "/v1/auth/verify-otp", map[string]any{"phone": phone, "code": req.str("dev_code"), "device_id": "dev-blocked"}, nil)
	require.Equal(t, http.StatusForbidden, res.Status)
	require.Equal(t, "USER_BLOCKED", res.Err.Error.Code)
	require.Equal(t, "Hasabyňyz petiklenen.", res.Err.Error.Message)

	// Existing sessions cannot be refreshed either.
	refresh := e.call(http.MethodPost, "/v1/auth/refresh", map[string]any{"refresh_token": session.refresh}, nil)
	require.Equal(t, http.StatusForbidden, refresh.Status)
	require.Equal(t, "USER_BLOCKED", refresh.Err.Error.Code)
}

func TestRequestOTPValidationAndRateLimits(t *testing.T) {
	e := newEnv(t)

	for _, body := range []any{
		map[string]any{"phone": "+99312345678"},
		map[string]any{"phone": "12345"},
		map[string]any{"phone": "+7 912 000 00 00"},
	} {
		res := e.call(http.MethodPost, "/v1/auth/request-otp", body, nil)
		require.Equal(t, http.StatusUnprocessableEntity, res.Status, string(res.Body))
		require.Equal(t, "INVALID_PHONE", res.Err.Error.Code)
	}

	res := e.call(http.MethodPost, "/v1/auth/request-otp", map[string]any{}, nil)
	require.Equal(t, http.StatusUnprocessableEntity, res.Status)
	require.Equal(t, "VALIDATION_ERROR", res.Err.Error.Code)
	require.Equal(t, "required", res.Err.Error.Details["phone"])

	res = e.call(http.MethodPost, "/v1/auth/request-otp", `{"phone": `, nil)
	require.Equal(t, http.StatusBadRequest, res.Status)
	require.Equal(t, "BAD_REQUEST", res.Err.Error.Code)

	res = e.call(http.MethodPost, "/v1/auth/request-otp", map[string]any{"phone": "+99365123456", "unexpected": 1}, nil)
	require.Equal(t, http.StatusBadRequest, res.Status, "unknown fields are rejected")

	res = e.call(http.MethodPost, "/v1/auth/request-otp", `{"phone":"+99365123456","padding":"`+strings.Repeat("x", 70000)+`"}`, nil)
	require.Equal(t, http.StatusRequestEntityTooLarge, res.Status)
	require.Equal(t, "PAYLOAD_TOO_LARGE", res.Err.Error.Code)

	res = e.call(http.MethodPost, "/v1/auth/verify-otp", map[string]any{"phone": "+99365123456", "code": "12345", "device_id": "d"}, nil)
	require.Equal(t, http.StatusUnprocessableEntity, res.Status)
	require.Equal(t, "len", res.Err.Error.Details["code"])
	res = e.call(http.MethodPost, "/v1/auth/verify-otp", map[string]any{"phone": "+99365123456", "code": "123456"}, nil)
	require.Equal(t, http.StatusUnprocessableEntity, res.Status)
	require.Equal(t, "required", res.Err.Error.Details["device_id"])

	// Daily cap: 5 per phone per 24h even when the cooldown has passed.
	const phone = "+99365777777"
	for i := 0; i < 5; i++ {
		ok := e.call(http.MethodPost, "/v1/auth/request-otp", map[string]any{"phone": phone}, nil)
		require.Equal(t, http.StatusOK, ok.Status, "request %d", i+1)
		e.advance(61 * time.Second)
	}
	capped := e.call(http.MethodPost, "/v1/auth/request-otp", map[string]any{"phone": phone}, nil)
	require.Equal(t, http.StatusTooManyRequests, capped.Status)
	require.NotEmpty(t, capped.Header.Get("Retry-After"))
	require.Equal(t, 5, e.queryInt(`SELECT count(*) FROM otp_codes WHERE phone = $1`, phone))
	require.Equal(t, 1, e.queryInt(`SELECT count(*) FROM otp_codes WHERE phone = $1 AND used = false`, phone), "only the newest code is active")

	// Per-device cap works independently of the phone.
	for i := 0; i < 3; i++ {
		ok := e.call(http.MethodPost, "/v1/auth/request-otp", map[string]any{"phone": "+9936500001" + string(rune('0'+i)), "device_id": "shared-device"}, nil)
		require.Equal(t, http.StatusOK, ok.Status, string(ok.Body))
	}
}

type session struct {
	access  string
	refresh string
	userID  string
}

// login performs request-otp + verify-otp for phone and returns the tokens.
func (e *env) login(phone, device string) session {
	e.t.Helper()
	req := e.call(http.MethodPost, "/v1/auth/request-otp", map[string]any{"phone": phone}, nil)
	require.Equal(e.t, http.StatusOK, req.Status, string(req.Body))
	res := e.call(http.MethodPost, "/v1/auth/verify-otp", map[string]any{"phone": phone, "code": req.str("dev_code"), "device_id": device}, nil)
	require.Equal(e.t, http.StatusOK, res.Status, string(res.Body))
	return session{
		access:  res.str("access_token"),
		refresh: res.str("refresh_token"),
		userID:  res.decoded["user"].(map[string]any)["id"].(string),
	}
}
