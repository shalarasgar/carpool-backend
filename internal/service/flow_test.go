package service

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/auth"
	"github.com/shalarasgar/carpool-backend/internal/smsqueue"
)

func TestOTPRoundTripDoesNotReuseAFailedSend(t *testing.T) {
	t.Parallel()
	db := newMemDB()
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	authSvc := NewAuth(db, testAuthConfig(), discardLogger())
	authSvc.now = func() time.Time { return now }
	gw := NewGateway(db, GatewayConfig{Lease: time.Minute, SendingLease: 3 * time.Minute, MaxAttempts: 3, MessageMaxAge: 5 * time.Minute, RatePerMinute: 1000}, discardLogger())
	gw.now = func() time.Time { return now }

	_, key := registerGateway(t, gw, "pixel", nil)
	authSvc.cfg.ReturnDevCode = true
	code, err := authSvc.RequestOTP(context.Background(), phoneNumber, "10.0.0.8", "device-1", "tk")
	if err != nil || len(code) != 6 {
		t.Fatalf("dev code err=%v len=%d", err, len(code))
	}

	claimer, err := gw.Authenticate(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := gw.Claim(context.Background(), claimer, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim = %+v err=%v", jobs, err)
	}
	job := jobs[0]
	if job.Message != smsqueue.SMSText("tk", "carpool", code) {
		t.Fatalf("gateway message = %q", job.Message)
	}
	if err := gw.MarkSending(context.Background(), claimer.ID, job.ID); err != nil {
		t.Fatal(err)
	}
	// Lease expires after the radio hand-off. The job must not become pending again.
	now = now.Add(4 * time.Minute)
	if err := gw.Maintain(context.Background()); err != nil {
		t.Fatal(err)
	}
	again, err := gw.Claim(context.Background(), claimer, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("expired in-flight job was claimed again: %+v", again)
	}
	if err := gw.Report(context.Background(), claimer.ID, smsqueue.Report{ID: job.ID, Status: smsqueue.ReportSent}); err != nil {
		t.Fatal(err)
	}
	if err := gw.Report(context.Background(), claimer.ID, smsqueue.Report{ID: job.ID, Status: smsqueue.ReportSent}); err != nil {
		t.Fatal("duplicate sent report should succeed")
	}
	session, err := authSvc.VerifyOTP(context.Background(), phoneNumber, code, "device-1", "tk")
	if err != nil || session.User.Phone != phoneNumber || !session.User.IsNew {
		t.Fatalf("verify = %+v err=%v", session.User, err)
	}
	if _, err := authSvc.VerifyOTP(context.Background(), phoneNumber, code, "device-1", "tk"); err != auth.ErrInvalidCode {
		t.Fatalf("replay err = %v", err)
	}
}

const phoneNumber = "+99361111223"

func TestTwoGatewaysCannotClaimTheSameJob(t *testing.T) {
	t.Parallel()
	db := newMemDB()
	gw := NewGateway(db, GatewayConfig{MaxAttempts: 3, MessageMaxAge: 5 * time.Minute, RatePerMinute: 1000}, discardLogger())
	_, keyA := registerGateway(t, gw, "a", nil)
	_, keyB := registerGateway(t, gw, "b", nil)
	db.insertJob(smsqueue.Job{ID: "job-1", Phone: phoneNumber, Message: "carpool: kod 111111. Kody hiçkim bilen paýlaşmaň!", Status: smsqueue.StatusPending, CreatedAt: time.Now()})

	var wg sync.WaitGroup
	claimed := make(chan string, 2)
	for _, key := range []string{keyA, keyB} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			g, err := gw.Authenticate(context.Background(), key)
			if err != nil {
				t.Errorf("auth: %v", err)
				return
			}
			jobs, err := gw.Claim(context.Background(), g, 1)
			if err != nil {
				t.Errorf("claim: %v", err)
				return
			}
			if len(jobs) == 1 {
				claimed <- g.ID
			}
		}(key)
	}
	wg.Wait()
	close(claimed)
	n := 0
	for range claimed {
		n++
	}
	if n != 1 {
		t.Fatalf("claimers = %d, want 1", n)
	}
}

