package utils

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTokenManagerIssueAndParse(t *testing.T) {
	manager := NewTokenManager("0123456789abcdef0123456789abcdef", "test-issuer", time.Minute)
	userID := uuid.New()
	raw, expiresAt, err := manager.Issue(userID)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	claims, err := manager.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if claims.Subject != userID.String() {
		t.Fatalf("subject = %q, want %q", claims.Subject, userID)
	}
	if !expiresAt.After(time.Now()) {
		t.Fatal("issued token expiration is not in the future")
	}

	wrongKey := NewTokenManager("abcdef0123456789abcdef0123456789", "test-issuer", time.Minute)
	if _, err := wrongKey.Parse(raw); err == nil {
		t.Fatal("Parse() accepted a token signed with a different key")
	}
}

func TestTokenManagerRejectsExpiredToken(t *testing.T) {
	manager := NewTokenManager("0123456789abcdef0123456789abcdef", "test-issuer", -time.Minute)
	raw, _, err := manager.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if _, err := manager.Parse(raw); err == nil {
		t.Fatal("Parse() accepted an expired token")
	}
}
