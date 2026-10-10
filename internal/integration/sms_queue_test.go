package integration

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/shalarasgar/carpool-backend/internal/service"
	"github.com/shalarasgar/carpool-backend/internal/store"
	"github.com/stretchr/testify/require"
)

// TestClaimPendingSkipLockedNeverDuplicates runs many concurrent claimers
// across two gateways and checks every message is handed out exactly once.
func TestClaimPendingSkipLockedNeverDuplicates(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	q := store.New(e.pool)

	const total = 300
	for i := 0; i < total; i++ {
		_, err := q.EnqueueSMS(ctx, store.EnqueueSMSParams{
			Phone:   fmt.Sprintf("+99365%06d", i),
			Message: "Carpool: kod 123456. Kimseyle paylasmanyn.",
		})
		require.NoError(t, err)
	}

	gateways := make([]service.GatewayIdentity, 0, 2)
	for _, name := range []string{"phone-a", "phone-b"} {
		created, err := e.gateway.Create(ctx, name, nil)
		require.NoError(t, err)
		gw, err := e.gateway.Authenticate(ctx, created.APIKey)
		require.NoError(t, err)
		gateways = append(gateways, gw)
	}

	const workers = 8
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		seen    = map[string]string{}
		dups    []string
		claimed int
	)
	start := make(chan struct{})
	for w := 0; w < workers; w++ {
		gw := gateways[w%len(gateways)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for {
				batch, err := e.gateway.ClaimPending(ctx, gw, 7)
				require.NoError(t, err)
				if len(batch) == 0 {
					return
				}
				mu.Lock()
				for _, m := range batch {
					if prev, ok := seen[m.ID]; ok {
						dups = append(dups, fmt.Sprintf("%s claimed by %s and %s", m.ID, prev, gw.Name))
					}
					seen[m.ID] = gw.Name
					claimed++
				}
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	require.Empty(t, dups, "messages claimed twice")
	require.Equal(t, total, claimed, "every message claimed exactly once")
	require.Len(t, seen, total)

	require.Equal(t, total, e.queryInt(`SELECT count(*) FROM sms_queue WHERE status = 'processing' AND locked_until > now() AND gateway_id IS NOT NULL`))
	require.Equal(t, 0, e.queryInt(`SELECT count(*) FROM sms_queue WHERE status = 'pending'`))

	// A second round finds nothing.
	batch, err := e.gateway.ClaimPending(ctx, gateways[0], 50)
	require.NoError(t, err)
	require.Empty(t, batch)
}

func TestGatewayReportRetriesThenFails(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	q := store.New(e.pool)

	id, err := q.EnqueueSMS(ctx, store.EnqueueSMSParams{Phone: "+99365000001", Message: "hello"})
	require.NoError(t, err)
	_, apiKey := e.createGateway("retry-gw")

	for attempt := 1; attempt <= 3; attempt++ {
		pending := e.call(http.MethodGet, "/gateway/v1/pending?limit=5", nil, bearer(apiKey))
		require.Equal(t, http.StatusOK, pending.Status, string(pending.Body))
		msgs := pending.decoded["messages"].([]any)
		require.Len(t, msgs, 1, "attempt %d", attempt)
		require.Equal(t, float64(attempt-1), msgs[0].(map[string]any)["attempts"])

		report := e.call(http.MethodPost, "/gateway/v1/report", map[string]any{
			"id": id.String(), "status": "failed", "error": "radio off",
		}, bearer(apiKey))
		require.Equal(t, http.StatusOK, report.Status, string(report.Body))
		require.Equal(t, float64(attempt), report.decoded["attempts"])
		if attempt < 3 {
			require.Equal(t, "pending", report.str("status"))
		} else {
			require.Equal(t, "failed", report.str("status"))
		}
	}

	require.Equal(t, "failed", e.queryString(`SELECT status::text FROM sms_queue WHERE id = $1`, id))
	require.Equal(t, "radio off", e.queryString(`SELECT last_error FROM sms_queue WHERE id = $1`, id))

	// Reporting again on a failed message is a 404, as is an unknown id.
	again := e.call(http.MethodPost, "/gateway/v1/report", map[string]any{"id": id.String(), "status": "sent"}, bearer(apiKey))
	require.Equal(t, http.StatusNotFound, again.Status)
	require.Equal(t, "SMS_NOT_FOUND", again.Err.Error.Code)

	unknown := e.call(http.MethodPost, "/gateway/v1/report", map[string]any{"id": "00000000-0000-0000-0000-000000000000", "status": "sent"}, bearer(apiKey))
	require.Equal(t, http.StatusNotFound, unknown.Status)

	badStatus := e.call(http.MethodPost, "/gateway/v1/report", map[string]any{"id": id.String(), "status": "maybe"}, bearer(apiKey))
	require.Equal(t, http.StatusUnprocessableEntity, badStatus.Status)
	require.Equal(t, "VALIDATION_ERROR", badStatus.Err.Error.Code)
	require.Equal(t, "oneof", badStatus.Err.Error.Details["status"])
}

func TestGatewayAuthAndAdminAuth(t *testing.T) {
	e := newEnv(t)

	for _, h := range []map[string]string{nil, bearer("gw_not-a-real-key"), {"Authorization": "Basic abc"}} {
		res := e.call(http.MethodGet, "/gateway/v1/pending", nil, h)
		require.Equal(t, http.StatusUnauthorized, res.Status)
		require.Equal(t, "UNAUTHORIZED", res.Err.Error.Code)
	}

	res := e.call(http.MethodPost, "/admin/v1/gateways", map[string]any{"name": "x"}, nil)
	require.Equal(t, http.StatusForbidden, res.Status)
	require.Equal(t, "FORBIDDEN", res.Err.Error.Code)
	res = e.call(http.MethodPost, "/admin/v1/gateways", map[string]any{"name": "x"}, bearer("wrong-admin-token-value"))
	require.Equal(t, http.StatusForbidden, res.Status)

	_, key := e.createGateway("dup")
	dup := e.call(http.MethodPost, "/admin/v1/gateways", map[string]any{"name": "dup"}, bearer(testAdminToken))
	require.Equal(t, http.StatusConflict, dup.Status)
	require.Equal(t, "GATEWAY_EXISTS", dup.Err.Error.Code)

	badPrefix := e.call(http.MethodPost, "/admin/v1/gateways", map[string]any{"name": "p", "operator_prefixes": []string{"6x"}}, bearer(testAdminToken))
	require.Equal(t, http.StatusUnprocessableEntity, badPrefix.Status)

	// The key is stored hashed only.
	require.Equal(t, 0, e.queryInt(`SELECT count(*) FROM sms_gateways WHERE api_key_hash = $1`, key))
	require.Equal(t, 1, e.queryInt(`SELECT count(*) FROM sms_gateways WHERE name = 'dup' AND length(api_key_hash) = 64`))

	// Deactivated gateways cannot authenticate.
	e.exec(`UPDATE sms_gateways SET is_active = false WHERE name = 'dup'`)
	res = e.call(http.MethodPost, "/gateway/v1/heartbeat", nil, bearer(key))
	require.Equal(t, http.StatusUnauthorized, res.Status)
}

func TestGatewayRoutingByOperatorPrefixAndHealth(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	_, keyA := e.createGateway("tmcell-phone", "65", "61")
	idB, keyB := e.createGateway("generic-phone")
	idA := e.queryString(`SELECT id::text FROM sms_gateways WHERE name = 'tmcell-phone'`)

	// No heartbeat yet: messages queue unassigned, readiness reports no gateway.
	ready := e.call(http.MethodGet, "/readyz", nil, nil)
	require.Equal(t, http.StatusOK, ready.Status)
	require.Equal(t, false, ready.decoded["sms_gateway_healthy"])

	require.NoError(t, e.sms.Send(ctx, "+99365111111", "unassigned"))
	require.Equal(t, 1, e.queryInt(`SELECT count(*) FROM sms_queue WHERE phone = '+99365111111' AND gateway_id IS NULL`))

	// Heartbeat both gateways.
	for _, k := range []string{keyA, keyB} {
		hb := e.call(http.MethodPost, "/gateway/v1/heartbeat", nil, bearer(k))
		require.Equal(t, http.StatusOK, hb.Status, string(hb.Body))
	}
	ready = e.call(http.MethodGet, "/readyz", nil, nil)
	require.Equal(t, true, ready.decoded["sms_gateway_healthy"])

	require.NoError(t, e.sms.Send(ctx, "+99365222222", "to tmcell"))
	require.NoError(t, e.sms.Send(ctx, "+99371333333", "to other operator"))

	require.Equal(t, idA, e.queryString(`SELECT gateway_id::text FROM sms_queue WHERE phone = '+99365222222'`))
	require.Equal(t, idB, e.queryString(`SELECT gateway_id::text FROM sms_queue WHERE phone = '+99371333333'`))

	// Gateway B must not receive A's message, but does get the unassigned one.
	batch := e.call(http.MethodGet, "/gateway/v1/pending?limit=10", nil, bearer(keyB))
	require.Equal(t, http.StatusOK, batch.Status)
	phones := phonesOf(batch)
	require.ElementsMatch(t, []string{"+99365111111", "+99371333333"}, phones)

	batch = e.call(http.MethodGet, "/gateway/v1/pending?limit=10", nil, bearer(keyA))
	require.ElementsMatch(t, []string{"+99365222222"}, phonesOf(batch))

	// A goes silent: its pending messages become claimable by anyone.
	require.NoError(t, e.sms.Send(ctx, "+99365444444", "routed to A"))
	e.exec(`UPDATE sms_gateways SET last_heartbeat_at = now() - interval '5 minutes' WHERE id = $1`, idA)
	batch = e.call(http.MethodGet, "/gateway/v1/pending?limit=10", nil, bearer(keyB))
	require.Empty(t, phonesOf(batch), "still assigned to A before the job runs")

	stats, err := e.jobs.RunOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), stats.UnassignedSMS)
	batch = e.call(http.MethodGet, "/gateway/v1/pending?limit=10", nil, bearer(keyB))
	require.ElementsMatch(t, []string{"+99365444444"}, phonesOf(batch))
}

