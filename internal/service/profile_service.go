package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/auth"
	"github.com/shalarasgar/carpool-backend/internal/media"
	"github.com/shalarasgar/carpool-backend/internal/ride"
)

// ProfileStore is the persistence of accounts and vehicles.
type ProfileStore interface {
	Profile(ctx context.Context, id string) (ride.Profile, error)
	PublicProfile(ctx context.Context, id string) (ride.Profile, error)
	UpdateProfile(ctx context.Context, id string, p ride.ProfilePatch) (ride.Profile, error)
	SetUserPhoto(ctx context.Context, id, url string) (string, error)
	PhoneInUse(ctx context.Context, phone, exceptUserID string) (bool, error)
	ChangePhone(ctx context.Context, userID string, in auth.VerifySpec) (ride.Profile, error)
	ListVehicles(ctx context.Context, userID string) ([]ride.Vehicle, error)
	Vehicle(ctx context.Context, userID, id string) (ride.Vehicle, error)
	CreateVehicle(ctx context.Context, v ride.Vehicle) (ride.Vehicle, error)
	UpdateVehicle(ctx context.Context, v ride.Vehicle) (ride.Vehicle, error)
	DeleteVehicle(ctx context.Context, userID, id string) error
	SetVehiclePhoto(ctx context.Context, userID, id, url string) (string, error)
	Locations(ctx context.Context) ([]ride.Location, error)
	DeleteAccount(ctx context.Context, userID string, now time.Time) (mediaURLs []string, err error)
	UserIDByPhone(ctx context.Context, phone string) (string, error)
}

// PhoneVerifier is the part of the OTP flow a phone change reuses.
type PhoneVerifier interface {
	NormalizePhone(raw string) (string, error)
	RequestOTP(ctx context.Context, phone, ip, deviceID, locale string) (string, error)
	PhoneCheck(phoneRaw, code string) (auth.VerifySpec, error)
}

// Profiles manages accounts, photos, vehicles, and the location catalog.
type Profiles struct {
	store  ProfileStore
	media  media.Storage
	otp    PhoneVerifier
	logger *slog.Logger
	now    func() time.Time
}

// NewProfiles returns the profile service. logger may be nil.
func NewProfiles(store ProfileStore, storage media.Storage, otp PhoneVerifier, logger *slog.Logger) *Profiles {
	if logger == nil {
		logger = slog.Default()
	}
	return &Profiles{store: store, media: storage, otp: otp, logger: logger, now: time.Now}
}

// Me returns the caller's own profile, including the phone number.
func (p *Profiles) Me(ctx context.Context, userID string) (ride.Profile, error) {
	return p.store.Profile(ctx, userID)
}

// Public returns another user's profile without private fields.
func (p *Profiles) Public(ctx context.Context, id string) (ride.Profile, error) {
	if !ride.ValidID(id) {
		return ride.Profile{}, ride.ErrNotFound
	}
	return p.store.PublicProfile(ctx, id)
}

// Update validates and applies a profile patch.
func (p *Profiles) Update(ctx context.Context, userID string, patch ride.ProfilePatch) (ride.Profile, error) {
	if err := patch.Normalize(); err != nil {
		return ride.Profile{}, err
	}
	return p.store.UpdateProfile(ctx, userID, patch)
}

// SetPhoto stores a new profile photo and removes the previous file.
func (p *Profiles) SetPhoto(ctx context.Context, userID string, data []byte) (ride.Profile, error) {
	url, err := p.media.Save(ctx, "user", data)
	if err != nil {
		return ride.Profile{}, err
	}
	old, err := p.store.SetUserPhoto(ctx, userID, url)
	if err != nil {
		_ = p.media.Delete(ctx, url)
		return ride.Profile{}, err
	}
	p.dropFile(ctx, old)
	return p.store.Profile(ctx, userID)
}

// RemovePhoto clears the profile photo.
func (p *Profiles) RemovePhoto(ctx context.Context, userID string) (ride.Profile, error) {
	old, err := p.store.SetUserPhoto(ctx, userID, "")
	if err != nil {
		return ride.Profile{}, err
	}
	p.dropFile(ctx, old)
	return p.store.Profile(ctx, userID)
}

func (p *Profiles) dropFile(ctx context.Context, url string) {
	if url == "" {
		return
	}
	if err := p.media.Delete(ctx, url); err != nil {
		p.logger.Warn("media delete failed", "err", err)
	}
}

