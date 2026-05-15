package main

import (
	"denkit-stash/internal/testdb"
	"denkit-stash/models"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humamux"
	"github.com/gorilla/mux"
)

func newTestMainDatabase(t *testing.T) models.Database {
	t.Helper()
	return testdb.New(t)
}

func TestInitializeMinIORequiresExplicitConnectionSettings(t *testing.T) {
	t.Setenv("MINIO_ENDPOINT", "")
	t.Setenv("MINIO_ACCESS_KEY", "")
	t.Setenv("MINIO_SECRET_KEY", "")
	t.Setenv("MINIO_BUCKET", "")

	if _, _, err := initializeMinIO(); err == nil {
		t.Fatal("expected missing MinIO endpoint to fail")
	}

	t.Setenv("MINIO_ENDPOINT", "localhost:9000")
	if _, _, err := initializeMinIO(); err == nil {
		t.Fatal("expected missing MinIO access key to fail")
	}

	t.Setenv("MINIO_ACCESS_KEY", "access")
	if _, _, err := initializeMinIO(); err == nil {
		t.Fatal("expected missing MinIO secret key to fail")
	}

	t.Setenv("MINIO_SECRET_KEY", "secret")
	if _, _, err := initializeMinIO(); err == nil {
		t.Fatal("expected missing MinIO bucket to fail")
	}
}

func TestDevEndpointsDisabledByDefault(t *testing.T) {
	t.Setenv("ENABLE_DEV_ENDPOINTS", "")

	if devEndpointsEnabled() {
		t.Fatal("expected development endpoints to be disabled by default")
	}
}

func TestDevEndpointsRequireExplicitTrue(t *testing.T) {
	t.Setenv("ENABLE_DEV_ENDPOINTS", "false")
	if devEndpointsEnabled() {
		t.Fatal("expected false value to keep development endpoints disabled")
	}

	t.Setenv("ENABLE_DEV_ENDPOINTS", "true")
	if !devEndpointsEnabled() {
		t.Fatal("expected true value to enable development endpoints")
	}
}

func TestHTTPServerHasTimeouts(t *testing.T) {
	t.Setenv("DENKIT_HTTP_READ_HEADER_TIMEOUT", "")
	t.Setenv("DENKIT_HTTP_READ_TIMEOUT", "")
	t.Setenv("DENKIT_HTTP_WRITE_TIMEOUT", "")
	t.Setenv("DENKIT_HTTP_IDLE_TIMEOUT", "")

	server := newHTTPServer("127.0.0.1:0", http.NewServeMux())

	if server.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("unexpected read header timeout: %s", server.ReadHeaderTimeout)
	}
	if server.ReadTimeout != 30*time.Minute {
		t.Fatalf("unexpected read timeout: %s", server.ReadTimeout)
	}
	if server.WriteTimeout != 30*time.Minute {
		t.Fatalf("unexpected write timeout: %s", server.WriteTimeout)
	}
	if server.IdleTimeout != 2*time.Minute {
		t.Fatalf("unexpected idle timeout: %s", server.IdleTimeout)
	}
}

func TestHTTPServerTimeoutsCanBeOverridden(t *testing.T) {
	t.Setenv("DENKIT_HTTP_READ_HEADER_TIMEOUT", "7s")
	t.Setenv("DENKIT_HTTP_READ_TIMEOUT", "8m")
	t.Setenv("DENKIT_HTTP_WRITE_TIMEOUT", "9m")
	t.Setenv("DENKIT_HTTP_IDLE_TIMEOUT", "10s")

	server := newHTTPServer("127.0.0.1:0", http.NewServeMux())

	if server.ReadHeaderTimeout != 7*time.Second {
		t.Fatalf("unexpected read header timeout: %s", server.ReadHeaderTimeout)
	}
	if server.ReadTimeout != 8*time.Minute {
		t.Fatalf("unexpected read timeout: %s", server.ReadTimeout)
	}
	if server.WriteTimeout != 9*time.Minute {
		t.Fatalf("unexpected write timeout: %s", server.WriteTimeout)
	}
	if server.IdleTimeout != 10*time.Second {
		t.Fatalf("unexpected idle timeout: %s", server.IdleTimeout)
	}
}

func TestRequestLogTargetRedactsQueryString(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/wharf/status?api_key=secret&x=1", nil)

	if got := requestLogTarget(req); got != "/wharf/status?<redacted>" {
		t.Fatalf("expected redacted request target, got %q", got)
	}
}