func TestJobsRequeueStuckAndCleanExpired(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	q := store.New(e.pool)

	id, err := q.EnqueueSMS(ctx, store.EnqueueSMSParams{Phone: "+99365000002", Message: "stuck"})
	require.NoError(t, err)
	_, key := e.createGateway("stuck-gw")

	batch := e.call(http.MethodGet, "/gateway/v1/pending", nil, bearer(key))
	require.Len(t, batch.decoded["messages"], 1)
	require.Equal(t, "processing", e.queryString(`SELECT status::text FROM sms_queue WHERE id = $1`, id))

	// Lock still valid: the job leaves it alone.
	stats, err := e.jobs.RunOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(0), stats.RequeuedSMS)

	e.exec(`UPDATE sms_queue SET locked_until = now() - interval '1 second' WHERE id = $1`, id)
	stats, err = e.jobs.RunOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), stats.RequeuedSMS)
	require.Equal(t, "pending", e.queryString(`SELECT status::text FROM sms_queue WHERE id = $1`, id))
	require.Equal(t, 0, e.queryInt(`SELECT count(*) FROM sms_queue WHERE id = $1 AND locked_until IS NOT NULL`, id))

	// Expired OTPs and refresh tokens are removed; fresh ones are kept.
	e.exec(`INSERT INTO otp_codes (phone, code_hash, expires_at) VALUES
		('+99365000003', 'h', now() - interval '2 hours'),
		('+99365000004', 'h', now() - interval '1 minute'),
		('+99365000005', 'h', now() + interval '4 minutes')`)
	e.exec(`INSERT INTO users (phone) VALUES ('+99365000006')`)
	e.exec(`INSERT INTO refresh_tokens (user_id, token_hash, device_id, expires_at, revoked_at)
		SELECT id, 'expired', 'd', now() - interval '1 day', NULL FROM users WHERE phone = '+99365000006'`)
	e.exec(`INSERT INTO refresh_tokens (user_id, token_hash, device_id, expires_at, revoked_at)
		SELECT id, 'old-revoked', 'd', now() + interval '10 days', now() - interval '30 days' FROM users WHERE phone = '+99365000006'`)
	e.exec(`INSERT INTO refresh_tokens (user_id, token_hash, device_id, expires_at, revoked_at)
		SELECT id, 'recent-revoked', 'd', now() + interval '10 days', now() - interval '1 hour' FROM users WHERE phone = '+99365000006'`)
	e.exec(`INSERT INTO refresh_tokens (user_id, token_hash, device_id, expires_at, revoked_at)
		SELECT id, 'live', 'd', now() + interval '10 days', NULL FROM users WHERE phone = '+99365000006'`)

	stats, err = e.jobs.RunOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), stats.DeletedOTPs, "only codes expired longer than the retention go")
	require.Equal(t, int64(2), stats.DeletedRefresh)
	require.Equal(t, 2, e.queryInt(`SELECT count(*) FROM otp_codes`))
	require.Equal(t, 2, e.queryInt(`SELECT count(*) FROM refresh_tokens`))
	require.Equal(t, 1, e.queryInt(`SELECT count(*) FROM refresh_tokens WHERE token_hash = 'live'`))
}

func phonesOf(res response) []string {
	msgs, _ := res.decoded["messages"].([]any)
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.(map[string]any)["phone"].(string))
	}
	return out
}
