package push

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/ride"
)

func testCredentials(t *testing.T, tokenURL string) ([]byte, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	creds, _ := json.Marshal(map[string]string{
		"type":         "service_account",
		"project_id":   "bilego-test",
		"client_email": "push@bilego-test.iam.gserviceaccount.com",
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"token_uri":    tokenURL,
	})
	return creds, key
}

// fakeGoogle serves the OAuth token endpoint and the FCM send endpoint.
func fakeGoogle(t *testing.T, key *rsa.PrivateKey, send http.HandlerFunc) (*httptest.Server, *int32) {
	t.Helper()
	var tokenCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&tokenCalls, 1)
		_ = r.ParseForm()
		parts := strings.Split(r.PostForm.Get("assertion"), ".")
		if len(parts) != 3 {
			http.Error(w, "bad assertion", http.StatusBadRequest)
			return
		}
		sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
		sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}
		claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
		if !strings.Contains(string(claims), fcmScope) {
			http.Error(w, "bad scope", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"access_token":"ya29.test","expires_in":3600,"token_type":"Bearer"}`)
	})
	mux.HandleFunc("/send", send)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &tokenCalls
}

func newTestFCM(t *testing.T, send http.HandlerFunc) (*FCM, *int32) {
	t.Helper()
	var srv *httptest.Server
	var calls *int32
	// The token URL is part of the credentials, so the server starts first.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv, calls = fakeGoogle(t, key, send)
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	creds, _ := json.Marshal(map[string]string{
		"type":         "service_account",
		"project_id":   "bilego-test",
		"client_email": "push@bilego-test.iam.gserviceaccount.com",
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"token_uri":    srv.URL + "/token",
	})
	f, err := NewFCM(creds, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	f.sendURL = srv.URL + "/send"
	return f, calls
}

func TestNewFCMRejectsBadCredentials(t *testing.T) {
	t.Parallel()
	if _, err := NewFCM([]byte(`{"type":"authorized_user"}`), nil); err == nil {
		t.Fatal("expected an error for a non service account")
	}
	creds, _ := testCredentials(t, "")
	f, err := NewFCM(creds, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f.ProjectID() != "bilego-test" || f.tokenURL != defaultTokenURL {
		t.Fatalf("project %q token url %q", f.ProjectID(), f.tokenURL)
	}
}

func TestFCMSendsAndCachesToken(t *testing.T) {
	t.Parallel()
	var got fcmRequest
	f, tokenCalls := newTestFCM(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ya29.test" {
			http.Error(w, "no auth", http.StatusUnauthorized)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = io.WriteString(w, `{"name":"projects/bilego-test/messages/1"}`)
	})
	msg := Message{Title: "Ýolugra", Body: "hello", Data: map[string]string{"type": "trip_reminder"}}
	for range 2 {
		if err := f.Send(context.Background(), "device-token", msg); err != nil {
			t.Fatal(err)
		}
	}
	if n := atomic.LoadInt32(tokenCalls); n != 1 {
		t.Fatalf("token fetched %d times, want 1", n)
	}
	m := got.Message
	if m.Token != "device-token" || m.Notification.Body != "hello" || m.Data["type"] != "trip_reminder" ||
		m.Android.Notification.ChannelID != AndroidChannel || m.Android.Priority != "HIGH" {
		t.Fatalf("unexpected request %+v", m)
	}
}

func TestFCMReportsInvalidTokens(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		status int
		body   string
		dead   bool
	}{
		"unregistered":    {404, `{"error":{"code":404,"status":"NOT_FOUND","details":[{"errorCode":"UNREGISTERED"}]}}`, true},
		"sender mismatch": {403, `{"error":{"code":403,"status":"PERMISSION_DENIED","details":[{"errorCode":"SENDER_ID_MISMATCH"}]}}`, true},
		"quota":           {429, `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","details":[{"errorCode":"QUOTA_EXCEEDED"}]}}`, false},
		"server":          {503, `oops`, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f, _ := newTestFCM(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(c.status)
				_, _ = io.WriteString(w, c.body)
			})
			err := f.Send(context.Background(), "t", Message{Title: "x", Body: "y"})
			if err == nil {
				t.Fatal("expected an error")
			}
			if errors.Is(err, ErrInvalidToken) != c.dead {
				t.Fatalf("err = %v, invalid token = %v", err, c.dead)
			}
		})
	}
}

