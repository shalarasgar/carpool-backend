package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/auth"
	"github.com/shalarasgar/carpool-backend/internal/smsqueue"
)

// AuthStore is the persistence used by OTP login.
type AuthStore interface {
	CreateChallenge(ctx context.Context, in auth.Challenge) (auth.ChallengeResult, error)
	VerifyOTP(ctx context.Context, in auth.VerifySpec) (auth.SessionUser, error)
	RotateRefresh(ctx context.Context, oldHash, newHash, deviceID string, exp time.Time) (auth.SessionUser, error)
	RevokeRefresh(ctx context.Context, hash string) error
}

// AuthConfig is the OTP and token policy. Secrets stay in this process.
type AuthConfig struct {
	AppName         string
	OTPSecret       string
	JWTSecret       string
	Prefixes        []string
	OTPTTL          time.Duration
	MaxAttempts     int
	PhoneInterval   time.Duration
	PhoneDailyMax   int
	IPHourlyMax     int
	DeviceHourlyMax int
	AccessTTL       time.Duration
	RefreshTTL      time.Duration
	ReturnDevCode   bool
	// TestPhones are normalized numbers that skip every OTP rate limit.
	TestPhones map[string]bool
}

// Session is the result of a successful OTP check or refresh.
type Session struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
	User         auth.SessionUser
}

// Auth issues and checks phone OTPs. The SMS body is queued; this type does not send it.
type Auth struct {
	store  AuthStore
	cfg    AuthConfig
	logger *slog.Logger
	now    func() time.Time
}

// NewAuth returns the OTP service. logger may be nil.
func NewAuth(store AuthStore, cfg AuthConfig, logger *slog.Logger) *Auth {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.AppName == "" {
		cfg.AppName = "carpool"
	}
	return &Auth{store: store, cfg: cfg, logger: logger, now: time.Now}
}

// RequestOTP validates the phone, stores only an HMAC, and enqueues the SMS text.
// The returned code is non-empty only when ReturnDevCode is set. Callers must not log it.
func (a *Auth) RequestOTP(ctx context.Context, phoneRaw, ip, deviceID, locale string) (string, error) {
	phone, err := auth.NormalizePhone(phoneRaw, a.cfg.Prefixes)
	if err != nil {
		return "", err
	}
	if deviceID != "" && !auth.ValidDeviceID(deviceID) {
		return "", auth.ErrValidation
	}
	code, err := auth.RandomCode()
	if err != nil {
		return "", err
	}
	now := a.now()
	result, err := a.store.CreateChallenge(ctx, auth.Challenge{
		Phone:           phone,
		CodeHash:        auth.HashOTP(a.cfg.OTPSecret, code),
		IP:              ip,
		DeviceID:        deviceID,
		Message:         smsqueue.SMSText(locale, a.cfg.AppName, code),
		ExpiresAt:       now.Add(a.cfg.OTPTTL),
		Now:             now,
		PhoneInterval:   a.cfg.PhoneInterval,
		PhoneDailyMax:   a.cfg.PhoneDailyMax,
		IPHourlyMax:     a.cfg.IPHourlyMax,
		DeviceHourlyMax: a.cfg.DeviceHourlyMax,
		SkipLimits:      a.cfg.TestPhones[phone],
	})
	if err != nil {
		return "", err
	}
	if result.NoHealthyGateway {
		a.logger.Warn("otp queued without a healthy sms gateway", "phone", smsqueue.MaskPhone(phone))
	}
	if a.cfg.ReturnDevCode {
		return code, nil
	}
	return "", nil
}

// VerifyOTP checks the code and returns a new session. Wrong, expired, and missing codes
// produce the same error.
func (a *Auth) VerifyOTP(ctx context.Context, phoneRaw, code, deviceID, locale string) (Session, error) {
	phone, err := auth.NormalizePhone(phoneRaw, a.cfg.Prefixes)
	if err != nil {
		return Session{}, err
	}
	if !auth.ValidDeviceID(deviceID) || len(code) != 6 {
		return Session{}, auth.ErrInvalidCode
	}
	raw, hash, err := auth.NewToken("")
	if err != nil {
		return Session{}, err
	}
	now := a.now()
	user, err := a.store.VerifyOTP(ctx, auth.VerifySpec{
		Phone:         phone,
		DeviceID:      deviceID,
		Locale:        locale,
		Now:           now,
		MaxAttempts:   a.cfg.MaxAttempts,
		RefreshHash:   hash,
		RefreshExpiry: now.Add(a.cfg.RefreshTTL),
		Match:         func(stored string) bool { return auth.OTPEqual(a.cfg.OTPSecret, code, stored) },
	})
	if err != nil {
		return Session{}, err
	}
	return a.session(user, raw, now)
}

// PhoneCheck builds the OTP check for a code sent to phoneRaw, for flows other than login
// (a phone number change). The HMAC secret stays inside the returned Match function.
func (a *Auth) PhoneCheck(phoneRaw, code string) (auth.VerifySpec, error) {
	phone, err := auth.NormalizePhone(phoneRaw, a.cfg.Prefixes)
	if err != nil {
		return auth.VerifySpec{}, err
	}
	if len(code) != 6 {
		return auth.VerifySpec{}, auth.ErrInvalidCode
	}
	return auth.VerifySpec{
		Phone:       phone,
		Now:         a.now(),
		MaxAttempts: a.cfg.MaxAttempts,
		Match:       func(stored string) bool { return auth.OTPEqual(a.cfg.OTPSecret, code, stored) },
	}, nil
}

// NormalizePhone applies the configured mobile prefixes.
func (a *Auth) NormalizePhone(raw string) (string, error) {
	return auth.NormalizePhone(raw, a.cfg.Prefixes)
}

// Refresh rotates a refresh token and returns a new session.
func (a *Auth) Refresh(ctx context.Context, refreshToken, deviceID string) (Session, error) {
	if refreshToken == "" {
		return Session{}, auth.ErrUnauthorized
	}
	if deviceID != "" && !auth.ValidDeviceID(deviceID) {
		return Session{}, auth.ErrValidation
	}
	raw, hash, err := auth.NewToken("")
	if err != nil {
		return Session{}, err
	}
	now := a.now()
	user, err := a.store.RotateRefresh(ctx, auth.HashSecret(refreshToken), hash, deviceID, now.Add(a.cfg.RefreshTTL))
	if err != nil {
		return Session{}, err
	}
	return a.session(user, raw, now)
}

// Logout revokes a refresh token. An unknown token still succeeds.
func (a *Auth) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	return a.store.RevokeRefresh(ctx, auth.HashSecret(refreshToken))
}

func (a *Auth) session(user auth.SessionUser, refresh string, now time.Time) (Session, error) {
	access, err := auth.SignAccess(a.cfg.JWTSecret, user.ID, now, a.cfg.AccessTTL)
	if err != nil {
		return Session{}, err
	}
	return Session{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    int(a.cfg.AccessTTL.Seconds()),
		User:         user,
	}, nil
}
