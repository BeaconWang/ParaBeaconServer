package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/internal/repository"
	"github.com/parabeacon/server/pkg/utils"
	"gorm.io/gorm"
)

var (
	ErrConflict                  = errors.New("username or email already exists")
	ErrInvalidDisplayName        = errors.New("invalid display name")
	ErrInvalidIntroduction       = errors.New("invalid introduction")
	ErrInvalidGender             = errors.New("invalid gender")
	ErrInvalidBirthday           = errors.New("invalid birthday")
	ErrInvalidChannelNumber      = errors.New("invalid channel number")
	ErrInvalidAvatarID           = errors.New("invalid avatar id")
	ErrInvalidCredentials        = errors.New("invalid email or password")
	ErrInvalidCurrentPassword    = errors.New("invalid current password")
	ErrPasswordUnchanged         = errors.New("new password must differ from current password")
	ErrPasswordChangeUnavailable = errors.New("password change unavailable")
	ErrInvalidInput              = errors.New("invalid registration input")
	ErrNotFound                  = errors.New("user not found")
	ErrInvalidRefresh            = repository.ErrInvalidRefreshToken
	usernamePattern              = regexp.MustCompile(`^[a-zA-Z0-9_]{3,32}$`)
)

const (
	MinPasswordCharacters = 6
	MaxAvatarID           = 7
	MaxIntroductionRunes  = 200
)

var allowedProfileGenders = map[string]struct{}{
	"":                  {},
	"male":              {},
	"female":            {},
	"other":             {},
	"prefer_not_to_say": {},
}

func IsValidAvatarID(avatarID int) bool {
	return avatarID >= 0 && avatarID <= MaxAvatarID
}

type AuthService struct {
	users         repository.UserRepository
	tokens        *utils.TokenManager
	refreshTokens repository.RefreshTokenRepository
}

type AuthResult struct {
	User             *model.User
	Token            string
	ExpiresAt        time.Time
	RefreshToken     string
	RefreshExpiresAt *time.Time
}

func NewAuthService(users repository.UserRepository, tokens *utils.TokenManager, refreshTokens ...repository.RefreshTokenRepository) *AuthService {
	service := &AuthService{users: users, tokens: tokens}
	if len(refreshTokens) > 0 {
		service.refreshTokens = refreshTokens[0]
	}
	return service
}

func (s *AuthService) Register(ctx context.Context, username, email, password string) (*model.User, error) {
	// The account name keeps the caller's casing; only lookups are case-insensitive.
	username = strings.TrimSpace(username)
	email = strings.ToLower(strings.TrimSpace(email))
	if !usernamePattern.MatchString(username) {
		return nil, fmt.Errorf("%w: username must be 3-32 letters, digits, or underscores", ErrInvalidInput)
	}
	if strings.EqualFold(username, "beacon") {
		return nil, ErrConflict
	}
	if email != "" {
		parsedEmail, err := mail.ParseAddress(email)
		if err != nil || parsedEmail.Address != email || len(email) > 254 {
			return nil, fmt.Errorf("%w: invalid email address", ErrInvalidInput)
		}
	}
	if utf8.RuneCountInString(password) < MinPasswordCharacters || len(password) > utils.MaxPasswordBytes {
		return nil, fmt.Errorf("%w: password must contain at least %d characters and no more than %d bytes", ErrInvalidInput, MinPasswordCharacters, utils.MaxPasswordBytes)
	}

	hash, err := utils.HashPassword(password)
	if err != nil {
		return nil, err
	}
	user := &model.User{Username: username, PasswordHash: hash}
	if email != "" {
		user.Email = &email
	}
	if err := s.users.Create(ctx, user); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, ErrConflict
		}
		return nil, err
	}
	return user, nil
}