func TestBuildLocalizesText(t *testing.T) {
	t.Parallel()
	j := ride.PushJob{
		NotificationID: "n1", Type: ride.NoteBookingRequested, TripID: "t1", Locale: "ru",
		From:  &ride.Location{NameTK: "Aşgabat", NameRU: "Ашхабад", NameEN: "Ashgabat"},
		To:    &ride.Location{NameTK: "Mary", NameRU: "Мары"},
		Actor: &ride.UserSummary{FirstName: "Aman", LastName: "Amanow"},
	}
	m, ok := Build("Ýolugra", j)
	if !ok {
		t.Fatal("no message")
	}
	if m.Body != "Aman Amanow запрашивает места: Ашхабад → Мары" {
		t.Fatalf("body = %q", m.Body)
	}
	if m.Data["notification_id"] != "n1" || m.Data["trip_id"] != "t1" || m.Data["type"] != "booking_requested" {
		t.Fatalf("data = %v", m.Data)
	}
	if _, has := m.Data["booking_id"]; has {
		t.Fatal("empty booking_id must be left out")
	}

	j.Locale, j.Actor = "en", nil
	m, _ = Build("Ýolugra", j)
	// English falls back to the Turkmen name when a place has no English name.
	if m.Body != "A user asks for seats on Ashgabat → Mary" {
		t.Fatalf("body = %q", m.Body)
	}

	j.Locale = "tk"
	j.Type = ride.NoteTripCancelled
	m, _ = Build("Ýolugra", j)
	if m.Body != "Aşgabat → Mary syýahaty ýatyryldy" {
		t.Fatalf("body = %q", m.Body)
	}

	if _, ok := Build("x", ride.PushJob{Type: "something_new"}); ok {
		t.Fatal("unknown types have no text")
	}
}

func TestEveryNotificationTypeHasText(t *testing.T) {
	t.Parallel()
	for _, typ := range []ride.NotificationType{
		ride.NoteBookingRequested, ride.NoteBookingApproved, ride.NoteBookingRejected, ride.NoteBookingCancelledP,
		ride.NoteBookingCancelledD, ride.NoteBookingExpired, ride.NoteTripCancelled, ride.NoteTripUpdated,
		ride.NoteTripReminder, ride.NoteTripCompleted, ride.NoteReviewReceived,
	} {
		if _, ok := texts[typ]; !ok {
			t.Errorf("no push text for %s", typ)
		}
	}
}

type fakeStore struct {
	batches [][]ride.PushJob
	deleted []string
	pruned  int
}

func (s *fakeStore) ClaimPushes(_ context.Context, _ time.Time, _ int) ([]ride.PushJob, error) {
	if len(s.batches) == 0 {
		return nil, nil
	}
	b := s.batches[0]
	s.batches = s.batches[1:]
	return b, nil
}

func (s *fakeStore) DeletePushTokens(_ context.Context, tokens []string) error {
	s.deleted = append(s.deleted, tokens...)
	return nil
}

func (s *fakeStore) PrunePushTokens(context.Context, time.Time) error {
	s.pruned++
	return nil
}

type fakeSender struct{ sent []string }

func (s *fakeSender) Send(_ context.Context, token string, _ Message) error {
	s.sent = append(s.sent, token)
	switch token {
	case "dead":
		return ErrInvalidToken
	case "flaky":
		return errors.New("http 503")
	}
	return nil
}

func TestDispatcherDeliversAndCleansUp(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	job := func(id string, age time.Duration, tokens ...string) ride.PushJob {
		return ride.PushJob{NotificationID: id, Type: ride.NoteTripReminder, CreatedAt: now.Add(-age), Tokens: tokens}
	}
	store := &fakeStore{batches: [][]ride.PushJob{
		{job("a", time.Second, "ok", "dead"), job("b", time.Hour, "old")},
		{job("c", time.Second, "flaky"), job("d", time.Second)},
	}}
	sender := &fakeSender{}
	d := NewDispatcher(store, sender, Config{Title: "Ýolugra", MaxAge: 30 * time.Minute, TokenTTL: 24 * time.Hour, Batch: 2}, nil)
	d.now = func() time.Time { return now }

	if err := d.Maintain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(sender.sent, ","); got != "ok,dead,flaky" {
		t.Fatalf("sent to %s; a stale row or a row without tokens was sent", got)
	}
	if len(store.deleted) != 1 || store.deleted[0] != "dead" {
		t.Fatalf("deleted %v", store.deleted)
	}
	if store.pruned != 1 {
		t.Fatalf("pruned %d times", store.pruned)
	}
	// Pruning runs at most once an hour.
	if err := d.Maintain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.pruned != 1 {
		t.Fatalf("pruned again within the hour")
	}
}
