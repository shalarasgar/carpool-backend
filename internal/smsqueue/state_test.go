package smsqueue

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestSMSTextIsSingleSegmentAndHasNoExtraCode(t *testing.T) {
	t.Parallel()
	for _, locale := range []string{"tk", "ru", "en", ""} {
		text := SMSText(locale, "BlaBlaCar TM", "123456")
		if n := utf8.RuneCountInString(text); n > 70 {
			t.Fatalf("locale %s length %d: %s", locale, n, text)
		}
		if strings.Count(text, "123456") != 1 {
			t.Fatalf("locale %s text = %s", locale, text)
		}
	}
	if SMSText("tk", "carpool", "123456") != "carpool: kod 123456. Kody hiçkim bilen paýlaşmaň!" {
		t.Fatalf("turkmen template changed")
	}
	if got := SMSText("tk", "Ýolugra", "123456"); got != "Ýolugra: kod 123456. Kody hiçkim bilen paýlaşmaň!" {
		t.Fatalf("turkmen app name was altered: %s", got)
	}
	if n := utf8.RuneCountInString(SMSText("tk", "Ýolugra Türkmenistan Ýollary", "123456")); n > 70 {
		t.Fatalf("long app name overflows one segment: %d", n)
	}
}

func TestChooseGatewayRoutesByPrefixAndFallsBack(t *testing.T) {
	t.Parallel()
	now := time.Now()
	gateways := []Gateway{
		{ID: "any", Active: true, LastHeartbeat: now, Prefixes: nil},
		{ID: "tmcell", Active: true, LastHeartbeat: now, Prefixes: []string{"61", "62"}},
		{ID: "stale", Active: true, LastHeartbeat: now.Add(-time.Minute), Prefixes: []string{"71"}},
	}
	id, ok := ChooseGateway("+99361123456", gateways, now)
	if !ok || id != "tmcell" {
		t.Fatalf("prefix route = %s ok=%v", id, ok)
	}
	id, ok = ChooseGateway("+99371123456", gateways, now)
	if !ok || id != "any" {
		t.Fatalf("fallback = %s ok=%v", id, ok)
	}
	_, ok = ChooseGateway("+99371123456", []Gateway{gateways[2]}, now)
	if ok {
		t.Fatal("stale gateway was selected")
	}
}

func TestEligibleSkipsPinnedHealthyGateway(t *testing.T) {
	t.Parallel()
	job := Job{Status: StatusPending, Phone: "+99361123456", GatewayID: "tmcell"}
	other := Gateway{ID: "other", Active: true}
	if Eligible(job, other, map[string]struct{}{"tmcell": {}}) {
		t.Fatal("other gateway claimed a pinned job")
	}
	if !Eligible(job, other, map[string]struct{}{}) {
		t.Fatal("stale pin should be claimable")
	}
	owner := Gateway{ID: "tmcell", Active: true, Prefixes: []string{"61"}}
	if !Eligible(job, owner, map[string]struct{}{"tmcell": {}}) {
		t.Fatal("owner should claim its prefix")
	}
	owner.Prefixes = []string{"71"}
	if Eligible(job, owner, nil) {
		t.Fatal("prefix mismatch should not match")
	}
}

