package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/internal/repository"
	"github.com/parabeacon/server/pkg/utils"
	"gorm.io/gorm"
)

type memoryUsers struct {
	byEmail         map[string]*model.User
	byID            map[uuid.UUID]*model.User
	sessionsRevoked bool
}

func newMemoryUsers() *memoryUsers {
	return &memoryUsers{byEmail: make(map[string]*model.User), byID: make(map[uuid.UUID]*model.User)}
}

func (r *memoryUsers) Create(_ context.Context, user *model.User) error {
	if user.Email != nil {
		if _, exists := r.byEmail[*user.Email]; exists {
			return gorm.ErrDuplicatedKey
		}
	}
	for _, existing := range r.byID {
		if existing.Username == user.Username {
			return gorm.ErrDuplicatedKey
		}
	}
	user.ID = uuid.New()
	pilotNumber := 123456 + len(r.byID)
	user.PilotNumber = &pilotNumber
	user.CreatedAt = time.Now().UTC()
	if user.Email != nil {
		r.byEmail[*user.Email] = user
	}
	r.byID[user.ID] = user
	return nil
}

func (r *memoryUsers) FindByLogin(_ context.Context, identifier string) (*model.User, error) {
	if user, ok := r.byEmail[identifier]; ok {
		return user, nil
	}
	for _, user := range r.byID {
		if user.Username == identifier {
			return user, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *memoryUsers) FindByID(_ context.Context, id uuid.UUID) (*model.User, error) {
	user, ok := r.byID[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return user, nil
}

func (r *memoryUsers) ChangePassword(_ context.Context, id uuid.UUID, expectedHash, newHash string, _ time.Time) error {
	user, ok := r.byID[id]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	if user.PasswordHash != expectedHash {
		return repository.ErrPasswordChanged
	}
	user.PasswordHash = newHash
	r.sessionsRevoked = true
	return nil
}

func (r *memoryUsers) UpdateChannelNumber(_ context.Context, id uuid.UUID, channelNumber int) error {
	user, ok := r.byID[id]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	user.ChannelNumber = channelNumber
	return nil
}

func (r *memoryUsers) UpdateDisplayName(_ context.Context, id uuid.UUID, displayName string) error {
	user, ok := r.byID[id]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	user.DisplayName = displayName
	return nil
}

func TestAuthServiceUpdatesChannelNumber(t *testing.T) {
	repo := newMemoryUsers()
	service := NewAuthService(repo, utils.NewTokenManager("0123456789abcdef0123456789abcdef", "test-issuer", time.Minute))
	user, err := service.Register(context.Background(), "frequency_pilot", "", "abc123")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	updated, err := service.UpdateChannelNumber(context.Background(), user.ID, 123456)
	if err != nil {
		t.Fatalf("UpdateChannelNumber() error = %v", err)
	}
	if updated.ChannelNumber != 123456 || repo.byID[user.ID].ChannelNumber != 123456 {
		t.Fatalf("channel number was not saved: updated=%d stored=%d", updated.ChannelNumber, repo.byID[user.ID].ChannelNumber)
	}
	updated, err = service.UpdateChannelNumber(context.Background(), user.ID, 0)
	if err != nil || updated.ChannelNumber != 0 {
		t.Fatalf("clear channel number = %#v, err=%v", updated, err)
	}
	for _, invalid := range []int{-1, 1000000} {
		if _, err := service.UpdateChannelNumber(context.Background(), user.ID, invalid); !errors.Is(err, ErrInvalidChannelNumber) {
			t.Errorf("UpdateChannelNumber(%d) error = %v, want ErrInvalidChannelNumber", invalid, err)
		}
	}
}

func TestAuthServiceUpdatesDisplayNameWithValidation(t *testing.T) {
	repo := newMemoryUsers()
	service := NewAuthService(repo, utils.NewTokenManager("0123456789abcdef0123456789abcdef", "test-issuer", time.Minute))
	user, err := service.Register(context.Background(), "sky_rider", "", "abc123")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	updated, err := service.UpdateDisplayName(context.Background(), user.ID, "  Sky Rider  ")
	if err != nil {
		t.Fatalf("UpdateDisplayName() error = %v", err)
	}
	if updated.DisplayName != "Sky Rider" {
		t.Fatalf("display name = %q, want trimmed name", updated.DisplayName)
	}
	if repo.byID[user.ID].DisplayName != "Sky Rider" {
		t.Fatalf("persisted display name = %q", repo.byID[user.ID].DisplayName)
	}

	for _, invalid := range []string{strings.Repeat("a", 49), "first\nsecond", "first\rsecond", "first\x00second"} {
		if _, err := service.UpdateDisplayName(context.Background(), user.ID, invalid); !errors.Is(err, ErrInvalidDisplayName) {
			t.Errorf("UpdateDisplayName(%q) error = %v, want invalid display name", invalid, err)
		}
	}
	updated, err = service.UpdateDisplayName(context.Background(), user.ID, "")
	if err != nil || updated.DisplayName != "" {
		t.Fatalf("clear display name = %#v, err=%v", updated, err)
	}
}

func TestAuthServiceChangePasswordVerifiesCurrentPasswordAndRevokesSessions(t *testing.T) {
	repo := newMemoryUsers()
	service := NewAuthService(repo, utils.NewTokenManager("0123456789abcdef0123456789abcdef", "test-issuer", time.Minute))
	user, err := service.Register(context.Background(), "password_pilot", "", "old-pass-123")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	oldHash := user.PasswordHash

	if err := service.ChangePassword(context.Background(), user.ID, "incorrect", "new-pass-456"); !errors.Is(err, ErrInvalidCurrentPassword) {
		t.Fatalf("wrong current password error = %v", err)
	}
	if repo.byID[user.ID].PasswordHash != oldHash || repo.sessionsRevoked {
		t.Fatal("wrong current password changed credentials or revoked sessions")
	}
	if err := service.ChangePassword(context.Background(), user.ID, "old-pass-123", "short"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("short new password error = %v", err)
	}
	if err := service.ChangePassword(context.Background(), user.ID, "old-pass-123", "old-pass-123"); !errors.Is(err, ErrPasswordUnchanged) {
		t.Fatalf("unchanged password error = %v", err)
	}
	if err := service.ChangePassword(context.Background(), user.ID, "old-pass-123", strings.Repeat("界", 25)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized UTF-8 password error = %v", err)
	}
	if err := service.ChangePassword(context.Background(), user.ID, "old-pass-123", "new-pass-456"); err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}
	if !repo.sessionsRevoked || utils.VerifyPassword(repo.byID[user.ID].PasswordHash, "new-pass-456") != nil {
		t.Fatal("new password was not stored or sessions were not revoked")
	}
	if utils.VerifyPassword(repo.byID[user.ID].PasswordHash, "old-pass-123") == nil {
		t.Fatal("old password still matches after change")
	}
	if _, err := service.Login(context.Background(), user.Username, "old-pass-123"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password login error = %v, want invalid credentials", err)
	}
	if _, err := service.Login(context.Background(), user.Username, "new-pass-456"); err != nil {
		t.Fatalf("new password login error = %v", err)
	}
}

func TestAuthServiceRegisterLoginAndProfile(t *testing.T) {
	repo := newMemoryUsers()
	tokens := utils.NewTokenManager("0123456789abcdef0123456789abcdef", "test-issuer", time.Minute)
	svc := NewAuthService(repo, tokens)
	ctx := context.Background()

	user, err := svc.Register(ctx, "Sky_Rider", "Pilot@Example.com", "a-long-unique-passphrase")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if user.Username != "sky_rider" || user.Email == nil || *user.Email != "pilot@example.com" {
		t.Fatalf("registration did not normalize identifiers: %#v", user)
	}
	if user.PilotNumber == nil || *user.PilotNumber < 100000 || *user.PilotNumber > 999999 {
		t.Fatalf("registration pilot number = %v, want six digits", user.PilotNumber)
	}
	if strings.Contains(user.PasswordHash, "a-long-unique-passphrase") {
		t.Fatal("stored password hash contains the plaintext password")
	}
	if _, err := svc.Register(ctx, "sky_rider", "pilot@example.com", "a-long-unique-passphrase"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate Register() error = %v, want ErrConflict", err)
	}

	usernameOnly, err := svc.Register(ctx, "new_pilot", "", "abc123")
	if err != nil {
		t.Fatalf("Register() without optional email error = %v", err)
	}
	if usernameOnly.Email != nil {
		t.Fatalf("email without a supplied value = %v, want nil", usernameOnly.Email)
	}
	if _, err := svc.Register(ctx, "short_pilot", "", "abc12"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Register() with a five-character password error = %v, want ErrInvalidInput", err)
	}
	if _, err := svc.Register(ctx, "another_pilot", "not-an-email", "another-long-passphrase"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Register() with invalid optional email error = %v, want ErrInvalidInput", err)
	}
	if _, err := svc.Login(ctx, "NEW_PILOT", "abc123"); err != nil {
		t.Fatalf("Login() without email error = %v", err)
	}

	result, err := svc.Login(ctx, "PILOT@example.com", "a-long-unique-passphrase")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	claims, err := tokens.Parse(result.Token)
	if err != nil {
		t.Fatalf("issued access token invalid: %v", err)
	}
	if claims.Subject != user.ID.String() {
		t.Fatalf("token subject = %q, want %q", claims.Subject, user.ID)
	}
	usernameResult, err := svc.Login(ctx, "SKY_RIDER", "a-long-unique-passphrase")
	if err != nil {
		t.Fatalf("Login() with username error = %v", err)
	}
	usernameClaims, err := tokens.Parse(usernameResult.Token)
	if err != nil || usernameClaims.Subject != user.ID.String() {
		t.Fatalf("username login token claims = %#v, error = %v", usernameClaims, err)
	}
	if _, err := svc.Login(ctx, "pilot@example.com", "incorrect-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("invalid password Login() error = %v, want ErrInvalidCredentials", err)
	}
	profile, err := svc.Profile(ctx, user.ID)
	if err != nil {
		t.Fatalf("Profile() error = %v", err)
	}
	if profile.ID != user.ID {
		t.Fatalf("profile id = %q, want %q", profile.ID, user.ID)
	}
}