func TestAPIKeysAreStoredAsDigests(t *testing.T) {
	db := newTestMainDatabase(t)
	defer db.Close()

	const rawAPIKey = "TESTUSER-SECRET-API-KEY"
	user := &models.User{Username: "keytest", DisplayName: "Key Test", APIKey: rawAPIKey, Role: "user", IsActive: true}
	if err := db.CreateUser(user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	authenticatedUser, err := db.GetUserByAPIKey(rawAPIKey)
	if err != nil {
		t.Fatalf("authenticate with raw API key: %v", err)
	}
	if authenticatedUser.Username != user.Username {
		t.Fatalf("expected authenticated user %q, got %q", user.Username, authenticatedUser.Username)
	}
	if authenticatedUser.APIKey == rawAPIKey {
		t.Fatal("expected stored API key value not to equal raw API key")
	}
	if !models.IsAPIKeyDigest(authenticatedUser.APIKey) {
		t.Fatalf("expected stored API key digest, got %q", authenticatedUser.APIKey)
	}

	users, err := db.ListUsers()
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	for _, listedUser := range users {
		if listedUser.Username == user.Username && listedUser.APIKey == rawAPIKey {
			t.Fatal("list users exposed raw API key")
		}
	}
}

func TestDevMinIOTestRouteRequiresAuthentication(t *testing.T) {
	db := newTestMainDatabase(t)
	defer db.Close()

	router, api := newTestHumaRouter()
	registerRaw[minioTestResponse](api, authOperation("dev-minio-test", http.MethodGet, "/test/minio", "Development-only MinIO smoke test", "Development", 401, 500), authHandler(db, devMinIOTestHandler(nil, "test-bucket")))

	req := httptest.NewRequest(http.MethodGet, "/test/minio", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated MinIO test route to return 401, got %d", rec.Code)
	}
}

func TestDevCredentialMintingRoutesAreNotRegisteredByDefault(t *testing.T) {
	t.Setenv("ENABLE_DEV_ENDPOINTS", "")

	db := newTestMainDatabase(t)
	defer db.Close()

	router, api := newTestHumaRouter()
	registerDevRoutes(api, db, nil, "test-bucket")

	for _, path := range []string{"/oauth/authorize", "/user/oauth", "/test/minio"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected %s to be absent from production-default router, got %d", path, rec.Code)
		}
	}
}

func TestDevOAuthRejectsExternalRedirectURI(t *testing.T) {
	db := newTestMainDatabase(t)
	defer db.Close()

	router, api := newTestHumaRouter()
	registerRawOperation(api, noSecurityOperation("dev-oauth-authorize", http.MethodGet, "/oauth/authorize", "Development-only OAuth compatibility redirect", "Development", 400), devOAuthHandler(db), redirectResponses())

	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?client_id=butler&redirect_uri=https://attacker.example/cb", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected external redirect URI to return 400, got %d", rec.Code)
	}
}

func TestDevOAuthDoesNotFallbackToUserIDOne(t *testing.T) {
	db := newTestMainDatabase(t)
	defer db.Close()

	admin := &models.User{Username: "admin", DisplayName: "Admin", APIKey: "ADMIN-SECRET-API-KEY", Role: "admin", IsActive: true}
	if err := db.CreateUser(admin); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	testUser := &models.User{Username: "testuser", DisplayName: "Test User", APIKey: "TESTUSER-API-KEY", Role: "user", IsActive: true}
	if err := db.CreateUser(testUser); err != nil {
		t.Fatalf("create existing test user: %v", err)
	}

	router, api := newTestHumaRouter()
	registerRawOperation(api, noSecurityOperation("dev-oauth-authorize", http.MethodGet, "/oauth/authorize", "Development-only OAuth compatibility redirect", "Development", 400), devOAuthHandler(db), redirectResponses())

	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?client_id=butler&redirect_uri=http://localhost:12345/callback", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected duplicate testuser to fail closed, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), admin.APIKey) || strings.Contains(rec.Header().Get("Location"), admin.APIKey) {
		t.Fatal("OAuth handler leaked user id 1 API key")
	}
}

func newTestHumaRouter() (*mux.Router, huma.API) {
	router := mux.NewRouter()
	return router, humamux.New(router, newAPIConfig())
}

func redirectResponses() map[int]reflect.Type {
	return map[int]reflect.Type{http.StatusFound: reflect.TypeOf("")}
}

func TestParseDevOAuthRedirectURIOnlyAllowsLocalhostHTTP(t *testing.T) {
	allowed := []string{
		"http://localhost:12345/callback",
		"http://127.0.0.1:12345/callback",
		"http://[::1]:12345/callback",
	}
	for _, rawURL := range allowed {
		if _, ok := parseDevOAuthRedirectURI(rawURL); !ok {
			t.Fatalf("expected %q to be allowed", rawURL)
		}
	}

	blocked := []string{
		"https://localhost:12345/callback",
		"http://attacker.example/callback",
		"http://127.0.0.2:12345/callback",
		"not a url",
	}
	for _, rawURL := range blocked {
		if _, ok := parseDevOAuthRedirectURI(rawURL); ok {
			t.Fatalf("expected %q to be blocked", rawURL)
		}
	}
}

func TestDevOAuthRedirectURLUsesFragmentWithoutHTMLOrJavaScript(t *testing.T) {
	redirectURL, ok := parseDevOAuthRedirectURI("http://localhost:12345/callback?state=abc")
	if !ok {
		t.Fatal("expected localhost redirect URI to parse")
	}

	got := devOAuthRedirectURL(redirectURL, "test-api-key")
	want := "http://localhost:12345/callback?state=abc#access_token=test-api-key"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
