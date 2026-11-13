package web

import (
	"net/http/httptest"
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
