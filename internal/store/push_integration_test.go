package store

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/ride"
)

func TestPushOutboxClaimsEachNotificationOnce(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	driver, _ := mkUser(t, db, "Driver")
	passenger, _ := mkUser(t, db, "Pasha")
	other, _ := mkUser(t, db, "Other")
	mkVehicle(t, db, driver, 4)
	tripID := mkTrip(t, db, driver, tripOpts{})

	token := fmt.Sprintf("tok-%d", rand.Int64())
	now := time.Now()
	// The token was first registered by another account on the same device; the driver
	// signing in there takes it over.
	if err := db.SavePushToken(ctx, other, token, "android", now); err != nil {
		t.Fatal(err)
	}
	if err := db.SavePushToken(ctx, driver, token, "android", now); err != nil {
		t.Fatal(err)
	}

	mustBook(t, db, tripID, passenger, 0, 2, 1) // the driver gets booking_requested

	find := func(jobs []ride.PushJob) *ride.PushJob {
		for i := range jobs {
			if jobs[i].TripID == tripID && jobs[i].Type == ride.NoteBookingRequested {
				return &jobs[i]
			}
		}
		return nil
	}
	jobs, err := db.ClaimPushes(ctx, time.Now(), 1000)
	if err != nil {
		t.Fatal(err)
	}
	j := find(jobs)
	if j == nil {
		t.Fatal("the new notification was not claimed")
	}
	if len(j.Tokens) != 1 || j.Tokens[0] != token {
		t.Fatalf("tokens = %v, want the driver's token only", j.Tokens)
	}
	if j.Actor == nil || j.Actor.FirstName != "Pasha" || j.From == nil || j.From.NameTK == "" || j.To == nil || j.Locale == "" {
		t.Fatalf("job lacks text details: %+v", j)
	}

	again, err := db.ClaimPushes(ctx, time.Now(), 1000)
	if err != nil {
		t.Fatal(err)
	}
	if find(again) != nil {
		t.Fatal("a notification was claimed twice")
	}

	// Another user cannot remove the driver's token; FCM cleanup can.
	if err := db.DeletePushToken(ctx, other, token); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.pool.QueryRow(ctx, `SELECT COUNT(*) FROM push_tokens WHERE token = $1`, token).Scan(&n); err != nil || n != 1 {
		t.Fatalf("token count after foreign delete = %d, %v", n, err)
	}
	if err := db.DeletePushTokens(ctx, []string{token}); err != nil {
		t.Fatal(err)
	}
	if err := db.pool.QueryRow(ctx, `SELECT COUNT(*) FROM push_tokens WHERE token = $1`, token).Scan(&n); err != nil || n != 0 {
		t.Fatalf("token count after cleanup = %d, %v", n, err)
	}
	if err := db.PrunePushTokens(ctx, time.Now().Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
}
