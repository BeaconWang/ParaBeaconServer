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

func TestReleasePageSupportsBeaconAccountPublishing(t *testing.T) {
	request := httptest.NewRequest("GET", "/release", nil)
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("status = %d", response.Code)
	}
	for _, marker := range []string{"data-auth-widget", "publishAuthHint", "ParaBeaconAuth.authHeaders"} {
		if !strings.Contains(response.Body.String(), marker) {
			t.Errorf("release page does not contain %q", marker)
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

func TestI18nScriptHandlerServesJavaScript(t *testing.T) {
	request := httptest.NewRequest("GET", "/assets/i18n.js", nil)
	response := httptest.NewRecorder()
	I18nScriptHandler().ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("status = %d", response.Code)
	}
	if got := response.Header().Get("Content-Type"); got != "application/javascript; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
	if !strings.Contains(response.Body.String(), "parabeacon-language") {
		t.Fatal("i18n script does not persist the language preference")
	}
}

func TestAuthScriptHandlerServesJavaScript(t *testing.T) {
	request := httptest.NewRequest("GET", "/assets/auth.js", nil)
	response := httptest.NewRecorder()
	AuthScriptHandler().ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("status = %d", response.Code)
	}
	if got := response.Header().Get("Content-Type"); got != "application/javascript; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
	if !strings.Contains(response.Body.String(), "ParaBeaconAuth") {
		t.Fatal("auth script does not expose the shared auth API")
	}
}

func TestHomeHandlerServesGuideWithSecurityPolicy(t *testing.T) {
	request := httptest.NewRequest("GET", "/", nil)
	response := httptest.NewRecorder()
	HomeHandler().ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("status = %d", response.Code)
	}
	if got := response.Header().Get("Content-Security-Policy"); got == "" {
		t.Fatal("home page has no content security policy")
	}
	for _, marker := range []string{"ParaBeacon", "homePanelTitle", "homeFeatureOneTitle"} {
		if !strings.Contains(response.Body.String(), marker) {
			t.Errorf("home page does not contain %q", marker)
		}
	}
}
