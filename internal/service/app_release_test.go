package service

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/internal/repository"
)

type memoryAppReleaseRepository struct {
	releases map[int64]*model.AppRelease
}

func newMemoryAppReleaseRepository() *memoryAppReleaseRepository {
	return &memoryAppReleaseRepository{releases: make(map[int64]*model.AppRelease)}
}

func (r *memoryAppReleaseRepository) Create(_ context.Context, release *model.AppRelease) error {
	if _, exists := r.releases[release.VersionCode]; exists {
		return repository.ErrAppReleaseAlreadyExists
	}
	r.releases[release.VersionCode] = release
	return nil
}

func (r *memoryAppReleaseRepository) ListPublished(_ context.Context, packageName string) ([]model.AppRelease, error) {
	result := make([]model.AppRelease, 0, len(r.releases))
	for _, release := range r.releases {
		if release.PackageName == packageName && release.PublishedAt != nil {
			result = append(result, *release)
		}
	}
	return result, nil
}

func (r *memoryAppReleaseRepository) Latest(_ context.Context, packageName string) (*model.AppRelease, error) {
	var latest *model.AppRelease
	for _, release := range r.releases {
		if release.PackageName == packageName && release.PublishedAt != nil &&
			(latest == nil || release.VersionCode > latest.VersionCode) {
			latest = release
		}
	}
	if latest == nil {
		return nil, repository.ErrAppReleaseNotFound
	}
	return latest, nil
}

func (r *memoryAppReleaseRepository) Delete(_ context.Context, packageName string, versionCode int64) error {
	release := r.releases[versionCode]
	if release == nil || release.PackageName != packageName || release.PublishedAt == nil {
		return repository.ErrAppReleaseNotFound
	}
	delete(r.releases, versionCode)
	return nil
}

func (r *memoryAppReleaseRepository) FindPublished(_ context.Context, packageName string, versionCode int64) (*model.AppRelease, error) {
	release := r.releases[versionCode]
	if release == nil || release.PackageName != packageName || release.PublishedAt == nil {
		return nil, repository.ErrAppReleaseNotFound
	}
	return release, nil
}

func makeTestAPK(t *testing.T) []byte {
	t.Helper()
	var content bytes.Buffer
	archive := zip.NewWriter(&content)
	manifest, err := archive.Create("AndroidManifest.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.Write([]byte("binary Android manifest")); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return content.Bytes()
}

func TestAppReleasePublishStoresAPKAndLatestMetadata(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryAppReleaseRepository()
	storageRoot := t.TempDir()
	service, err := NewAppReleaseService(repository, storageRoot, "com.beacon.parabeacon")
	if err != nil {
		t.Fatal(err)
	}
	apk := makeTestAPK(t)
	release, err := service.Publish(ctx, "1.2.3", 123, bytes.NewReader(apk))
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if release.VersionName != "1.2.3" || release.VersionCode != 123 || release.PackageName != "com.beacon.parabeacon" {
		t.Fatalf("release metadata = %#v", release)
	}
	if release.SizeBytes != int64(len(apk)) {
		t.Fatalf("APK size = %d, want %d", release.SizeBytes, len(apk))
	}
	digest := sha256.Sum256(apk)
	if release.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("APK digest = %s", release.SHA256)
	}
	if filepath.Base(release.StorageKey) != release.StorageKey {
		t.Fatalf("storage key is not opaque: %q", release.StorageKey)
	}
	storedAPK, err := os.ReadFile(filepath.Join(storageRoot, release.StorageKey))
	if err != nil || !bytes.Equal(storedAPK, apk) {
		t.Fatalf("stored APK mismatch: error = %v", err)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(filepath.Join(storageRoot, release.StorageKey)); err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("published APK permissions = %v, error = %v", info, err)
		}
	}

	latest, err := service.Latest(ctx)
	if err != nil || latest.VersionCode != release.VersionCode {
		t.Fatalf("Latest() = %#v, error = %v", latest, err)
	}
	downloaded, path, err := service.FindPublished(ctx, release.VersionCode)
	if err != nil || downloaded.StorageKey != release.StorageKey || path != filepath.Join(storageRoot, release.StorageKey) {
		t.Fatalf("FindPublished() = %#v, %q, error = %v", downloaded, path, err)
	}
}

func TestAppReleaseDeleteRemovesMetadataAndAPK(t *testing.T) {
	releaseRepository := newMemoryAppReleaseRepository()
	storageRoot := t.TempDir()
	service, err := NewAppReleaseService(releaseRepository, storageRoot, "com.beacon.parabeacon")
	if err != nil {
		t.Fatal(err)
	}
	release, err := service.Publish(context.Background(), "1.2.3", 123, bytes.NewReader(makeTestAPK(t)))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(storageRoot, release.StorageKey)
	if err := service.Delete(context.Background(), release.VersionCode); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("APK still exists, error = %v", err)
	}
	if _, _, err := service.FindPublished(context.Background(), release.VersionCode); !errors.Is(err, repository.ErrAppReleaseNotFound) {
		t.Fatalf("FindPublished() error = %v, want not found", err)
	}
}

func TestAppReleasePublishRejectsInvalidAPKAndMetadata(t *testing.T) {
	service, err := NewAppReleaseService(newMemoryAppReleaseRepository(), t.TempDir(), "com.beacon.parabeacon")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		versionName string
		versionCode int64
		apk         []byte
		want        error
	}{
		{name: "bad version", versionName: "../latest", versionCode: 1, apk: makeTestAPK(t), want: ErrInvalidAppRelease},
		{name: "bad code", versionName: "1.0.0", versionCode: 0, apk: makeTestAPK(t), want: ErrInvalidAppRelease},
		{name: "not apk", versionName: "1.0.0", versionCode: 1, apk: []byte("not an APK"), want: ErrInvalidAPK},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.Publish(context.Background(), test.versionName, test.versionCode, bytes.NewReader(test.apk))
			if !errors.Is(err, test.want) {
				t.Fatalf("Publish() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestAppReleaseRejectsStorageKeyTraversalAndSymlink(t *testing.T) {
	storageRoot := t.TempDir()
	repository := newMemoryAppReleaseRepository()
	now := time.Now().UTC()
	traversal := &model.AppRelease{
		VersionCode: 1,
		PackageName: "com.beacon.parabeacon",
		StorageKey:  "../outside.apk",
		PublishedAt: &now,
	}
	repository.releases[1] = traversal
	service, err := NewAppReleaseService(repository, storageRoot, "com.beacon.parabeacon")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.FindPublished(context.Background(), 1); !errors.Is(err, ErrInvalidAppRelease) {
		t.Fatalf("traversal key error = %v", err)
	}

	outside := filepath.Join(t.TempDir(), "outside.apk")
	if err := os.WriteFile(outside, makeTestAPK(t), 0600); err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString() + ".apk"
	if err := os.Symlink(outside, filepath.Join(storageRoot, key)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	repository.releases[2] = &model.AppRelease{
		VersionCode: 2,
		PackageName: "com.beacon.parabeacon",
		StorageKey:  key,
		SizeBytes:   int64(len(makeTestAPK(t))),
		PublishedAt: &now,
	}
	if _, _, err := service.FindPublished(context.Background(), 2); !errors.Is(err, ErrInvalidAPK) {
		t.Fatalf("symlink key error = %v", err)
	}
}
