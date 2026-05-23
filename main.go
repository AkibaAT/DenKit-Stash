package main

import (
	"context"
	"denkit-stash/auth"
	"denkit-stash/handlers"
	"denkit-stash/models"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humamux"
	"github.com/gorilla/mux"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func initializeMinIO() (*minio.Client, string, error) {
	endpoint := os.Getenv("MINIO_ENDPOINT")
	accessKey := os.Getenv("MINIO_ACCESS_KEY")
	secretKey := os.Getenv("MINIO_SECRET_KEY")
	bucketName := os.Getenv("MINIO_BUCKET")
	useSSL := getEnvOrDefault("MINIO_USE_SSL", "false") == "true"

	if endpoint == "" {
		return nil, "", fmt.Errorf("MINIO_ENDPOINT environment variable is required")
	}
	if accessKey == "" {
		return nil, "", fmt.Errorf("MINIO_ACCESS_KEY environment variable is required")
	}
	if secretKey == "" {
		return nil, "", fmt.Errorf("MINIO_SECRET_KEY environment variable is required")
	}
	if bucketName == "" {
		return nil, "", fmt.Errorf("MINIO_BUCKET environment variable is required")
	}

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, "", fmt.Errorf("failed to create MinIO client: %v", err)
	}

	ctx := context.Background()
	exists, err := client.BucketExists(ctx, bucketName)
	if err != nil {
		return nil, "", fmt.Errorf("failed to check if bucket exists: %v", err)
	}

	if !exists {
		err = client.MakeBucket(ctx, bucketName, minio.MakeBucketOptions{})
		if err != nil {
			return nil, "", fmt.Errorf("failed to create bucket: %v", err)
		}
		fmt.Printf("Created MinIO bucket: %s\n", bucketName)
	}

	if err = client.SetBucketPolicy(ctx, bucketName, ""); err != nil {
		return nil, "", fmt.Errorf("failed to enforce private bucket policy: %v", err)
	}

	return client, bucketName, nil
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func devEndpointsEnabled() bool {
	return os.Getenv("ENABLE_DEV_ENDPOINTS") == "true"
}

func requestLogTarget(req *http.Request) string {
	if req.URL == nil {
		return ""
	}
	if req.URL.RawQuery == "" {
		return req.URL.Path
	}
	return req.URL.Path + "?<redacted>"
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		fmt.Printf("Ignoring invalid %s=%q; using %s\n", key, value, fallback)
		return fallback
	}
	return parsed
}

func newHTTPServer(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: envDuration("DENKIT_HTTP_READ_HEADER_TIMEOUT", 5*time.Second),
		ReadTimeout:       envDuration("DENKIT_HTTP_READ_TIMEOUT", 30*time.Minute),
		WriteTimeout:      envDuration("DENKIT_HTTP_WRITE_TIMEOUT", 30*time.Minute),
		IdleTimeout:       envDuration("DENKIT_HTTP_IDLE_TIMEOUT", 2*time.Minute),
	}
}

func devMinIOTestHandler(minioClient *minio.Client, bucketName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		testContent := "Hello from MinIO! This is a test file."
		objectName := "test/hello.txt"

		ctx := context.Background()
		_, err := minioClient.PutObject(ctx, bucketName, objectName, strings.NewReader(testContent), int64(len(testContent)), minio.PutObjectOptions{
			ContentType: "text/plain",
		})
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to upload test file: %v", err), http.StatusInternalServerError)
			return
		}

		signedURL, err := minioClient.PresignedGetObject(ctx, bucketName, objectName, time.Hour, nil)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to generate signed URL: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"message":      "Test file uploaded successfully",
			"signed_url":   signedURL.String(),
			"expires_in":   "1 hour",
			"test_content": testContent,
		})
	}
}

func parseDevOAuthRedirectURI(rawRedirectURI string) (*url.URL, bool) {
	redirectURL, err := url.Parse(rawRedirectURI)
	if err != nil || redirectURL.Scheme != "http" {
		return nil, false
	}

	host := redirectURL.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return nil, false
	}

	return redirectURL, true
}

func devOAuthRedirectURL(redirectURL *url.URL, apiKey string) string {
	redirectURL.Fragment = "access_token=" + apiKey
	return redirectURL.String()
}

func devOAuthHandler(db models.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clientID := r.URL.Query().Get("client_id")
		if clientID != "butler" {
			http.Error(w, "Invalid client_id", http.StatusBadRequest)
			return
		}

		redirectURI := r.URL.Query().Get("redirect_uri")
		if redirectURI == "" {
			http.Error(w, "Missing redirect_uri", http.StatusBadRequest)
			return
		}

		parsedRedirectURL, ok := parseDevOAuthRedirectURI(redirectURI)
		if !ok {
			http.Error(w, "Invalid redirect_uri", http.StatusBadRequest)
			return
		}

		user, err := auth.CreateTestUser(db, "testuser")
		if err != nil {
			http.Error(w, "OAuth test user unavailable", http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, devOAuthRedirectURL(parsedRedirectURL, user.APIKey), http.StatusFound)
	}
}

