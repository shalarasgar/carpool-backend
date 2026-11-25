package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/auth"
	"github.com/shalarasgar/carpool-backend/internal/ride"
)

const (
	userA = "11111111-1111-4111-8111-111111111111"
	userB = "22222222-2222-4222-8222-222222222222"
)

// fakeAccounts implements the two account methods of ProfileStore. The embedded nil
// interface makes any other call panic, which no test here should reach.
type fakeAccounts struct {
	ProfileStore
	byPhone   map[string]string
	files     []string
	deleteErr error
	deleted   []string
}

func (f *fakeAccounts) DeleteAccount(_ context.Context, userID string, _ time.Time) ([]string, error) {
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	f.deleted = append(f.deleted, userID)
	return f.files, nil
}

func (f *fakeAccounts) UserIDByPhone(_ context.Context, phone string) (string, error) {
	id, ok := f.byPhone[phone]
	if !ok {
		return "", ride.ErrNotFound
	}
	return id, nil
}

type fakeMedia struct{ removed []string }

func (m *fakeMedia) Save(context.Context, string, []byte) (string, error) { return "", nil }

func (m *fakeMedia) Delete(_ context.Context, url string) error {
	m.removed = append(m.removed, url)
	return nil
}

type fakePhones struct{}

func (fakePhones) NormalizePhone(raw string) (string, error) {
	return auth.NormalizePhone(raw, []string{"6", "7"})
}

func (fakePhones) RequestOTP(context.Context, string, string, string, string) (string, error) {
	return "", nil
}

func (fakePhones) PhoneCheck(string, string) (auth.VerifySpec, error) { return auth.VerifySpec{}, nil }

func TestDeleteAccountRemovesPhotoFilesAfterTheDatabaseChange(t *testing.T) {
	t.Parallel()
	store := &fakeAccounts{files: []string{"/media/vehicle_1.jpg", "", "/media/user_1.jpg"}}
	storage := &fakeMedia{}
	profiles := NewProfiles(store, storage, fakePhones{}, discardLogger())

	if err := profiles.DeleteAccount(context.Background(), userA); err != nil {
		t.Fatal(err)
	}
	if len(store.deleted) != 1 || store.deleted[0] != userA {
		t.Fatalf("deleted = %v", store.deleted)
	}
	if len(storage.removed) != 2 || storage.removed[0] != "/media/vehicle_1.jpg" || storage.removed[1] != "/media/user_1.jpg" {
		t.Fatalf("removed files = %v", storage.removed)
	}
}

func TestDeleteAccountKeepsFilesWhenTheDeletionIsRefused(t *testing.T) {
	t.Parallel()
	store := &fakeAccounts{files: []string{"/media/user_1.jpg"}, deleteErr: ride.ErrTripInProgress}
	storage := &fakeMedia{}
	profiles := NewProfiles(store, storage, fakePhones{}, discardLogger())

	if err := profiles.DeleteAccount(context.Background(), userA); !errors.Is(err, ride.ErrTripInProgress) {
		t.Fatalf("err = %v", err)
	}
	if len(storage.removed) != 0 {
		t.Fatalf("files were removed although the account stays: %v", storage.removed)
	}
}

func TestDeleteAccountByPhone(t *testing.T) {
	t.Parallel()
	store := &fakeAccounts{byPhone: map[string]string{"+99361234567": userA}}
	profiles := NewProfiles(store, &fakeMedia{}, fakePhones{}, discardLogger())
	ctx := context.Background()

	// The operator may type the number in any accepted form.
	if err := profiles.DeleteAccountByPhone(ctx, "993 61 23-45-67"); err != nil {
		t.Fatal(err)
	}
	if len(store.deleted) != 1 || store.deleted[0] != userA {
		t.Fatalf("deleted = %v", store.deleted)
	}
	if err := profiles.DeleteAccountByPhone(ctx, "+99362222222"); !errors.Is(err, ride.ErrNotFound) {
		t.Fatalf("unknown number err = %v", err)
	}
	if err := profiles.DeleteAccountByPhone(ctx, "12345"); !errors.Is(err, auth.ErrInvalidPhone) {
		t.Fatalf("malformed number err = %v", err)
	}
	if len(store.deleted) != 1 {
		t.Fatalf("an account was deleted for a bad number: %v", store.deleted)
	}
}

// fakeModeration records what reaches the store.
type fakeModeration struct {
	ModerationStore
	blocks  [][2]string
	reports []ride.ReportInput
	filters []ride.ReportFilter
	limits  []int
}

