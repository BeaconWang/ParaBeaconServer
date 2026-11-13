package service

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/internal/repository"
)

const MaxAPKSizeBytes int64 = 512 << 20
const MaxReleaseNotesBytes = 64 << 10

var (
	ErrInvalidAppRelease = errors.New("invalid app release")
	ErrAPKTooLarge       = errors.New("APK exceeds the maximum upload size")
	ErrInvalidAPK        = errors.New("invalid Android APK")
	versionNamePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]{0,63}$`)
	packageNamePattern   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)+$`)
)

type AppReleaseRepository interface {
	Create(ctx context.Context, release *model.AppRelease) error
	ListPublished(ctx context.Context, packageName string) ([]model.AppRelease, error)
	Latest(ctx context.Context, packageName string) (*model.AppRelease, error)
	FindPublished(ctx context.Context, packageName string, versionCode int64) (*model.AppRelease, error)
	IncrementDownloadCount(ctx context.Context, packageName string, versionCode int64) error
	Delete(ctx context.Context, packageName string, versionCode int64) error
}

type AppReleaseService struct {
	releases    AppReleaseRepository
	storageRoot string
	packageName string
}

func NewAppReleaseService(releases AppReleaseRepository, storageRoot, packageName string) (*AppReleaseService, error) {
	storageRoot = strings.TrimSpace(storageRoot)
	packageName = strings.TrimSpace(packageName)
	if releases == nil || storageRoot == "" || !packageNamePattern.MatchString(packageName) {
		return nil, ErrInvalidAppRelease
	}
	root, err := filepath.Abs(storageRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve APK storage directory: %w", err)
	}
	return &AppReleaseService{releases: releases, storageRoot: root, packageName: packageName}, nil
}

func (s *AppReleaseService) ListPublished(ctx context.Context) ([]model.AppRelease, error) {
	return s.releases.ListPublished(ctx, s.packageName)
}

func (s *AppReleaseService) Latest(ctx context.Context) (*model.AppRelease, error) {
	return s.releases.Latest(ctx, s.packageName)
}

func (s *AppReleaseService) RecordDownload(ctx context.Context, versionCode int64) error {
	if versionCode < 1 || versionCode > 2100000000 {
		return ErrInvalidAppRelease
	}
	return s.releases.IncrementDownloadCount(ctx, s.packageName, versionCode)
}

func (s *AppReleaseService) Publish(ctx context.Context, versionName string, versionCode int64, notes string, source io.Reader) (*model.AppRelease, error) {
	versionName = strings.TrimSpace(versionName)
	notes = strings.TrimSpace(notes)
	if !versionNamePattern.MatchString(versionName) || versionCode < 1 || versionCode > 2100000000 || len(notes) > MaxReleaseNotesBytes || !utf8.ValidString(notes) || source == nil {
		return nil, ErrInvalidAppRelease
	}
	if err := os.MkdirAll(s.storageRoot, 0700); err != nil {
		return nil, fmt.Errorf("create APK storage directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.storageRoot, ".upload-*.apk")
	if err != nil {
		return nil, fmt.Errorf("create temporary APK: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		temporary.Close()
		return nil, fmt.Errorf("secure temporary APK: %w", err)
	}
	hasher := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(temporary, hasher), io.LimitReader(source, MaxAPKSizeBytes+1))
	closeErr := temporary.Close()
	if copyErr != nil {
		return nil, fmt.Errorf("store APK: %w", copyErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close APK: %w", closeErr)
	}
	if size > MaxAPKSizeBytes {
		return nil, ErrAPKTooLarge
	}
	if size == 0 || validateAPK(temporaryPath, size) != nil {
		return nil, ErrInvalidAPK
	}

	now := time.Now().UTC()
	storageKey := uuid.NewString() + ".apk"
	finalPath := filepath.Join(s.storageRoot, storageKey)
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return nil, fmt.Errorf("publish APK file: %w", err)
	}
	release := &model.AppRelease{
		VersionCode: versionCode,
		VersionName: versionName,
		PackageName: s.packageName,
		StorageKey:  storageKey,
		SHA256:      hex.EncodeToString(hasher.Sum(nil)),
		SizeBytes:   size,
		Notes:       notes,
		PublishedAt: &now,
	}
	if err := s.releases.Create(ctx, release); err != nil {
		if removeErr := os.Remove(finalPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return nil, fmt.Errorf("save app release: %w (remove orphan APK: %v)", err, removeErr)
		}
		return nil, err
	}
	return release, nil
}

func validateAPK(path string, size int64) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	archive, err := zip.NewReader(file, size)
	if err != nil {
		return err
	}
	for _, entry := range archive.File {
		if entry.Name == "AndroidManifest.xml" && !entry.FileInfo().IsDir() && entry.UncompressedSize64 > 0 {
			return nil
		}
	}
	return ErrInvalidAPK
}

func (s *AppReleaseService) Delete(ctx context.Context, versionCode int64) error {
	if versionCode < 1 || versionCode > 2100000000 {
		return ErrInvalidAppRelease
	}
	release, path, err := s.FindPublished(ctx, versionCode)
	if err != nil {
		return err
	}
	tombstone := filepath.Join(s.storageRoot, ".delete-"+uuid.NewString()+".apk")
	if err := os.Rename(path, tombstone); err != nil {
		return fmt.Errorf("stage APK deletion: %w", err)
	}
	if err := s.releases.Delete(ctx, s.packageName, release.VersionCode); err != nil {
		if restoreErr := os.Rename(tombstone, path); restoreErr != nil {
			return fmt.Errorf("delete app release: %w (restore APK: %v)", err, restoreErr)
		}
		return err
	}
	if err := os.Remove(tombstone); err != nil && !errors.Is(err, os.ErrNotExist) {
		restoreDBErr := s.releases.Create(ctx, release)
		restoreFileErr := os.Rename(tombstone, path)
		if restoreDBErr != nil || restoreFileErr != nil {
			return fmt.Errorf("remove APK: %w (restore metadata: %v; restore APK: %v)", err, restoreDBErr, restoreFileErr)
		}
		return fmt.Errorf("remove APK: %w", err)
	}
	return nil
}

func (s *AppReleaseService) FindPublished(ctx context.Context, versionCode int64) (*model.AppRelease, string, error) {
	if versionCode < 1 || versionCode > 2100000000 {
		return nil, "", ErrInvalidAppRelease
	}
	release, err := s.releases.FindPublished(ctx, s.packageName, versionCode)
	if err != nil {
		return nil, "", err
	}
	if _, err := uuid.Parse(strings.TrimSuffix(release.StorageKey, ".apk")); err != nil || filepath.Base(release.StorageKey) != release.StorageKey || !strings.HasSuffix(release.StorageKey, ".apk") {
		return nil, "", ErrInvalidAppRelease
	}
	path := filepath.Join(s.storageRoot, release.StorageKey)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, "", fmt.Errorf("open published APK: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() != release.SizeBytes {
		return nil, "", ErrInvalidAPK
	}
	return release, path, nil
}

func IsAppReleaseNotFound(err error) bool {
	return errors.Is(err, repository.ErrAppReleaseNotFound)
}
