package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/internal/repository"
)

const (
	MaxProfileImageUploadBytes = 8 << 20
	profileImageSize           = 512
	profileImageMaxDimension   = 4096
	profileImageMaxPixels      = 12_000_000
)

var ErrInvalidProfileImage = errors.New("invalid profile image")
var ErrProfileImageNotFound = errors.New("profile image not found")

type ProfileImageStorage struct {
	root string
}

func NewProfileImageStorage(root string) (*ProfileImageStorage, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("profile image storage directory is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve profile image storage directory: %w", err)
	}
	if err := os.MkdirAll(absolute, 0700); err != nil {
		return nil, fmt.Errorf("create profile image storage directory: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalidProfileImage
	}
	if err := os.Chmod(absolute, 0700); err != nil {
		return nil, fmt.Errorf("secure profile image storage directory: %w", err)
	}
	return &ProfileImageStorage{root: absolute}, nil
}

func (s *ProfileImageStorage) Store(ctx context.Context, source io.Reader) (string, error) {
	if s == nil || source == nil {
		return "", ErrInvalidProfileImage
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	content, err := io.ReadAll(io.LimitReader(source, MaxProfileImageUploadBytes+1))
	if err != nil {
		return "", fmt.Errorf("read profile image: %w", err)
	}
	if len(content) == 0 || len(content) > MaxProfileImageUploadBytes {
		return "", ErrInvalidProfileImage
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil || (format != "jpeg" && format != "png") || !validProfileImageBounds(config.Width, config.Height) {
		return "", ErrInvalidProfileImage
	}
	decoded, decodedFormat, err := image.Decode(bytes.NewReader(content))
	if err != nil || decodedFormat != format || !validProfileImageBounds(decoded.Bounds().Dx(), decoded.Bounds().Dy()) {
		return "", ErrInvalidProfileImage
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	encoded, err := normalizeProfileImage(decoded)
	if err != nil {
		return "", fmt.Errorf("normalize profile image: %w", err)
	}
	key := uuid.NewString()
	temporary, err := os.CreateTemp(s.root, ".profile-image-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create profile image temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		temporary.Close()
		return "", fmt.Errorf("secure profile image: %w", err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		temporary.Close()
		return "", fmt.Errorf("write profile image: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return "", fmt.Errorf("sync profile image: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("close profile image: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.Link(temporaryPath, s.imagePath(key)); err != nil {
		return "", fmt.Errorf("publish profile image: %w", err)
	}
	return key, nil
}

func validProfileImageBounds(width, height int) bool {
	return width > 0 && height > 0 &&
		width <= profileImageMaxDimension && height <= profileImageMaxDimension &&
		int64(width)*int64(height) <= profileImageMaxPixels
}

func normalizeProfileImage(source image.Image) ([]byte, error) {
	bounds := source.Bounds()
	cropSize := bounds.Dx()
	if bounds.Dy() < cropSize {
		cropSize = bounds.Dy()
	}
	cropX := bounds.Min.X + (bounds.Dx()-cropSize)/2
	cropY := bounds.Min.Y + (bounds.Dy()-cropSize)/2
	cropped := image.NewRGBA(image.Rect(0, 0, profileImageSize, profileImageSize))
	for y := 0; y < profileImageSize; y++ {
		sourceY := cropY + y*cropSize/profileImageSize
		for x := 0; x < profileImageSize; x++ {
			sourceX := cropX + x*cropSize/profileImageSize
			pixel := color.NRGBAModel.Convert(source.At(sourceX, sourceY)).(color.NRGBA)
			alpha := uint32(pixel.A)
			red := (uint32(pixel.R)*alpha + 255*(255-alpha)) / 255
			green := (uint32(pixel.G)*alpha + 255*(255-alpha)) / 255
			blue := (uint32(pixel.B)*alpha + 255*(255-alpha)) / 255
			cropped.SetRGBA(x, y, color.RGBA{R: uint8(red), G: uint8(green), B: uint8(blue), A: 255})
		}
	}
	var output bytes.Buffer
	if err := jpeg.Encode(&output, cropped, &jpeg.Options{Quality: 84}); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func (s *ProfileImageStorage) Open(key string) (*os.File, os.FileInfo, error) {
	if s == nil || !validProfileImageKey(key) {
		return nil, nil, ErrProfileImageNotFound
	}
	filePath := s.imagePath(key)
	info, err := os.Lstat(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, ErrProfileImageNotFound
		}
		return nil, nil, fmt.Errorf("inspect profile image: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return nil, nil, ErrProfileImageNotFound
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("open profile image: %w", err)
	}
	return file, info, nil
}

func (s *ProfileImageStorage) Delete(key string) error {
	if s == nil || !validProfileImageKey(key) {
		return ErrInvalidProfileImage
	}
	if err := os.Remove(s.imagePath(key)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete profile image: %w", err)
	}
	return nil
}

func (s *ProfileImageStorage) imagePath(key string) string {
	return filepath.Join(s.root, key+".jpg")
}

func validProfileImageKey(key string) bool {
	id, err := uuid.Parse(key)
	return err == nil && id != uuid.Nil && id.String() == key
}

type ProfileImageService struct {
	users   repository.ProfileImageRepository
	storage *ProfileImageStorage
}

func NewProfileImageService(users repository.ProfileImageRepository, storage *ProfileImageStorage) *ProfileImageService {
	return &ProfileImageService{users: users, storage: storage}
}

func (s *ProfileImageService) Upload(ctx context.Context, userID uuid.UUID, source io.Reader) (*model.User, error) {
	if s == nil || s.users == nil || s.storage == nil || userID == uuid.Nil {
		return nil, ErrInvalidProfileImage
	}
	key, err := s.storage.Store(ctx, source)
	if err != nil {
		return nil, err
	}
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		_ = s.storage.Delete(key)
		return nil, err
	}
	oldKey, err := s.users.UpdateProfileImageKey(ctx, userID, key)
	if err != nil {
		_ = s.storage.Delete(key)
		return nil, err
	}
	user.ProfileImageKey = key
	if oldKey != "" {
		_ = s.storage.Delete(oldKey)
	}
	return user, nil
}

func (s *ProfileImageService) Remove(ctx context.Context, userID uuid.UUID) (*model.User, error) {
	if s == nil || s.users == nil || s.storage == nil || userID == uuid.Nil {
		return nil, ErrInvalidProfileImage
	}
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	oldKey, err := s.users.UpdateProfileImageKey(ctx, userID, "")
	if err != nil {
		return nil, err
	}
	user.ProfileImageKey = ""
	if oldKey != "" {
		_ = s.storage.Delete(oldKey)
	}
	return user, nil
}

func (s *ProfileImageService) Open(imageKey string) (*os.File, os.FileInfo, error) {
	if s == nil || s.storage == nil {
		return nil, nil, ErrProfileImageNotFound
	}
	return s.storage.Open(imageKey)
}

var _ repository.ProfileImageRepository = (*repository.GormUserRepository)(nil)
