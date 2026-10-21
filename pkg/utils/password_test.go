package utils

import (
	"strings"
	"testing"
)

func TestHashPasswordAndVerify(t *testing.T) {
	password := "a-long-password-for-test"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if hash == password || !strings.HasPrefix(hash, "$2") {
		t.Fatal("password was not stored as a bcrypt hash")
	}
	if err := VerifyPassword(hash, password); err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}
	if err := VerifyPassword(hash, "wrong-password"); err == nil {
		t.Fatal("VerifyPassword() accepted an incorrect password")
	}
}

func TestHashPasswordRejectsOverLimit(t *testing.T) {
	if _, err := HashPassword(strings.Repeat("x", MaxPasswordBytes+1)); err == nil {
		t.Fatal("HashPassword() accepted an input over bcrypt's byte limit")
	}
}
