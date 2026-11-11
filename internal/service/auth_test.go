package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/pkg/utils"
	"gorm.io/gorm"
)

type memoryUsers struct {
	byEmail map[string]*model.User
	byID    map[uuid.UUID]*model.User
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
	if strings.Contains(user.PasswordHash, "a-long-unique-passphrase") {
		t.Fatal("stored password hash contains the plaintext password")
	}
	if _, err := svc.Register(ctx, "sky_rider", "pilot@example.com", "a-long-unique-passphrase"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate Register() error = %v, want ErrConflict", err)
	}

	usernameOnly, err := svc.Register(ctx, "new_pilot", "", "another-long-passphrase")
	if err != nil {
		t.Fatalf("Register() without optional email error = %v", err)
	}
	if usernameOnly.Email != nil {
		t.Fatalf("email without a supplied value = %v, want nil", usernameOnly.Email)
	}
	if _, err := svc.Register(ctx, "another_pilot", "not-an-email", "another-long-passphrase"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Register() with invalid optional email error = %v, want ErrInvalidInput", err)
	}
	if _, err := svc.Login(ctx, "NEW_PILOT", "another-long-passphrase"); err != nil {
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