func registerDevRoutes(api huma.API, db models.Database, minioClient *minio.Client, bucketName string) {
	if !devEndpointsEnabled() {
		return
	}

	fmt.Println("Development endpoints enabled")
	registerRaw[minioTestResponse](api, authOperation("dev-minio-test", http.MethodGet, "/test/minio", "Development-only MinIO smoke test", "Development", 401, 500), authHandler(db, devMinIOTestHandler(minioClient, bucketName)))
	registerRawOperation(api, noSecurityOperation("dev-oauth-authorize", http.MethodGet, "/oauth/authorize", "Development-only OAuth compatibility redirect", "Development", 400), devOAuthHandler(db), map[int]reflect.Type{302: reflect.TypeOf("")})
	registerRawOperation(api, noSecurityOperation("dev-user-oauth", http.MethodGet, "/user/oauth", "Development-only OAuth compatibility redirect", "Development", 400), devOAuthHandler(db), map[int]reflect.Type{302: reflect.TypeOf("")})
}

func main() {
	var (
		port           = flag.String("port", "8080", "Port to run the server on")
		createUser     = flag.String("create-user", "", "Create a regular user with the given username")
		createAdmin    = flag.String("create-admin", "", "Create an admin user with the given username")
		ensureUser     = flag.String("ensure-user", "", "Create or update a regular user with the given username")
		ensureAdmin    = flag.String("ensure-admin", "", "Create or update an admin user with the given username")
		apiKey         = flag.String("api-key", "", "API key to use with --ensure-user or --ensure-admin")
		listUsers      = flag.Bool("list-users", false, "List all users in the database")
		deactivateUser = flag.String("deactivate-user", "", "Deactivate user with the given username")
		activateUser   = flag.String("activate-user", "", "Activate user with the given username")
	)
	flag.Parse()

	fmt.Println("Using PostgreSQL database")
	db, err := models.NewPostgresDatabase()
	if err != nil {
		log.Fatalf("Failed to open PostgreSQL database: %v", err)
	}
	defer db.Close()

	fmt.Println("Using MinIO storage")
	minioClient, bucketName, err := initializeMinIO()
	if err != nil {
		log.Fatalf("Failed to initialize MinIO: %v", err)
	}
	fmt.Printf("MinIO initialized with endpoint: %s, bucket: %s\n", os.Getenv("MINIO_ENDPOINT"), bucketName)

	if *createUser != "" {
		_, err := auth.CreateUser(db, *createUser, "user")
		if err != nil {
			log.Fatalf("Failed to create user: %v", err)
		}
		os.Exit(0)
	}

	if *createAdmin != "" {
		_, err := auth.CreateUser(db, *createAdmin, "admin")
		if err != nil {
			log.Fatalf("Failed to create admin: %v", err)
		}
		os.Exit(0)
	}

	if *ensureUser != "" {
		_, err := auth.EnsureUser(db, *ensureUser, "user", *apiKey)
		if err != nil {
			log.Fatalf("Failed to ensure user: %v", err)
		}
		os.Exit(0)
	}

	if *ensureAdmin != "" {
		_, err := auth.EnsureUser(db, *ensureAdmin, "admin", *apiKey)
		if err != nil {
			log.Fatalf("Failed to ensure admin: %v", err)
		}
		os.Exit(0)
	}

	if *listUsers {
		err := auth.ListUsers(db)
		if err != nil {
			log.Fatalf("Failed to list users: %v", err)
		}
		os.Exit(0)
	}

	if *deactivateUser != "" {
		err := auth.DeactivateUser(db, *deactivateUser)
		if err != nil {
			log.Fatalf("Failed to deactivate user: %v", err)
		}
		os.Exit(0)
	}

	if *activateUser != "" {
		err := auth.ActivateUser(db, *activateUser)
		if err != nil {
			log.Fatalf("Failed to activate user: %v", err)
		}
		os.Exit(0)
	}

	coreHandlers := handlers.NewCoreHandlers(db)
	wharfHandlers := handlers.NewWharfHandlers(db, minioClient, bucketName)

	r := mux.NewRouter()

	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			fmt.Printf("REQUEST: %s %s\n", req.Method, requestLogTarget(req))

			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

			if req.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			next.ServeHTTP(w, req)
		})
	})

	api := humamux.New(r, newAPIConfig())
	registerDenKitAPI(api, db, coreHandlers, wharfHandlers)
	registerDevRoutes(api, db, minioClient, bucketName)

	fmt.Printf("Starting server on port %s\n", *port)
	fmt.Printf("Database: PostgreSQL (%s:%s/%s)\n", os.Getenv("POSTGRES_HOST"), os.Getenv("POSTGRES_PORT"), os.Getenv("POSTGRES_DB"))
	fmt.Printf("Storage: MinIO (%s)\n", os.Getenv("MINIO_ENDPOINT"))
	fmt.Printf("\nTo create a test user, run:\n")
	fmt.Printf("  %s -create-user=myusername\n", os.Args[0])
	fmt.Printf("\nThen configure butler with:\n")
	fmt.Printf("  butler --address=http://127.0.0.1:%s login\n", *port)
	fmt.Printf("\nOr add '127.0.0.1 api.localhost' to /etc/hosts and use:\n")
	fmt.Printf("  butler --address=http://localhost:%s login\n", *port)

	address := "0.0.0.0:" + *port
	fmt.Printf("Server listening on %s (all interfaces)\n", address)
	log.Fatal(newHTTPServer(address, r).ListenAndServe())
}
