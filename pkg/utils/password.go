package utils

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

const MaxPasswordBytes = 72

func HashPassword(password string) (string, error) {
	if len(password) > MaxPasswordBytes {
		return "", fmt.Errorf("password exceeds bcrypt's %d-byte limit", MaxPasswordBytes)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

func VerifyPassword(hash, password string) error {
	if len(password) > MaxPasswordBytes {
		return bcrypt.ErrMismatchedHashAndPassword
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}
