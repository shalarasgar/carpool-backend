// Package staff holds the admin panel's accounts: roles, credentials rules, and the
// actions recorded in the audit log. It has no database or HTTP code.
package staff

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Role decides what a staff member may do.
type Role string

const (
	// RoleAdmin can do everything, including managing staff, gateways and account deletion.
	RoleAdmin Role = "admin"
	// RoleModerator handles reports and users: block, unblock, read cards and analytics.
	RoleModerator Role = "moderator"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r == RoleAdmin || r == RoleModerator }

var (
	// ErrInvalidCredentials covers an unknown login, a wrong password and a disabled
	// account alike, so the answer does not reveal which logins exist.
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUnauthorized       = errors.New("unauthorized")
	ErrForbidden          = errors.New("forbidden")
	ErrNotFound           = errors.New("staff member not found")
	ErrLoginTaken         = errors.New("login taken")
	// ErrLastAdmin refuses a change that would leave no active admin.
	ErrLastAdmin = errors.New("last active admin")
	// ErrSelf refuses changing one's own role or disabling oneself.
	ErrSelf = errors.New("cannot change own role or access")
)

// FieldError is a rejected input field. errors.Is(err, ErrValidation) is true.
type FieldError struct {
	Field  string
	Reason string
}

// ErrValidation matches every FieldError.
var ErrValidation = errors.New("validation")

func (e *FieldError) Error() string        { return e.Field + ": " + e.Reason }
func (e *FieldError) Is(target error) bool { return target == ErrValidation }

func invalid(field, reason string) error { return &FieldError{Field: field, Reason: reason} }

// Password limits. bcrypt reads at most 72 bytes.
const (
	MinPassword = 10
	MaxPassword = 72
	MaxName     = 80
)

var loginPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,31}$`)

// NormalizeLogin lowercases and trims a login.
func NormalizeLogin(login string) string { return strings.ToLower(strings.TrimSpace(login)) }

// ValidateLogin checks a normalized login: 3–32 of a–z, 0–9, dot, dash, underscore,
// starting with a letter or digit.
func ValidateLogin(login string) error {
	if !loginPattern.MatchString(login) {
		return invalid("login", "format")
	}
	return nil
}

// ValidateName checks a trimmed display name.
func ValidateName(name string) error {
	if name == "" || utf8.RuneCountInString(name) > MaxName {
		return invalid("name", "length")
	}
	return nil
}

// ValidatePassword checks length only: long passphrases beat composition rules.
func ValidatePassword(password string) error {
	if utf8.RuneCountInString(password) < MinPassword {
		return invalid("password", "too_short")
	}
	if len(password) > MaxPassword {
		return invalid("password", "too_long")
	}
	return nil
}

// Member is a staff account as the panel shows it. The password hash never leaves the
// store.
type Member struct {
	ID          string
	Login       string
	Name        string
	Role        Role
	CreatedAt   time.Time
	LastLoginAt *time.Time
	DisabledAt  *time.Time
}

// Actor is whoever makes an admin request: a signed-in member, or the static
// ADMIN_TOKEN (ID empty).
type Actor struct {
	ID   string
	Name string
	Role Role
}

// TokenActor is the static ADMIN_TOKEN: an admin without an account.
var TokenActor = Actor{Name: "ADMIN_TOKEN", Role: RoleAdmin}

// IsAdmin reports whether the actor has the admin role.
func (a Actor) IsAdmin() bool { return a.Role == RoleAdmin }

// Audit actions.
const (
	ActionLogin           = "staff.login"
	ActionReportResolve   = "report.resolve"
	ActionUserBlock       = "user.block"
	ActionUserUnblock     = "user.unblock"
	ActionUserDelete      = "user.delete"
	ActionGatewayCreate   = "gateway.create"
	ActionGatewayRevoke   = "gateway.revoke"
	ActionStaffCreate     = "staff.create"
	ActionStaffUpdate     = "staff.update"
	ActionStaffPassword   = "staff.password_reset"
	ActionOwnPassword     = "staff.password_change"
	ActionStaffDisable    = "staff.disable"
	ActionStaffEnable     = "staff.enable"
	ActionStaffRoleChange = "staff.role"
)

// Entry is one line of the audit log.
type Entry struct {
	ID           int64
	StaffID      string
	StaffName    string
	Action       string
	TargetUserID string
	Target       string
	Note         string
	CreatedAt    time.Time
}

// AuditFilter narrows the log. Empty fields do not filter.
type AuditFilter struct {
	StaffID      string
	TargetUserID string
	Action       string
}

// Update changes an account. Nil fields stay as they are.
type Update struct {
	Name     *string
	Role     *Role
	Disabled *bool
}
