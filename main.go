package main

import (
	"cmp"
	"context"
	"denkit-stash/auth"
	"denkit-stash/handlers"
	"denkit-stash/models"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humamux"
	"github.com/gorilla/mux"
)

type storageConfig struct {
	endpoint       string
	publicEndpoint string
	accessKey      string
	secretKey      string
	bucketName     string
	region         string
	useSSL         bool
}

type storageTestResponse = handlers.StorageTestResponse

type objectStorageClients struct {
	client        *s3.Client
	presignClient *s3.PresignClient
	bucketName    string
	config        storageConfig
}

func readStorageConfig() (storageConfig, error) {
	useSSLValue := os.Getenv("S3_USE_SSL")
	if useSSLValue == "" {
		useSSLValue = "false"
	}
	useSSL, err := strconv.ParseBool(useSSLValue)
	if err != nil {
		return storageConfig{}, fmt.Errorf("S3_USE_SSL must be true or false")
	}

	cfg := storageConfig{
		endpoint:       os.Getenv("S3_ENDPOINT"),
		publicEndpoint: os.Getenv("S3_PUBLIC_ENDPOINT"),
		accessKey:      os.Getenv("S3_ACCESS_KEY"),
		secretKey:      os.Getenv("S3_SECRET_KEY"),
		bucketName:     os.Getenv("S3_BUCKET"),
		region:         cmp.Or(os.Getenv("S3_REGION"), "us-east-1"),
		useSSL:         useSSL,
	}
	if cfg.endpoint == "" {
		return cfg, fmt.Errorf("S3_ENDPOINT environment variable is required")
	}
	if cfg.accessKey == "" {
		return cfg, fmt.Errorf("S3_ACCESS_KEY environment variable is required")
	}
	if cfg.secretKey == "" {
		return cfg, fmt.Errorf("S3_SECRET_KEY environment variable is required")
	}
	if cfg.bucketName == "" {
		return cfg, fmt.Errorf("S3_BUCKET environment variable is required")
	}
	return cfg, nil
}

func endpointURLForS3Client(rawEndpoint string, fallbackUseSSL bool) (string, error) {
	parsed, err := url.Parse(rawEndpoint)
	if err == nil && parsed.Scheme != "" {
		if parsed.Host == "" || parsed.Path != "" {
			return "", fmt.Errorf("invalid S3 endpoint %q", rawEndpoint)
		}
		switch parsed.Scheme {
		case "http", "https":
			return rawEndpoint, nil
		default:
			return "", fmt.Errorf("unsupported S3 endpoint scheme %q", parsed.Scheme)
		}
	}
	scheme := "http"
	if fallbackUseSSL {
		scheme = "https"
	}
	return scheme + "://" + rawEndpoint, nil
}

func newStorageClient(ctx context.Context, endpoint string, useSSL bool, cfg storageConfig) (*s3.Client, error) {
	endpointURL, err := endpointURLForS3Client(endpoint, useSSL)
	if err != nil {
		return nil, err
	}

	awsConfig, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(cfg.region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.accessKey, cfg.secretKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create AWS SDK config: %v", err)
	}

	return s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(endpointURL)
		options.UsePathStyle = true
	}), nil
}

func initializeObjectStorage() (*objectStorageClients, error) {
	cfg, err := readStorageConfig()
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	client, err := newStorageClient(ctx, cfg.endpoint, cfg.useSSL, cfg)
	if err != nil {
		return nil, err
	}
	presignS3Client := client
	if cfg.publicEndpoint != "" && cfg.publicEndpoint != cfg.endpoint {
		presignS3Client, err = newStorageClient(ctx, cfg.publicEndpoint, cfg.useSSL, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create public S3-compatible storage client: %v", err)
		}
	}
	presignClient := s3.NewPresignClient(presignS3Client)

	_, err = client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(cfg.bucketName)})
	if err != nil && !isNotFoundError(err) {
		return nil, fmt.Errorf("failed to check if bucket exists: %v", err)
	}

	if err != nil {
		if _, err = client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(cfg.bucketName)}); err != nil {
			return nil, fmt.Errorf("failed to create bucket: %v", err)
		}
		log.Printf("created storage bucket: %s", cfg.bucketName)
	}

	if _, err = client.DeleteBucketPolicy(ctx, &s3.DeleteBucketPolicyInput{Bucket: aws.String(cfg.bucketName)}); err != nil && !isNoSuchBucketPolicyError(err) {
		return nil, fmt.Errorf("failed to enforce private bucket policy: %v", err)
	}

	return &objectStorageClients{client: client, presignClient: presignClient, bucketName: cfg.bucketName, config: cfg}, nil
}