func TestWrongCodeLocksAfterMaxAttempts(t *testing.T) {
	t.Parallel()
	db := newMemDB()
	authSvc := NewAuth(db, testAuthConfig(), discardLogger())
	authSvc.cfg.ReturnDevCode = true
	authSvc.cfg.MaxAttempts = 5
	code, err := authSvc.RequestOTP(context.Background(), phoneNumber, "10.0.0.1", "device-1", "en")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := authSvc.VerifyOTP(context.Background(), phoneNumber, "000000", "device-1", "en"); err != auth.ErrInvalidCode {
			t.Fatalf("attempt %d err = %v", i, err)
		}
	}
	if _, err := authSvc.VerifyOTP(context.Background(), phoneNumber, code, "device-1", "en"); err != auth.ErrInvalidCode {
		t.Fatalf("correct code after lockout err = %v", err)
	}
}

func TestTestPhonesSkipRateLimits(t *testing.T) {
	t.Parallel()
	db := newMemDB()
	cfg := testAuthConfig()
	cfg.TestPhones = map[string]bool{phoneNumber: true}
	authSvc := NewAuth(db, cfg, discardLogger())
	for i := 0; i < cfg.PhoneDailyMax+2; i++ {
		if _, err := authSvc.RequestOTP(context.Background(), phoneNumber, "10.0.0.1", "device-1", "tk"); err != nil {
			t.Fatalf("test phone request %d err = %v", i, err)
		}
	}
	const other = "+99361111224"
	if _, err := authSvc.RequestOTP(context.Background(), other, "10.0.0.1", "device-1", "tk"); err != nil {
		t.Fatal(err)
	}
	if _, err := authSvc.RequestOTP(context.Background(), other, "10.0.0.1", "device-1", "tk"); err == nil {
		t.Fatal("a regular phone must still be rate limited")
	}
}