func TestFailedBeforeSendRequeuesUntilMax(t *testing.T) {
	t.Parallel()
	job := Job{ID: "1", Status: StatusProcessing, GatewayID: "g", Attempts: 0}
	next, err := ApplyReport(job, Report{Status: ReportFailed, Error: "radio_off"}, 3, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if next.Status != StatusPending || next.GatewayID != "" || next.Attempts != 1 {
		t.Fatalf("requeue = %+v", next)
	}
	job.Attempts = 2
	next, err = ApplyReport(job, Report{Status: ReportFailed, Error: "radio_off"}, 3, time.Now())
	if err != nil || next.Status != StatusFailed || next.Attempts != 3 {
		t.Fatalf("terminal = %+v err=%v", next, err)
	}
}

func TestSendStartedDoesNotRequeueUnlessNotSubmitted(t *testing.T) {
	t.Parallel()
	now := time.Now()
	job := Job{Status: StatusProcessing, GatewayID: "g", SendStartedAt: now}
	next, err := ApplyReport(job, Report{Status: ReportFailed, Error: "generic"}, 3, now)
	if err != nil || next.Status != StatusFailed || next.GatewayID != "g" {
		t.Fatalf("terminal = %+v err=%v", next, err)
	}
	again, err := ApplyReport(job, Report{Status: ReportFailed, NotSubmitted: true, Error: "not_submitted"}, 3, now)
	if err != nil || again.Status != StatusPending || again.SendStartedAt.IsZero() == false {
		t.Fatalf("not submitted = %+v err=%v", again, err)
	}
}

func TestUnknownDoesNotReturnToPendingAndLateSentReconciles(t *testing.T) {
	t.Parallel()
	now := time.Now()
	job := Job{Status: StatusProcessing, GatewayID: "g", SendStartedAt: now}
	unknown, err := ApplyReport(job, Report{Status: ReportUnknown, Error: "123456 timeout +99361111111"}, 3, now)
	if err != nil {
		t.Fatal(err)
	}
	if unknown.Status != StatusFailed || unknown.DeliveryStatus != DeliveryUnknown {
		t.Fatalf("unknown = %+v", unknown)
	}
	if strings.Contains(unknown.LastError, "123456") || strings.Contains(unknown.LastError, "99361111111") {
		t.Fatalf("error leaked secrets: %s", unknown.LastError)
	}
	sent, err := ApplyReport(unknown, Report{Status: ReportSent}, 3, now)
	if err != nil || sent.Status != StatusSent || sent.DeliveryStatus != "" {
		t.Fatalf("late sent = %+v err=%v", sent, err)
	}
	delivered, err := ApplyReport(sent, Report{Status: ReportDelivered}, 3, now)
	if err != nil || delivered.DeliveryStatus != DeliveryConfirmed || delivered.Status != StatusSent {
		t.Fatalf("delivered = %+v err=%v", delivered, err)
	}
}

func TestSentIsNotCarrierDeliveryAndIsIdempotent(t *testing.T) {
	t.Parallel()
	now := time.Now()
	job := Job{Status: StatusProcessing, GatewayID: "g"}
	sent, err := ApplyReport(job, Report{Status: ReportSent}, 3, now)
	if err != nil || sent.Status != StatusSent || sent.DeliveryStatus != "" || sent.TelephonyAcceptedAt.IsZero() {
		t.Fatalf("sent = %+v err=%v", sent, err)
	}
	again, err := ApplyReport(sent, Report{Status: ReportSent}, 3, now)
	if err != nil || !again.SentAt.Equal(sent.SentAt) {
		t.Fatalf("idempotent sent = %+v err=%v", again, err)
	}
	if _, err := ApplyReport(sent, Report{Status: ReportFailed}, 3, now); err != ErrConflict {
		t.Fatalf("failed after sent err = %v", err)
	}
}

func TestExpireRequeuesOnlyBeforeSendAndFailsOldOTP(t *testing.T) {
	t.Parallel()
	now := time.Now()
	leased := Job{
		Status:      StatusProcessing,
		GatewayID:   "g",
		LockedUntil: now.Add(-time.Second),
		CreatedAt:   now.Add(-time.Minute),
	}
	released, ok := Expire(leased, 5*time.Minute, now)
	if !ok || released.Status != StatusPending || released.GatewayID != "" {
		t.Fatalf("release = %+v ok=%v", released, ok)
	}
	leased.SendStartedAt = now.Add(-time.Minute)
	unknown, ok := Expire(leased, 5*time.Minute, now)
	if !ok || unknown.Status != StatusFailed || unknown.DeliveryStatus != DeliveryUnknown {
		t.Fatalf("unknown expire = %+v ok=%v", unknown, ok)
	}
	old := Job{Status: StatusPending, CreatedAt: now.Add(-6 * time.Minute)}
	expired, ok := Expire(old, 5*time.Minute, now)
	if !ok || expired.Status != StatusFailed || expired.LastError != "expired" {
		t.Fatalf("expired = %+v ok=%v", expired, ok)
	}
}

func TestMarkSendingIsIdempotentUntilSent(t *testing.T) {
	t.Parallel()
	now := time.Now()
	job := Job{Status: StatusProcessing, GatewayID: "g"}
	marked, err := MarkSending(job, time.Minute, now)
	if err != nil || marked.SendStartedAt.IsZero() || !marked.LockedUntil.Equal(now.Add(time.Minute)) {
		t.Fatalf("mark = %+v err=%v", marked, err)
	}
	again, err := MarkSending(marked, time.Minute, now.Add(time.Second))
	if err != nil || !again.SendStartedAt.Equal(marked.SendStartedAt) {
		t.Fatalf("second mark = %+v err=%v", again, err)
	}
	marked.Status = StatusSent
	if _, err := MarkSending(marked, time.Minute, now); err != ErrAlreadySent {
		t.Fatalf("err = %v", err)
	}
}