func (f *fakeModeration) BlockUser(_ context.Context, blocker, blocked string) error {
	f.blocks = append(f.blocks, [2]string{blocker, blocked})
	return nil
}

func (f *fakeModeration) CreateReport(_ context.Context, in ride.ReportInput) (string, error) {
	f.reports = append(f.reports, in)
	return "report-1", nil
}

func (f *fakeModeration) Reports(_ context.Context, filter ride.ReportFilter, _, limit int) ([]ride.Report, bool, error) {
	f.filters, f.limits = append(f.filters, filter), append(f.limits, limit)
	return nil, false, nil
}

func TestBlockRejectsSelfAndMalformedIDs(t *testing.T) {
	t.Parallel()
	store := &fakeModeration{}
	m := NewModeration(store)
	ctx := context.Background()

	var field *ride.FieldError
	if err := m.Block(ctx, userA, userA); !errors.As(err, &field) || field.Reason != "self" {
		t.Fatalf("self block err = %v", err)
	}
	if err := m.Block(ctx, userA, "not-a-uuid"); !errors.Is(err, ride.ErrNotFound) {
		t.Fatalf("malformed id err = %v", err)
	}
	if len(store.blocks) != 0 {
		t.Fatalf("an invalid block reached the store: %v", store.blocks)
	}
	if err := m.Block(ctx, userA, userB); err != nil {
		t.Fatal(err)
	}
	if len(store.blocks) != 1 || store.blocks[0] != [2]string{userA, userB} {
		t.Fatalf("blocks = %v", store.blocks)
	}
}

func TestReportIsNormalizedBeforeItIsStored(t *testing.T) {
	t.Parallel()
	store := &fakeModeration{}
	m := NewModeration(store)
	ctx := context.Background()

	if _, err := m.Report(ctx, ride.ReportInput{ReporterID: userA, ReportedUserID: userB, Reason: "made_up"}); !errors.Is(err, ride.ErrValidation) {
		t.Fatalf("unknown reason err = %v", err)
	}
	if len(store.reports) != 0 {
		t.Fatal("an invalid report reached the store")
	}
	id, err := m.Report(ctx, ride.ReportInput{ReporterID: userA, ReportedUserID: userB, Reason: ride.ReasonAbuse, Details: "  rude  "})
	if err != nil || id != "report-1" {
		t.Fatalf("report id = %q err=%v", id, err)
	}
	if got := store.reports[0].Details; got != "rude" {
		t.Fatalf("stored details = %q", got)
	}
}

func TestReportsListValidatesFiltersAndBoundsThePage(t *testing.T) {
	t.Parallel()
	store := &fakeModeration{}
	m := NewModeration(store)
	ctx := context.Background()

	var field *ride.FieldError
	for _, tc := range []struct {
		filter ride.ReportFilter
		field  string
	}{
		{ride.ReportFilter{Status: "closed"}, "status"},
		{ride.ReportFilter{Reason: "rude"}, "reason"},
		{ride.ReportFilter{ReportedUserID: "not-a-uuid"}, "reported_user_id"},
		{ride.ReportFilter{ReporterID: "not-a-uuid"}, "reporter_id"},
	} {
		if _, _, err := m.Reports(ctx, tc.filter, 1, 20); !errors.As(err, &field) || field.Field != tc.field {
			t.Fatalf("filter %+v err = %v, want field %s", tc.filter, err, tc.field)
		}
	}
	if len(store.filters) != 0 {
		t.Fatalf("an invalid filter reached the store: %v", store.filters)
	}

	if _, _, err := m.Reports(ctx, ride.ReportFilter{}, 0, 5000); err != nil {
		t.Fatal(err)
	}
	full := ride.ReportFilter{Status: ride.ReportOpen, Reason: ride.ReasonFraud,
		ReportedUserID: "00000000-0000-4000-8000-000000000001", ReporterID: "00000000-0000-4000-8000-000000000002"}
	if _, _, err := m.Reports(ctx, full, 1, 10); err != nil {
		t.Fatal(err)
	}
	if len(store.filters) != 2 || store.filters[0] != (ride.ReportFilter{}) || store.filters[1] != full {
		t.Fatalf("filters = %v", store.filters)
	}
	if store.limits[0] != 20 || store.limits[1] != 10 {
		t.Fatalf("limits = %v", store.limits)
	}
}
