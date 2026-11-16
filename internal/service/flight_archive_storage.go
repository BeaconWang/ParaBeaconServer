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
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	MaxFlightArchiveBytes             = 24 << 20
	MaxFlightArchiveUncompressedBytes = 18 << 20
)

var ErrInvalidFlightArchive = errors.New("invalid flight archive")

type FlightArchiveStorage struct {
	root string
}

func NewFlightArchiveStorage(root string) (*FlightArchiveStorage, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("flight archive storage directory is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve flight archive storage directory: %w", err)
	}
	if err := os.MkdirAll(absolute, 0700); err != nil {
		return nil, fmt.Errorf("create flight archive storage directory: %w", err)
	}
	if err := os.Chmod(absolute, 0700); err != nil {
		return nil, fmt.Errorf("secure flight archive storage directory: %w", err)
	}
	storage := &FlightArchiveStorage{root: absolute}
	stagingRoot := filepath.Join(absolute, ".staging")
	if err := os.MkdirAll(stagingRoot, 0700); err != nil {
		return nil, fmt.Errorf("create flight archive staging directory: %w", err)
	}
	stagingInfo, err := os.Lstat(stagingRoot)
	if err != nil || !stagingInfo.IsDir() || stagingInfo.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalidFlightArchive
	}
	return storage, nil
}

func (s *FlightArchiveStorage) Stage(ctx context.Context, syncID, archiveID uuid.UUID, source io.Reader) ([]byte, int64, string, error) {
	if syncID == uuid.Nil || archiveID == uuid.Nil || source == nil {
		return nil, 0, "", ErrInvalidFlightArchive
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, "", err
	}
	stageDir := filepath.Join(s.root, ".staging", syncID.String())
	if err := os.MkdirAll(stageDir, 0700); err != nil {
		return nil, 0, "", fmt.Errorf("create flight archive staging directory: %w", err)
	}
	stageInfo, err := os.Lstat(stageDir)
	if err != nil || !stageInfo.IsDir() || stageInfo.Mode()&os.ModeSymlink != 0 {
		return nil, 0, "", ErrInvalidFlightArchive
	}
	if err := os.Chmod(stageDir, 0700); err != nil {
		return nil, 0, "", fmt.Errorf("secure flight archive staging directory: %w", err)
	}
	temporary, err := os.CreateTemp(stageDir, ".upload-*.tmp")
	if err != nil {
		return nil, 0, "", fmt.Errorf("create temporary flight archive: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		temporary.Close()
		return nil, 0, "", fmt.Errorf("secure temporary flight archive: %w", err)
	}
	hasher := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(temporary, hasher), io.LimitReader(source, MaxFlightArchiveBytes+1))
	closeErr := temporary.Close()
	if copyErr != nil {
		if size >= MaxFlightArchiveBytes {
			return nil, 0, "", ErrInvalidFlightArchive
		}
		return nil, 0, "", fmt.Errorf("store staged flight archive: %w", copyErr)
	}
	if closeErr != nil {
		return nil, 0, "", fmt.Errorf("close staged flight archive: %w", closeErr)
	}
	if size == 0 || size > MaxFlightArchiveBytes {
		return nil, 0, "", ErrInvalidFlightArchive
	}
	flightJSON, err := readFlightArchive(temporaryPath, size)
	if err != nil {
		return nil, 0, "", err
	}
	stagedPath := s.stagedPath(syncID, archiveID)
	if err := os.Link(temporaryPath, stagedPath); err != nil {
		return nil, 0, "", fmt.Errorf("publish staged flight archive: %w", err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		_ = os.Remove(stagedPath)
		return nil, 0, "", fmt.Errorf("remove temporary flight archive: %w", err)
	}
	return flightJSON, size, hex.EncodeToString(hasher.Sum(nil)), nil
}

func (s *FlightArchiveStorage) Publish(syncID, archiveID uuid.UUID) error {
	if syncID == uuid.Nil || archiveID == uuid.Nil {
		return ErrInvalidFlightArchive
	}
	stagedPath := s.stagedPath(syncID, archiveID)
	finalPath := s.archivePath(archiveID)
	if finalInfo, err := os.Lstat(finalPath); err == nil {
		if !finalInfo.Mode().IsRegular() || finalInfo.Size() <= 0 || finalInfo.Size() > MaxFlightArchiveBytes {
			return ErrInvalidFlightArchive
		}
		_, err := readFlightArchive(finalPath, finalInfo.Size())
		return err
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect final flight archive: %w", err)
	}

	stagedInfo, err := os.Lstat(stagedPath)
	if err != nil || !stagedInfo.Mode().IsRegular() {
		return ErrInvalidFlightArchive
	}
	source, err := os.Open(stagedPath)
	if err != nil {
		return fmt.Errorf("open staged flight archive: %w", err)
	}
	defer source.Close()
	temporary, err := os.CreateTemp(s.root, ".publish-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary published flight archive: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		temporary.Close()
		return err
	}
	_, copyErr := io.Copy(temporary, source)
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil {
		return errors.Join(copyErr, syncErr, closeErr)
	}
	if err := os.Link(temporaryPath, finalPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			finalInfo, statErr := os.Lstat(finalPath)
			if statErr == nil && finalInfo.Mode().IsRegular() {
				_, validateErr := readFlightArchive(finalPath, finalInfo.Size())
				return validateErr
			}
		}
		return fmt.Errorf("publish flight archive: %w", err)
	}
	return nil
}