func isNotFoundError(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.ErrorCode() {
	case "NotFound", "NoSuchBucket", "404":
		return true
	default:
		return false
	}
}

func isNoSuchBucketPolicyError(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.ErrorCode() {
	case "NoSuchBucketPolicy", "NoSuchBucketPolicyException", "NotFound", "404":
		return true
	default:
		return false
	}
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
		log.Printf("ignoring invalid %s=%q; using %s", key, value, fallback)
		return fallback
	}
	return parsed
}

// cleanStaleScratchDirs removes archive staging directories a previous process
// left behind after a crash. TMPDIR is a persistent disk-backed volume, so
// unlike a tmpfs these multi-GB leftovers would otherwise accumulate forever.
func cleanStaleScratchDirs() {
	tempDir := os.TempDir()
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		log.Printf("warning: could not scan %s for stale scratch dirs: %v", tempDir, err)
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), "denkit-build-") || strings.HasPrefix(entry.Name(), "denkit-rebuild-") {
			path := tempDir + string(os.PathSeparator) + entry.Name()
			if err := os.RemoveAll(path); err != nil {
				log.Printf("warning: could not remove stale scratch dir %s: %v", path, err)
			} else {
				log.Printf("removed stale scratch dir %s", path)
			}
		}
	}
}

func archiveGCConfigFromEnv() handlers.ArchiveGCConfig {
	cfg := handlers.DefaultArchiveGCConfig()
	cfg.Enabled = os.Getenv("DENKIT_ARCHIVE_GC_ENABLED") == "true"
	cfg.TTL = envDuration("DENKIT_ARCHIVE_TTL", cfg.TTL)
	cfg.Interval = envDuration("DENKIT_ARCHIVE_GC_INTERVAL", cfg.Interval)
	if batch := os.Getenv("DENKIT_ARCHIVE_GC_BATCH"); batch != "" {
		if parsed, err := strconv.Atoi(batch); err == nil && parsed > 0 {
			cfg.BatchLimit = parsed
		} else {
			log.Printf("ignoring invalid DENKIT_ARCHIVE_GC_BATCH=%q; using %d", batch, cfg.BatchLimit)
		}
	}
	return cfg
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

func devObjectStorageTestHandler(storageClient *s3.Client, presignClient *s3.PresignClient, bucketName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		testContent := "Hello from S3-compatible storage. This is a test file."
		objectName := "test/hello.txt"

		ctx := context.Background()
		_, err := storageClient.PutObject(ctx, &s3.PutObjectInput{
			Bucket:        aws.String(bucketName),
			Key:           aws.String(objectName),
			Body:          strings.NewReader(testContent),
			ContentLength: aws.Int64(int64(len(testContent))),
			ContentType:   aws.String("text/plain"),
		})
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to upload test file: %v", err), http.StatusInternalServerError)
			return
		}

		signedURL, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
			Bucket: aws.String(bucketName),
			Key:    aws.String(objectName),
		}, func(options *s3.PresignOptions) {
			options.Expires = time.Hour
		})
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to generate signed URL: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(handlers.StorageTestResponse{
			Message: "Test file uploaded", SignedURL: signedURL.URL,
			ExpiresIn: "1 hour", TestContent: testContent,
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

func registerDevRoutes(api huma.API, db models.Database, storageClient *s3.Client, presignClient *s3.PresignClient, bucketName string) {
	if !devEndpointsEnabled() {
		return
	}

	registerRaw[handlers.StorageTestResponse](api, authOperation("dev-storage-test", http.MethodGet, "/test/storage", "Development-only object storage smoke test", "Development", 401, 500), authHandler(db, devObjectStorageTestHandler(storageClient, presignClient, bucketName)))
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

	db, err := models.NewPostgresDatabase()
	if err != nil {
		log.Fatalf("Failed to open PostgreSQL database: %v", err)
	}
	defer db.Close()

	storage, err := initializeObjectStorage()
	if err != nil {
		log.Fatalf("Failed to initialize object storage: %v", err)
	}

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

	cleanStaleScratchDirs()

	coreHandlers := handlers.NewCoreHandlers(db)
	wharfHandlers := handlers.NewWharfHandlers(db, storage.client, storage.presignClient, storage.bucketName)
	wharfHandlers.SetArchiveRebuildTimeout(envDuration("DENKIT_ARCHIVE_REBUILD_TIMEOUT", 20*time.Minute))
	wharfHandlers.StartArchiveGC(context.Background(), archiveGCConfigFromEnv())

	r := mux.NewRouter()

	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
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
	registerDevRoutes(api, db, storage.client, storage.presignClient, storage.bucketName)

	address := "0.0.0.0:" + *port
	log.Printf("listening on %s", address)
	log.Fatal(newHTTPServer(address, r).ListenAndServe())
}