// RequestPhoneChange sends an OTP to the new number through the existing SMS queue.
func (p *Profiles) RequestPhoneChange(ctx context.Context, userID, phoneRaw, ip, deviceID, locale string) (string, error) {
	phone, err := p.otp.NormalizePhone(phoneRaw)
	if err != nil {
		return "", err
	}
	taken, err := p.store.PhoneInUse(ctx, phone, userID)
	if err != nil {
		return "", err
	}
	if taken {
		return "", ride.ErrPhoneTaken
	}
	return p.otp.RequestOTP(ctx, phone, ip, deviceID, locale)
}

// ConfirmPhoneChange checks the OTP of the new number and moves the account to it.
func (p *Profiles) ConfirmPhoneChange(ctx context.Context, userID, phoneRaw, code string) (ride.Profile, error) {
	spec, err := p.otp.PhoneCheck(phoneRaw, code)
	if err != nil {
		return ride.Profile{}, err
	}
	return p.store.ChangePhone(ctx, userID, spec)
}

// DeleteAccount erases the caller's account: personal data, vehicles, sessions, and open
// trips and bookings. It fails with ride.ErrTripInProgress while a trip the caller drives
// is under way. Photo files are removed after the database change commits; a file that
// cannot be removed is logged and does not undo the deletion.
func (p *Profiles) DeleteAccount(ctx context.Context, userID string) error {
	files, err := p.store.DeleteAccount(ctx, userID, p.now())
	if err != nil {
		return err
	}
	for _, url := range files {
		p.dropFile(ctx, url)
	}
	return nil
}

// DeleteAccountByPhone erases the account that owns a phone number. An operator uses it
// for a deletion request received outside the app.
func (p *Profiles) DeleteAccountByPhone(ctx context.Context, phoneRaw string) error {
	phone, err := p.otp.NormalizePhone(phoneRaw)
	if err != nil {
		return err
	}
	userID, err := p.store.UserIDByPhone(ctx, phone)
	if err != nil {
		return err
	}
	return p.DeleteAccount(ctx, userID)
}

// Vehicles lists the caller's vehicles.
func (p *Profiles) Vehicles(ctx context.Context, userID string) ([]ride.Vehicle, error) {
	return p.store.ListVehicles(ctx, userID)
}

// AddVehicle validates and stores a vehicle for the caller.
func (p *Profiles) AddVehicle(ctx context.Context, userID string, v ride.Vehicle) (ride.Vehicle, error) {
	v.UserID = userID
	if err := v.Normalize(); err != nil {
		return ride.Vehicle{}, err
	}
	return p.store.CreateVehicle(ctx, v)
}

// EditVehicle replaces an owned vehicle's fields.
func (p *Profiles) EditVehicle(ctx context.Context, userID, id string, v ride.Vehicle) (ride.Vehicle, error) {
	if !ride.ValidID(id) {
		return ride.Vehicle{}, ride.ErrNotFound
	}
	v.ID, v.UserID = id, userID
	if err := v.Normalize(); err != nil {
		return ride.Vehicle{}, err
	}
	return p.store.UpdateVehicle(ctx, v)
}

// RemoveVehicle soft-deletes an owned vehicle that no live trip uses.
func (p *Profiles) RemoveVehicle(ctx context.Context, userID, id string) error {
	if !ride.ValidID(id) {
		return ride.ErrNotFound
	}
	return p.store.DeleteVehicle(ctx, userID, id)
}

// SetVehiclePhoto stores a vehicle photo and removes the previous file.
func (p *Profiles) SetVehiclePhoto(ctx context.Context, userID, id string, data []byte) (ride.Vehicle, error) {
	if !ride.ValidID(id) {
		return ride.Vehicle{}, ride.ErrNotFound
	}
	if _, err := p.store.Vehicle(ctx, userID, id); err != nil {
		return ride.Vehicle{}, err
	}
	url, err := p.media.Save(ctx, "vehicle", data)
	if err != nil {
		return ride.Vehicle{}, err
	}
	old, err := p.store.SetVehiclePhoto(ctx, userID, id, url)
	if err != nil {
		_ = p.media.Delete(ctx, url)
		return ride.Vehicle{}, err
	}
	p.dropFile(ctx, old)
	return p.store.Vehicle(ctx, userID, id)
}

// Locations returns the active location catalog.
func (p *Profiles) Locations(ctx context.Context) ([]ride.Location, error) {
	return p.store.Locations(ctx)
}
