package service

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"gorm.io/gorm"
)

type profileImageTestRepository struct {
	user *model.User
}

func (r *profileImageTestRepository) FindByID(_ context.Context, id uuid.UUID) (*model.User, error) {
	if r.user == nil || r.user.ID != id {
		return nil, gorm.ErrRecordNotFound
	}
	copy := *r.user
	return &copy, nil
}

func (r *profileImageTestRepository) UpdateProfileImageKey(_ context.Context, id uuid.UUID, key string) (string, error) {
	if r.user == nil || r.user.ID != id {
		return "", gorm.ErrRecordNotFound
	}
	previous := r.user.ProfileImageKey
	r.user.ProfileImageKey = key
	return previous, nil
}

func testProfileImagePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	imageData := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			imageData.SetNRGBA(x, y, color.NRGBA{R: 20, G: 100, B: 200, A: 255})
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, imageData); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}
	return output.Bytes()
}

func TestProfileImageStorageNormalizesUploadsToCroppedJPEG(t *testing.T) {
	storage, err := NewProfileImageStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewProfileImageStorage() error = %v", err)
	}
	key, err := storage.Store(context.Background(), bytes.NewReader(testProfileImagePNG(t, 800, 400)))
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	file, info, err := storage.Open(key)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		t.Fatalf("profile image permissions = %o, want private file", info.Mode().Perm())
	}
	decoded, err := jpeg.Decode(file)
	if err != nil {
		t.Fatalf("stored image is not a normalized JPEG: %v", err)
	}
	if decoded.Bounds().Dx() != profileImageSize || decoded.Bounds().Dy() != profileImageSize {
		t.Fatalf("stored image dimensions = %v, want %dx%d", decoded.Bounds(), profileImageSize, profileImageSize)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close profile image: %v", err)
	}
	if err := storage.Delete(key); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, _, err := storage.Open(key); !errors.Is(err, ErrProfileImageNotFound) {
		t.Fatalf("Open() after delete error = %v, want not found", err)
	}
}

func TestProfileImageStorageRejectsUnsupportedAndOversizedInput(t *testing.T) {
	storage, err := NewProfileImageStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewProfileImageStorage() error = %v", err)
	}
	for name, content := range map[string][]byte{
		"empty":     nil,
		"invalid":   []byte("not an image"),
		"oversized": bytes.Repeat([]byte{1}, MaxProfileImageUploadBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := storage.Store(context.Background(), bytes.NewReader(content)); !errors.Is(err, ErrInvalidProfileImage) {
				t.Fatalf("Store() error = %v, want ErrInvalidProfileImage", err)
			}
		})
	}
}

func TestProfileImageStorageRejectsExcessiveDimensionsBeforeDecode(t *testing.T) {
	storage, err := NewProfileImageStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewProfileImageStorage() error = %v", err)
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, profileImageMaxDimension+1, 1))); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}
	if _, err := storage.Store(context.Background(), bytes.NewReader(encoded.Bytes())); !errors.Is(err, ErrInvalidProfileImage) {
		t.Fatalf("Store() oversized dimensions error = %v, want ErrInvalidProfileImage", err)
	}
}

func TestProfileImageServiceReplacesAndRemovesImageForOwner(t *testing.T) {
	userID := uuid.New()
	repository := &profileImageTestRepository{user: &model.User{ID: userID, Username: "pilot"}}
	storage, err := NewProfileImageStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewProfileImageStorage() error = %v", err)
	}
	images := NewProfileImageService(repository, storage)

	first, err := images.Upload(context.Background(), userID, bytes.NewReader(testProfileImagePNG(t, 16, 16)))
	if err != nil {
		t.Fatalf("first Upload() error = %v", err)
	}
	firstKey := first.ProfileImageKey
	if firstKey == "" || repository.user.ProfileImageKey != firstKey {
		t.Fatalf("first image key = %q; stored=%q", firstKey, repository.user.ProfileImageKey)
	}
	second, err := images.Upload(context.Background(), userID, bytes.NewReader(testProfileImagePNG(t, 12, 20)))
	if err != nil {
		t.Fatalf("replacement Upload() error = %v", err)
	}
	if second.ProfileImageKey == firstKey {
		t.Fatal("replacement image reused the old image key")
	}
	if _, _, err := storage.Open(firstKey); !errors.Is(err, ErrProfileImageNotFound) {
		t.Fatalf("replaced image is still present: %v", err)
	}
	removed, err := images.Remove(context.Background(), userID)
	if err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if removed.ProfileImageKey != "" || repository.user.ProfileImageKey != "" {
		t.Fatalf("removed key = %q; stored=%q", removed.ProfileImageKey, repository.user.ProfileImageKey)
	}
	if _, _, err := storage.Open(second.ProfileImageKey); !errors.Is(err, ErrProfileImageNotFound) {
		t.Fatalf("removed image is still present: %v", err)
	}
	if _, err := images.Remove(context.Background(), uuid.New()); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("non-owner Remove() error = %v, want not found", err)
	}
}

func TestProfileImageStorageDoesNotFollowStoredImageSymlink(t *testing.T) {
	root := t.TempDir()
	storage, err := NewProfileImageStorage(root)
	if err != nil {
		t.Fatalf("NewProfileImageStorage() error = %v", err)
	}
	key := uuid.NewString()
	target := t.TempDir() + string(os.PathSeparator) + "target.jpg"
	if err := os.WriteFile(target, []byte("secret"), 0600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, storage.imagePath(key)); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if _, _, err := storage.Open(key); !errors.Is(err, ErrProfileImageNotFound) {
		t.Fatalf("Open() symlink error = %v, want not found", err)
	}
}
