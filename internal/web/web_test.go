package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerDisablesCaching(t *testing.T) {
	request := httptest.NewRequest("GET", "/", nil)
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("status = %d", response.Code)
	}
	for header, want := range map[string]string{
		"Cache-Control": "no-store, no-cache, must-revalidate",
		"Pragma":        "no-cache",
		"Expires":       "0",
	} {
		if got := response.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestLiveHandlerServesLiveMapWithSecurityPolicy(t *testing.T) {
	request := httptest.NewRequest("GET", "/live", nil)
	response := httptest.NewRecorder()
	LiveHandler().ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("status = %d", response.Code)
	}
	if got := response.Header().Get("Content-Security-Policy"); got == "" {
		t.Fatal("live page has no content security policy")
	}
	if !strings.Contains(response.Body.String(), "Public flight tracking") {
		t.Fatal("live page does not contain its title")
	}
}
