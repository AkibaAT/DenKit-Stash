package handlers

import (
	"context"
	"denkit-stash/auth"
	"denkit-stash/models"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/minio/minio-go/v7"
)

var channelPlatformRules = []struct {
	platform string
	aliases  []string
}{
	{platform: "windows", aliases: []string{"win", "windows"}},
	{platform: "linux", aliases: []string{"linux"}},
	{platform: "osx", aliases: []string{"mac", "osx"}},
	{platform: "android", aliases: []string{"android"}},
}

func platformsForChannelName(channelName string) string {
	channelTokens := channelNameTokens(channelName)

	return platformsForTokens(channelTokens)
}

func platformsForArchiveFilename(filename string) string {
	filenameTokens := channelNameTokens(filename)

	return platformsForTokens(filenameTokens)
}

func platformsForTokens(tokens map[string]bool) string {
	platforms := make([]string, 0, len(channelPlatformRules))

	for _, rule := range channelPlatformRules {
		for _, alias := range rule.aliases {
			if tokens[alias] {
				platforms = append(platforms, rule.platform)
				break
			}
		}
	}

	platformsJSON, err := json.Marshal(platforms)
	if err != nil {
		return "[]"
	}
	return string(platformsJSON)
}

func channelNameTokens(channelName string) map[string]bool {
	tokens := make(map[string]bool)
	for _, token := range strings.FieldsFunc(strings.ToLower(channelName), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if token != "" {
			tokens[token] = true
		}
	}
	return tokens
}

// validateNamespaceAccess checks if the user can access the given namespace.
func (h *WharfHandlers) validateNamespaceAccess(user *models.User, namespace string) error {
	if !user.CanAccessNamespace(namespace) {
		return fmt.Errorf("access denied: user '%s' cannot access namespace '%s'", user.Username, namespace)
	}
	return nil
}

func (h *WharfHandlers) authorizeBuildAccess(r *http.Request, buildID int64) error {
	requestUser, ok := auth.GetUser(r.Context())
	if !ok {
		return fmt.Errorf("authentication required")
	}

	build, err := h.db.GetBuildByID(buildID)
	if err != nil {
		return err
	}
	return h.authorizeLoadedBuildAccess(r, build, requestUser)
}

func (h *WharfHandlers) authorizeLoadedBuildAccess(r *http.Request, build *models.Build, requestUser *models.User) error {
	if requestUser == nil {
		var ok bool
		requestUser, ok = auth.GetUser(r.Context())
		if !ok {
			return fmt.Errorf("authentication required")
		}
	}
	upload, err := h.db.GetUploadByID(build.UploadID)
	if err != nil {
		return err
	}
	owner, _, err := h.db.GetGameByID(upload.GameID)
	if err != nil {
		return err
	}

	return h.validateNamespaceAccess(requestUser, owner.Username)
}

func writeBuildAccessError(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r.Context()); !ok {
		http.Error(w, `{"errors":["missing api_key"]}`, http.StatusUnauthorized)
		return
	}
	http.Error(w, `{"errors":["access denied"]}`, http.StatusForbidden)
}

type WharfHandlers struct {
	db          models.Database
	minioClient *minio.Client
	bucketName  string
}

var (
	contentRangePattern    = regexp.MustCompile(`^bytes (\d+)-(\d+)/(\*|\d+)$`)
	emptyFinalRangePattern = regexp.MustCompile(`^bytes (\d+)--1/(\d+)$`)
)

func NewWharfHandlers(db models.Database, minioClient *minio.Client, bucketName string) *WharfHandlers {
	return &WharfHandlers{db: db, minioClient: minioClient, bucketName: bucketName}
}

func (h *WharfHandlers) absoluteURL(r *http.Request, path string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwardedProto := r.Header.Get("X-Forwarded-Proto"); forwardedProto != "" {
		scheme = forwardedProto
	}
	return fmt.Sprintf("%s://%s%s", scheme, r.Host, path)
}

func (h *WharfHandlers) GetPresignedUploadURL(objectName string, expiry time.Duration) (string, error) {
	ctx := context.Background()
	presignedURL, err := h.minioClient.PresignedPutObject(ctx, h.bucketName, objectName, expiry)
	if err != nil {
		return "", fmt.Errorf("failed to generate presigned upload URL: %v", err)
	}
	return presignedURL.String(), nil
}

func (h *WharfHandlers) FileExists(objectName string) bool {
	ctx := context.Background()
	_, err := h.minioClient.StatObject(ctx, h.bucketName, objectName, minio.StatObjectOptions{})
	return err == nil
}

func (h *WharfHandlers) GetFileSize(objectName string) (int64, error) {
	ctx := context.Background()
	stat, err := h.minioClient.StatObject(ctx, h.bucketName, objectName, minio.StatObjectOptions{})
	if err != nil {
		return 0, fmt.Errorf("failed to get object stat: %v", err)
	}
	return stat.Size, nil
}

func (h *WharfHandlers) GetSignedURL(objectName string, expiry time.Duration) (string, error) {
	ctx := context.Background()
	presignedURL, err := h.minioClient.PresignedGetObject(ctx, h.bucketName, objectName, expiry, nil)
	if err != nil {
		return "", fmt.Errorf("failed to generate signed URL: %v", err)
	}
	return presignedURL.String(), nil
}