func (s *AuthService) UpdateChannelNumber(ctx context.Context, userID uuid.UUID, channelNumber int) (*model.User, error) {
	if userID == uuid.Nil {
		return nil, ErrNotFound
	}
	if channelNumber < 0 || channelNumber > 999999 {
		return nil, ErrInvalidChannelNumber
	}
	user, err := s.Profile(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := s.users.UpdateChannelNumber(ctx, userID, channelNumber); err != nil {
		return nil, err
	}
	user.ChannelNumber = channelNumber
	return user, nil
}

func (s *AuthService) UpdateAvatarID(ctx context.Context, userID uuid.UUID, avatarID int) (*model.User, error) {
	if userID == uuid.Nil {
		return nil, ErrNotFound
	}
	if !IsValidAvatarID(avatarID) {
		return nil, ErrInvalidAvatarID
	}
	user, err := s.Profile(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := s.users.UpdateAvatarID(ctx, userID, avatarID); err != nil {
		return nil, err
	}
	user.AvatarID = avatarID
	return user, nil
}

func (s *AuthService) UpdateDisplayName(ctx context.Context, userID uuid.UUID, displayName string) (*model.User, error) {
	if userID == uuid.Nil {
		return nil, ErrNotFound
	}
	displayName = strings.TrimSpace(displayName)
	if utf8.RuneCountInString(displayName) > 48 || strings.ContainsAny(displayName, "\x00\r\n") {
		return nil, ErrInvalidDisplayName
	}
	user, err := s.Profile(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := s.users.UpdateDisplayName(ctx, userID, displayName); err != nil {
		return nil, err
	}
	user.DisplayName = displayName
	return user, nil
}

func (s *AuthService) UpdateIntroduction(ctx context.Context, userID uuid.UUID, introduction string) (*model.User, error) {
	if userID == uuid.Nil {
		return nil, ErrNotFound
	}
	introduction = strings.TrimSpace(introduction)
	if utf8.RuneCountInString(introduction) > MaxIntroductionRunes || strings.ContainsRune(introduction, '\x00') {
		return nil, ErrInvalidIntroduction
	}
	user, err := s.Profile(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := s.users.UpdateIntroduction(ctx, userID, introduction); err != nil {
		return nil, err
	}
	user.Introduction = introduction
	return user, nil
}

func (s *AuthService) UpdateGender(ctx context.Context, userID uuid.UUID, gender string) (*model.User, error) {
	if userID == uuid.Nil {
		return nil, ErrNotFound
	}
	gender = strings.ToLower(strings.TrimSpace(gender))
	if _, ok := allowedProfileGenders[gender]; !ok {
		return nil, ErrInvalidGender
	}
	user, err := s.Profile(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := s.users.UpdateGender(ctx, userID, gender); err != nil {
		return nil, err
	}
	user.Gender = gender
	return user, nil
}

func (s *AuthService) UpdateBirthday(ctx context.Context, userID uuid.UUID, birthday string) (*model.User, error) {
	if userID == uuid.Nil {
		return nil, ErrNotFound
	}
	birthday = strings.TrimSpace(birthday)
	if birthday != "" {
		parsed, err := time.Parse("2006-01-02", birthday)
		if err != nil || parsed.Format("2006-01-02") != birthday || parsed.Year() < 1900 {
			return nil, ErrInvalidBirthday
		}
		today := time.Now().UTC().Format("2006-01-02")
		if birthday > today {
			return nil, ErrInvalidBirthday
		}
	}
	user, err := s.Profile(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := s.users.UpdateBirthday(ctx, userID, birthday); err != nil {
		return nil, err
	}
	user.Birthday = birthday
	return user, nil
}

func (s *AuthService) ChangePassword(ctx context.Context, userID uuid.UUID, currentPassword, newPassword string) error {
	if userID == uuid.Nil {
		return ErrNotFound
	}
	if utf8.RuneCountInString(newPassword) < MinPasswordCharacters || len(newPassword) > utils.MaxPasswordBytes {
		return ErrInvalidInput
	}
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	if err := utils.VerifyPassword(user.PasswordHash, currentPassword); err != nil {
		return ErrInvalidCurrentPassword
	}
	if err := utils.VerifyPassword(user.PasswordHash, newPassword); err == nil {
		return ErrPasswordUnchanged
	}
	passwordRepository, ok := s.users.(repository.PasswordChangeRepository)
	if !ok {
		return ErrPasswordChangeUnavailable
	}
	newHash, err := utils.HashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := passwordRepository.ChangePassword(ctx, userID, user.PasswordHash, newHash, time.Now().UTC()); err != nil {
		if errors.Is(err, repository.ErrPasswordChanged) {
			return ErrInvalidCurrentPassword
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func (s *AuthService) Login(ctx context.Context, identifier, password string) (*AuthResult, error) {
	// Login identifiers are matched case-insensitively by the repository.
	identifier = strings.TrimSpace(identifier)
	user, err := s.users.FindByLogin(ctx, identifier)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	if err := utils.VerifyPassword(user.PasswordHash, password); err != nil {
		return nil, ErrInvalidCredentials
	}
	if s.refreshTokens == nil {
		token, expiresAt, err := s.tokens.Issue(user.ID)
		if err != nil {
			return nil, err
		}
		return &AuthResult{User: user, Token: token, ExpiresAt: expiresAt}, nil
	}

	refreshToken, tokenHash, err := createRefreshToken()
	if err != nil {
		return nil, err
	}
	sessionID := uuid.New()
	token, expiresAt, err := s.tokens.Issue(user.ID, sessionID)
	if err != nil {
		return nil, err
	}
	if err := s.refreshTokens.CreateSession(ctx, &model.RefreshToken{
		ID:                   sessionID,
		UserID:               user.ID,
		TokenHash:            tokenHash,
		ExpectedPasswordHash: user.PasswordHash,
	}); err != nil {
		if errors.Is(err, repository.ErrPasswordChanged) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	return &AuthResult{
		User:         user,
		Token:        token,
		ExpiresAt:    expiresAt,
		RefreshToken: refreshToken,
	}, nil
}

func (s *AuthService) Refresh(ctx context.Context, rawToken string) (*AuthResult, error) {
	if s.refreshTokens == nil || len(rawToken) < 32 || len(rawToken) > 128 {
		return nil, ErrInvalidRefresh
	}
	newToken, newHash, err := createRefreshToken()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	replacement := &model.RefreshToken{
		ID:        uuid.New(),
		TokenHash: newHash,
	}
	userID, err := s.refreshTokens.Rotate(ctx, hashRefreshToken(rawToken), now, replacement)
	if err != nil {
		if errors.Is(err, repository.ErrInvalidRefreshToken) {
			return nil, ErrInvalidRefresh
		}
		return nil, err
	}
	accessToken, expiresAt, err := s.tokens.Issue(userID, replacement.ID)
	if err != nil {
		return nil, err
	}
	return &AuthResult{
		Token:        accessToken,
		ExpiresAt:    expiresAt,
		RefreshToken: newToken,
	}, nil
}

func createRefreshToken() (string, string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", "", fmt.Errorf("generate refresh token: %w", err)
	}
	raw := base64.RawURLEncoding.EncodeToString(value)
	return raw, hashRefreshToken(raw), nil
}

func hashRefreshToken(raw string) string {
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:])
}

func (s *AuthService) Logout(ctx context.Context, rawRefreshToken string) error {
	if s.refreshTokens == nil || len(rawRefreshToken) < 32 || len(rawRefreshToken) > 128 {
		return nil
	}
	return s.refreshTokens.Revoke(ctx, hashRefreshToken(rawRefreshToken), time.Now().UTC())
}

func (s *AuthService) Profile(ctx context.Context, userID uuid.UUID) (*model.User, error) {
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return user, nil
}
