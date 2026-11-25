package ride

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestUserQueryNormalize(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		text, digits string
		letters      bool
	}{
		{"", "", false},
		{"  Merdan  ", "", true},
		{"+993 65 12", "9936512", false},
		{"65-1", "651", false},
		{"12", "", false}, // too few digits to search phones
		{"Aýna 12345", "12345", true},
	} {
		q := UserQuery{Text: tc.text}
		if err := q.Normalize(); err != nil {
			t.Fatalf("%q: %v", tc.text, err)
		}
		if q.Text != strings.TrimSpace(tc.text) || q.Digits != tc.digits || q.HasLetters() != tc.letters {
			t.Fatalf("%q -> text %q digits %q letters %v", tc.text, q.Text, q.Digits, q.HasLetters())
		}
	}

	var field *FieldError
	q := UserQuery{Text: strings.Repeat("ý", MaxUserQuery+1)}
	if err := q.Normalize(); !errors.As(err, &field) || field.Field != "q" {
		t.Fatalf("long query err = %v", err)
	}
	q = UserQuery{Text: strings.Repeat("ý", MaxUserQuery)}
	if err := q.Normalize(); err != nil {
		t.Fatalf("a query of exactly the limit in runes: %v", err)
	}
	q = UserQuery{State: "banned"}
	if err := q.Normalize(); !errors.As(err, &field) || field.Field != "status" {
		t.Fatalf("unknown state err = %v", err)
	}
	for _, s := range []UserState{UserStateLive, UserStateActive, UserStateBlocked, UserStateDeleted} {
		q = UserQuery{State: s}
		if err := q.Normalize(); err != nil {
			t.Fatalf("state %q: %v", s, err)
		}
	}
}

func TestReportFilterValidate(t *testing.T) {
	t.Parallel()
	ok := ReportFilter{Status: ReportResolved, Reason: ReasonNoShow,
		ReportedUserID: "00000000-0000-4000-8000-000000000001", ReporterID: "00000000-0000-4000-8000-000000000002"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (ReportFilter{}).Validate(); err != nil {
		t.Fatalf("empty filter: %v", err)
	}
	if err := (ReportFilter{Reason: "rude"}).Validate(); !errors.Is(err, ErrValidation) {
		t.Fatalf("unknown reason err = %v", err)
	}
}

func TestDayStartUsesTheLocalCalendarDay(t *testing.T) {
	t.Parallel()
	ashgabat := time.FixedZone("Asia/Ashgabat", 5*3600)
	// 21:30 UTC on the 1st is already 02:30 on the 2nd in Ashgabat.
	now := time.Date(2026, 3, 1, 21, 30, 0, 0, time.UTC)
	got := DayStart(now, ashgabat)
	want := time.Date(2026, 3, 2, 0, 0, 0, 0, ashgabat)
	if !got.Equal(want) {
		t.Fatalf("DayStart = %v, want %v", got, want)
	}
	if got := DayStart(now, time.UTC); !got.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("DayStart UTC = %v", got)
	}
}
