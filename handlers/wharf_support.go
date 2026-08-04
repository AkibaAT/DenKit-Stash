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

	"github.com/aws/aws-sdk-go-v2/service/s3"
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

func (h *WharfHandlers) validateNamespaceAccess(user *models.User, namespace string) error {
	if !user.CanAccessNamespace(namespace) {
		return fmt.Errorf("access denied: user '%s' cannot access namespace '%s'", user.Username, namespace)
	}
	return nil
}

func (h *WharfHandlers) authorizeBuildAccess(r *http.Request, buildID int64) error {
	build, err := h.db.GetBuildByID(buildID)
	if err != nil {
		return err
	}
	return h.authorizeLoadedBuildAccess(r, build)
}

func (h *WharfHandlers) authorizeLoadedBuildAccess(r *http.Request, build *models.Build) error {
	requestUser, ok := auth.GetUser(r.Context())
	if !ok {
		return fmt.Errorf("authentication required")
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
		writeError(w, http.StatusUnauthorized, "missing api_key")
		return
	}
	writeError(w, http.StatusForbidden, "access denied")
}

type WharfHandlers struct {
	db                    models.Database
	storage               ObjectStorage
	archiveRebuildTimeout time.Duration
}

var (
	contentRangePattern    = regexp.MustCompile(`^bytes (\d+)-(\d+)/(\*|\d+)$`)
	emptyFinalRangePattern = regexp.MustCompile(`^bytes (\d+)--1/(\d+)$`)
)

func NewWharfHandlers(db models.Database, storageClient *s3.Client, presignClient *s3.PresignClient, bucketName string) *WharfHandlers {
	handlers := &WharfHandlers{db: db}
	if storageClient != nil {
		handlers.storage = newS3ObjectStorage(storageClient, presignClient, bucketName)
	}
	return handlers
}

func (h *WharfHandlers) GetWharfStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, WharfStatusResponse{OK: true})
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
	url, err := h.storage.PresignPut(context.Background(), objectName, expiry)
	if err != nil {
		return "", fmt.Errorf("failed to generate presigned upload URL: %v", err)
	}
	return url, nil
}

func (h *WharfHandlers) FileExists(objectName string) bool {
	_, err := h.storage.Head(context.Background(), objectName)
	return err == nil
}

func (h *WharfHandlers) GetFileSize(objectName string) (int64, error) {
	size, err := h.storage.Head(context.Background(), objectName)
	if err != nil {
		return 0, fmt.Errorf("failed to get object stat: %v", err)
	}
	return size, nil
}

// GetSignedURL presigns a download for objectName. Pass a non-empty
// downloadFilename to control the name the browser saves the file under.
func (h *WharfHandlers) GetSignedURL(objectName string, expiry time.Duration, downloadFilename string) (string, error) {
	url, err := h.storage.PresignGet(context.Background(), objectName, expiry, downloadFilename)
	if err != nil {
		return "", fmt.Errorf("failed to generate signed URL: %v", err)
	}
	return url, nil
}
