package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractAPIKeyAcceptsQueryStringCredentialsForCompatibility(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/protected?api_key=secret", nil)

	if got := extractAPIKey(req); got != "secret" {
		t.Fatalf("expected query string API key, got %q", got)
	}
}

func TestExtractAPIKeyAcceptsAuthorizationBearer(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer secret")

	if got := extractAPIKey(req); got != "secret" {
		t.Fatalf("expected bearer token, got %q", got)
	}
}