func (s *FlightArchiveStorage) Read(archiveID uuid.UUID) ([]byte, error) {
	if archiveID == uuid.Nil {
		return nil, ErrInvalidFlightArchive
	}
	path := s.archivePath(archiveID)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("open flight archive: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > MaxFlightArchiveBytes {
		return nil, ErrInvalidFlightArchive
	}
	return readFlightArchive(path, info.Size())
}

func readFlightArchive(path string, size int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open flight archive: %w", err)
	}
	defer file.Close()
	archive, err := zip.NewReader(file, size)
	if err != nil || len(archive.File) != 1 {
		return nil, ErrInvalidFlightArchive
	}
	entry := archive.File[0]
	if entry.Name != "flight.json" || !entry.FileInfo().Mode().IsRegular() || entry.UncompressedSize64 == 0 || entry.UncompressedSize64 > MaxFlightArchiveUncompressedBytes {
		return nil, ErrInvalidFlightArchive
	}
	reader, err := entry.Open()
	if err != nil {
		return nil, ErrInvalidFlightArchive
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, MaxFlightArchiveUncompressedBytes+1))
	if err != nil || len(data) == 0 || len(data) > MaxFlightArchiveUncompressedBytes {
		return nil, ErrInvalidFlightArchive
	}
	return data, nil
}

func (s *FlightArchiveStorage) RemoveStaged(syncID, archiveID uuid.UUID) error {
	if syncID == uuid.Nil || archiveID == uuid.Nil {
		return ErrInvalidFlightArchive
	}
	if err := os.Remove(s.stagedPath(syncID, archiveID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove staged flight archive: %w", err)
	}
	return nil
}

func (s *FlightArchiveStorage) Remove(archiveID uuid.UUID) error {
	if archiveID == uuid.Nil {
		return ErrInvalidFlightArchive
	}
	if err := os.Remove(s.archivePath(archiveID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove flight archive: %w", err)
	}
	return nil
}

func (s *FlightArchiveStorage) CleanupSession(syncID uuid.UUID) error {
	if syncID == uuid.Nil {
		return ErrInvalidFlightArchive
	}
	path := filepath.Join(s.root, ".staging", syncID.String())
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("clean flight archive staging directory: %w", err)
	}
	return nil
}

func (s *FlightArchiveStorage) CleanupExpiredStages(cutoff time.Time) error {
	root := filepath.Join(s.root, ".staging")
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		syncID, err := uuid.Parse(entry.Name())
		if err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := s.CleanupSession(syncID); err != nil {
			return err
		}
	}
	return nil
}

func (s *FlightArchiveStorage) stagedPath(syncID, archiveID uuid.UUID) string {
	return filepath.Join(s.root, ".staging", syncID.String(), archiveID.String()+".flight.zip")
}

func (s *FlightArchiveStorage) archivePath(archiveID uuid.UUID) string {
	return filepath.Join(s.root, archiveID.String()+".flight.zip")
}
