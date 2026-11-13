// Package integration holds live API tests that run from the developer's
// machine against a deployed ParaBeacon server.
//
// Run with:
//
//	PARABEACON_BASE_URL=https://www.parabeacon.icu/ go test ./tests/integration -v -count=1
//
// The tests are skipped when PARABEACON_BASE_URL is unset, so `go test ./...`
// stays offline-safe. Registration has no delete endpoint: accounts created
// here use the "zz_liveauth_" prefix and must be cleaned up externally.
package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

const accountPrefix = "zz_liveauth_"

type apiResponse struct {
	status int
	body   []byte
}

func liveBase(t *testing.T) string {
	t.Helper()
	base := strings.TrimRight(os.Getenv("PARABEACON_BASE_URL"), "/")
	if base == "" {
		t.Skip("PARABEACON_BASE_URL is unset; skipping live integration tests (e.g. https://www.parabeacon.icu/)")
	}
	return base
}

func call(t *testing.T, base, method, path string, payload any, token string) apiResponse {
	t.Helper()
	var reader io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, base+path, reader)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, path, err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s failed: %v", method, path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s body: %v", method, path, err)
	}
	return apiResponse{status: resp.StatusCode, body: body}
}

func wantStatus(t *testing.T, name string, r apiResponse, want int) bool {
	t.Helper()
	if r.status == want {
		t.Logf("%s: HTTP %d", name, r.status)
		return true
	}
	t.Errorf("%s: HTTP %d, want %d; body=%s", name, r.status, want, string(r.body))
	return false
}

func asMap(t *testing.T, r apiResponse) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(r.body, &out); err != nil {
		t.Fatalf("decode JSON (HTTP %d): %v; body=%s", r.status, err, string(r.body))
	}
	return out
}

func userMap(t *testing.T, r apiResponse) map[string]any {
	t.Helper()
	user, ok := asMap(t, r)["user"].(map[string]any)
	if !ok {
		t.Fatalf("response missing user object: %s", string(r.body))
	}
	return user
}

func TestAuthFlowLive(t *testing.T) {
	base := liveBase(t)
	// Keep the generated username within the handler's max=32 validation.
	suffix := fmt.Sprintf("%d", time.Now().Unix())
	username := accountPrefix + suffix
	usernameNoEmail := username + "_b"
	email := username + "@example.com"
	password := "A-long-unique-passphrase-2026"

	t.Logf("target: %s", base)
	defer t.Logf("created accounts (no delete endpoint): %s, %s", username, usernameNoEmail)

	var userID, accessToken string

	t.Run("liveness", func(t *testing.T) {
		wantStatus(t, "GET /healthz", call(t, base, http.MethodGet, "/healthz", nil, ""), http.StatusOK)
		wantStatus(t, "GET /readyz", call(t, base, http.MethodGet, "/readyz", nil, ""), http.StatusOK)
	})

	t.Run("register_with_email", func(t *testing.T) {
		r := call(t, base, http.MethodPost, "/api/v1/auth/register",
			map[string]string{"username": username, "email": email, "password": password}, "")
		if !wantStatus(t, "register", r, http.StatusCreated) {
			return
		}
		user := userMap(t, r)
		if user["username"] != username {
			t.Errorf("username = %v, want %q", user["username"], username)
		}
		if user["email"] != email {
			t.Errorf("email = %v, want %q (normalized)", user["email"], email)
		}
		if _, leaked := user["password_hash"]; leaked {
			t.Error("response leaked password_hash")
		}
		userID, _ = user["id"].(string)
		if userID == "" {
			t.Error("response missing user.id")
		}
	})

	t.Run("duplicate_register", func(t *testing.T) {
		wantStatus(t, "duplicate register", call(t, base, http.MethodPost, "/api/v1/auth/register",
			map[string]string{"username": username, "email": email, "password": password}, ""), http.StatusConflict)
	})

	t.Run("register_without_optional_email", func(t *testing.T) {
		r := call(t, base, http.MethodPost, "/api/v1/auth/register",
			map[string]string{"username": usernameNoEmail, "password": password}, "")
		if wantStatus(t, "register (no email)", r, http.StatusCreated) {
			if user := userMap(t, r); user["email"] != nil {
				t.Errorf("email = %v, want omitted", user["email"])
			}
		}
	})

	t.Run("register_invalid_email", func(t *testing.T) {
		wantStatus(t, "register (invalid email)", call(t, base, http.MethodPost, "/api/v1/auth/register",
			map[string]string{"username": username + "_c", "email": "not-an-email", "password": password}, ""), http.StatusBadRequest)
	})

	t.Run("login_by_email", func(t *testing.T) {
		r := call(t, base, http.MethodPost, "/api/v1/auth/login",
			map[string]string{"identifier": email, "password": password}, "")
		if !wantStatus(t, "login (email)", r, http.StatusOK) {
			return
		}
		data := asMap(t, r)
		accessToken, _ = data["access_token"].(string)
		if accessToken == "" || data["token_type"] != "Bearer" {
			t.Errorf("token payload invalid: %v", data)
		}
		if user, ok := data["user"].(map[string]any); ok && user["id"] != userID {
			t.Errorf("login user.id = %v, want %q", user["id"], userID)
		}
	})

	t.Run("login_by_username_case_insensitive", func(t *testing.T) {
		wantStatus(t, "login (uppercase username)", call(t, base, http.MethodPost, "/api/v1/auth/login",
			map[string]string{"identifier": strings.ToUpper(username), "password": password}, ""), http.StatusOK)
	})

	t.Run("login_legacy_email_field", func(t *testing.T) {
		wantStatus(t, "login (legacy email field)", call(t, base, http.MethodPost, "/api/v1/auth/login",
			map[string]string{"email": email, "password": password}, ""), http.StatusOK)
	})

	t.Run("login_wrong_password", func(t *testing.T) {
		wantStatus(t, "login (wrong password)", call(t, base, http.MethodPost, "/api/v1/auth/login",
			map[string]string{"identifier": email, "password": "incorrect-password"}, ""), http.StatusUnauthorized)
	})

	t.Run("profile_with_token", func(t *testing.T) {
		if accessToken == "" {
			t.Skip("no access token from login")
		}
		r := call(t, base, http.MethodGet, "/api/v1/user/profile", nil, accessToken)
		if wantStatus(t, "profile (bearer)", r, http.StatusOK) {
			if user := userMap(t, r); user["id"] != userID {
				t.Errorf("profile user.id = %v, want %q", user["id"], userID)
			}
		}
	})

	t.Run("profile_without_token", func(t *testing.T) {
		wantStatus(t, "profile (no token)", call(t, base, http.MethodGet, "/api/v1/user/profile", nil, ""), http.StatusUnauthorized)
		wantStatus(t, "profile (garbage token)", call(t, base, http.MethodGet, "/api/v1/user/profile", nil, "not-a-jwt"), http.StatusUnauthorized)
	})
}