func TestPrefixGatewayReceivesItsOwnOperator(t *testing.T) {
	t.Parallel()
	db := newMemDB()
	now := time.Now()
	gw := NewGateway(db, GatewayConfig{RatePerMinute: 1000, MessageMaxAge: 5 * time.Minute}, discardLogger())
	gw.now = func() time.Time { return now }
	id, key := registerGateway(t, gw, "tmcell", []string{"61"})
	if err := gw.Heartbeat(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	authSvc := NewAuth(db, testAuthConfig(), discardLogger())
	authSvc.now = func() time.Time { return now }
	authSvc.cfg.ReturnDevCode = true
	if _, err := authSvc.RequestOTP(context.Background(), phoneNumber, "", "device-1", "tk"); err != nil {
		t.Fatal(err)
	}
	otherDBGateway := smsqueue.Gateway{ID: "other", Name: "other", Active: true}
	db.mu.Lock()
	db.gateways["other"] = otherDBGateway
	db.byHash["other-hash"] = "other"
	db.mu.Unlock()
	jobs, err := gw.Claim(context.Background(), otherDBGateway, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("unrelated gateway received %+v", jobs)
	}
	owner, err := gw.Authenticate(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err = gw.Claim(context.Background(), owner, 10)
	if err != nil || len(jobs) != 1 || jobs[0].Phone != phoneNumber {
		t.Fatalf("owner claim = %+v err=%v", jobs, err)
	}
}

func testAuthConfig() AuthConfig {
	return AuthConfig{
		AppName:         "carpool",
		OTPSecret:       "otp-hmac-secret-value",
		JWTSecret:       "jwt-secret-value-xxxx",
		Prefixes:        []string{"6", "7"},
		OTPTTL:          5 * time.Minute,
		MaxAttempts:     5,
		PhoneInterval:   time.Minute,
		PhoneDailyMax:   5,
		IPHourlyMax:     30,
		DeviceHourlyMax: 10,
		AccessTTL:       15 * time.Minute,
		RefreshTTL:      30 * 24 * time.Hour,
	}
}

func registerGateway(t *testing.T, gw *Gateway, name string, prefixes []string) (string, string) {
	t.Helper()
	g, raw, err := gw.CreateGateway(context.Background(), name, prefixes)
	if err != nil || raw == "" {
		t.Fatalf("create gateway %s: %v", name, err)
	}
	return g.ID, raw
}

type memDB struct {
	mu       sync.Mutex
	otps     []memOTP
	users    map[string]auth.SessionUser
	blocked  map[string]bool
	refresh  map[string]memRefresh
	jobs     map[string]smsqueue.Job
	gateways map[string]smsqueue.Gateway
	byHash   map[string]string
	seq      int
}

type memOTP struct {
	phone, hash, ip, device string
	expires, created        time.Time
	attempts                int
	used                    bool
}

type memRefresh struct {
	userID, device string
	expires        time.Time
	revoked        bool
}

func newMemDB() *memDB {
	return &memDB{
		users:    map[string]auth.SessionUser{},
		blocked:  map[string]bool{},
		refresh:  map[string]memRefresh{},
		jobs:     map[string]smsqueue.Job{},
		gateways: map[string]smsqueue.Gateway{},
		byHash:   map[string]string{},
	}
}

func (m *memDB) CreateChallenge(_ context.Context, in auth.Challenge) (auth.ChallengeResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !in.SkipLimits {
		for i := len(m.otps) - 1; i >= 0; i-- {
			if m.otps[i].phone == in.Phone {
				if in.Now.Sub(m.otps[i].created) < in.PhoneInterval {
					return auth.ChallengeResult{}, &smsqueue.RateLimited{RetryAfter: in.PhoneInterval - in.Now.Sub(m.otps[i].created)}
				}
				break
			}
		}
		daily := 0
		for _, otp := range m.otps {
			if otp.phone == in.Phone && in.Now.Sub(otp.created) < 24*time.Hour {
				daily++
			}
		}
		if daily >= in.PhoneDailyMax {
			return auth.ChallengeResult{}, &smsqueue.RateLimited{RetryAfter: time.Minute}
		}
	}
	for i := range m.otps {
		if m.otps[i].phone == in.Phone {
			m.otps[i].used = true
		}
	}
	m.otps = append(m.otps, memOTP{phone: in.Phone, hash: in.CodeHash, ip: in.IP, device: in.DeviceID, expires: in.ExpiresAt, created: in.Now})
	list := make([]smsqueue.Gateway, 0, len(m.gateways))
	for _, g := range m.gateways {
		list = append(list, g)
	}
	gatewayID, ok := smsqueue.ChooseGateway(in.Phone, list, in.Now)
	m.seq++
	m.jobs[idOf(m.seq)] = smsqueue.Job{
		ID: idOf(m.seq), Phone: in.Phone, Message: in.Message, Status: smsqueue.StatusPending,
		GatewayID: gatewayID, CreatedAt: in.Now,
	}
	return auth.ChallengeResult{NoHealthyGateway: !ok}, nil
}

func (m *memDB) VerifyOTP(_ context.Context, in auth.VerifySpec) (auth.SessionUser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := -1
	for i := len(m.otps) - 1; i >= 0; i-- {
		if m.otps[i].phone == in.Phone && !m.otps[i].used {
			idx = i
			break
		}
	}
	if idx < 0 || !m.otps[idx].expires.After(in.Now) {
		if idx >= 0 {
			m.otps[idx].used = true
		}
		return auth.SessionUser{}, auth.ErrInvalidCode
	}
	if in.Match == nil || !in.Match(m.otps[idx].hash) {
		m.otps[idx].attempts++
		if m.otps[idx].attempts >= in.MaxAttempts {
			m.otps[idx].used = true
		}
		return auth.SessionUser{}, auth.ErrInvalidCode
	}
	m.otps[idx].used = true
	user, ok := m.users[in.Phone]
	if !ok {
		m.seq++
		user = auth.SessionUser{ID: "user-" + idOf(m.seq), Phone: in.Phone, Locale: in.Locale, IsNew: true}
		m.users[in.Phone] = user
	}
	if m.blocked[user.ID] {
		return auth.SessionUser{}, auth.ErrBlocked
	}
	m.refresh[in.RefreshHash] = memRefresh{userID: user.ID, device: in.DeviceID, expires: in.RefreshExpiry}
	return user, nil
}

func (m *memDB) RotateRefresh(_ context.Context, oldHash, newHash, deviceID string, exp time.Time) (auth.SessionUser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.refresh[oldHash]
	if !ok {
		return auth.SessionUser{}, auth.ErrUnauthorized
	}
	if row.revoked || !row.expires.After(time.Now()) {
		row.revoked = true
		m.refresh[oldHash] = row
		return auth.SessionUser{}, auth.ErrUnauthorized
	}
	row.revoked = true
	m.refresh[oldHash] = row
	var user auth.SessionUser
	for _, candidate := range m.users {
		if candidate.ID == row.userID {
			user = candidate
		}
	}
	if user.ID == "" || m.blocked[user.ID] {
		return auth.SessionUser{}, auth.ErrUnauthorized
	}
	if deviceID != "" {
		row.device = deviceID
	}
	m.refresh[newHash] = memRefresh{userID: user.ID, device: row.device, expires: exp}
	user.IsNew = false
	return user, nil
}

func (m *memDB) RevokeRefresh(_ context.Context, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row := m.refresh[hash]
	row.revoked = true
	m.refresh[hash] = row
	return nil
}

func (m *memDB) GatewayByHash(_ context.Context, hash string) (smsqueue.Gateway, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.byHash[hash]
	if !ok {
		return smsqueue.Gateway{}, smsqueue.ErrUnauthorized
	}
	g := m.gateways[id]
	if !g.Active {
		return smsqueue.Gateway{}, smsqueue.ErrUnauthorized
	}
	return g, nil
}

func (m *memDB) TouchHeartbeat(_ context.Context, id string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.gateways[id]
	if !ok || !g.Active {
		return smsqueue.ErrUnauthorized
	}
	g.LastHeartbeat = now
	m.gateways[id] = g
	return nil
}

func (m *memDB) ClaimPending(_ context.Context, claimer smsqueue.Gateway, limit int, lease time.Duration, now time.Time) ([]smsqueue.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	healthy := map[string]struct{}{}
	for id, g := range m.gateways {
		if g.Active && !g.LastHeartbeat.IsZero() && now.Sub(g.LastHeartbeat) <= smsqueue.HealthyWindow {
			healthy[id] = struct{}{}
		}
	}
	var out []smsqueue.Job
	for id, job := range m.jobs {
		if len(out) >= limit {
			break
		}
		if !smsqueue.Eligible(job, claimer, healthy) {
			continue
		}
		job.Status = smsqueue.StatusProcessing
		job.GatewayID = claimer.ID
		job.LockedUntil = now.Add(lease)
		job.SendStartedAt = time.Time{}
		m.jobs[id] = job
		out = append(out, job)
	}
	if g, ok := m.gateways[claimer.ID]; ok {
		g.LastHeartbeat = now
		m.gateways[claimer.ID] = g
	}
	return out, nil
}

func (m *memDB) MutateJob(_ context.Context, id string, fn func(*smsqueue.Job) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return smsqueue.ErrNotFound
	}
	if err := fn(&job); err != nil {
		return err
	}
	m.jobs[id] = job
	return nil
}

func (m *memDB) ExpireDue(_ context.Context, maxAge time.Duration, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	changed := 0
	for id, job := range m.jobs {
		next, ok := smsqueue.Expire(job, maxAge, now)
		if !ok {
			continue
		}
		m.jobs[id] = next
		changed++
	}
	return changed, nil
}

func (m *memDB) Cleanup(context.Context, time.Time) error { return nil }

func (m *memDB) CreateGateway(_ context.Context, name, hash string, prefixes []string) (smsqueue.Gateway, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, g := range m.gateways {
		if g.Name == name {
			return smsqueue.Gateway{}, smsqueue.ErrConflict
		}
	}
	m.seq++
	g := smsqueue.Gateway{ID: "gw-" + idOf(m.seq), Name: name, Prefixes: append([]string{}, prefixes...), Active: true, CreatedAt: time.Now()}
	m.gateways[g.ID] = g
	m.byHash[hash] = g.ID
	return g, nil
}

func (m *memDB) ListGateways(context.Context) ([]smsqueue.Gateway, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]smsqueue.Gateway, 0, len(m.gateways))
	for _, g := range m.gateways {
		out = append(out, g)
	}
	return out, nil
}

func (m *memDB) RevokeGateway(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.gateways[id]
	if !ok {
		return smsqueue.ErrNotFound
	}
	g.Active = false
	m.gateways[id] = g
	return nil
}

func (m *memDB) QueueStats(context.Context) (map[string]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]int{}
	for _, job := range m.jobs {
		out[job.Status]++
	}
	return out, nil
}

func (m *memDB) insertJob(job smsqueue.Job) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[job.ID] = job
}

func idOf(n int) string { return strconv.Itoa(n) }
