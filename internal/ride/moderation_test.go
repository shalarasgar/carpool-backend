package ride

import (
	"errors"
	"strings"
	"testing"
)

func TestReportInputNormalize(t *testing.T) {
	t.Parallel()
	const (
		reporter = "11111111-1111-4111-8111-111111111111"
		reported = "22222222-2222-4222-8222-222222222222"
		trip     = "33333333-3333-4333-8333-333333333333"
	)
	valid := func() ReportInput {
		return ReportInput{ReporterID: reporter, ReportedUserID: reported, Reason: ReasonFraud}
	}
	cases := []struct {
		name   string
		change func(*ReportInput)
		field  string // empty means the input is valid
		reason string
	}{
		{"minimal", func(*ReportInput) {}, "", ""},
		{"with trip and details", func(in *ReportInput) { in.TripID, in.Details = trip, "asked for money upfront" }, "", ""},
		{"other with details", func(in *ReportInput) { in.Reason, in.Details = ReasonOther, "something else" }, "", ""},
		{"details at the limit", func(in *ReportInput) { in.Details = strings.Repeat("ý", MaxReportDetails) }, "", ""},
		{"missing reported user", func(in *ReportInput) { in.ReportedUserID = "" }, "reported_user_id", "required"},
		{"malformed reported user", func(in *ReportInput) { in.ReportedUserID = "42" }, "reported_user_id", "required"},
		{"reports oneself", func(in *ReportInput) { in.ReportedUserID = reporter }, "reported_user_id", "self"},
		{"malformed trip", func(in *ReportInput) { in.TripID = "trip" }, "trip_id", "not_found"},
		{"unknown reason", func(in *ReportInput) { in.Reason = "boring" }, "reason", "unknown"},
		{"empty reason", func(in *ReportInput) { in.Reason = "" }, "reason", "unknown"},
		{"details too long", func(in *ReportInput) { in.Details = strings.Repeat("a", MaxReportDetails+1) }, "details", "length"},
		{"other without details", func(in *ReportInput) { in.Reason, in.Details = ReasonOther, "   " }, "details", "required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := valid()
			tc.change(&in)
			err := in.Normalize()
			if tc.field == "" {
				if err != nil {
					t.Fatalf("Normalize() = %v, want nil", err)
				}
				return
			}
			var fe *FieldError
			if !errors.As(err, &fe) || fe.Field != tc.field || fe.Reason != tc.reason {
				t.Fatalf("Normalize() = %v, want %s: %s", err, tc.field, tc.reason)
			}
			if !errors.Is(err, ErrValidation) {
				t.Fatal("a field error must match ErrValidation")
			}
		})
	}
}

func TestReportInputNormalizeTrimsDetails(t *testing.T) {
	t.Parallel()
	in := ReportInput{
		ReporterID:     "11111111-1111-4111-8111-111111111111",
		ReportedUserID: "22222222-2222-4222-8222-222222222222",
		Reason:         ReasonSpam,
		Details:        "\n  posts the same trip ten times \t",
	}
	if err := in.Normalize(); err != nil {
		t.Fatal(err)
	}
	if in.Details != "posts the same trip ten times" {
		t.Fatalf("details = %q", in.Details)
	}
}

func TestReportReasonsMatchTheDatabaseConstraint(t *testing.T) {
	t.Parallel()
	// The same list is the CHECK constraint reports_reason_known in migration 000008.
	want := []ReportReason{"spam", "fraud", "abuse", "no_show", "unsafe", "fake_profile", "inappropriate_content", "other"}
	for _, r := range want {
		if !r.Valid() {
			t.Fatalf("reason %q is in the database constraint but not valid in code", r)
		}
	}
	if ReportReason("").Valid() || ReportStatus("").Valid() || !ReportOpen.Valid() || !ReportResolved.Valid() {
		t.Fatal("empty values must be invalid and both statuses valid")
	}
}
