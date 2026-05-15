package auth

import (
	"crypto/rand"
	"denkit-stash/models"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

func AuthMiddleware(db models.Database) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey := extractAPIKey(r)

			if apiKey == "" {
				http.Error(w, `{"errors":["missing api_key"]}`, http.StatusUnauthorized)
				return
			}

			user, err := db.GetUserByAPIKey(apiKey)
			if err != nil {
				http.Error(w, `{"errors":["invalid api_key"]}`, http.StatusUnauthorized)
				return
			}

			ctx := r.Context()
			ctx = SetUser(ctx, user)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func OptionalAuthMiddleware(db models.Database) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey := extractAPIKey(r)
			if apiKey != "" {
				user, err := db.GetUserByAPIKey(apiKey)
				if err == nil {
					ctx := r.Context()
					ctx = SetUser(ctx, user)
					r = r.WithContext(ctx)
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

func extractAPIKey(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		if strings.HasPrefix(authHeader, "Bearer ") {
			return strings.TrimPrefix(authHeader, "Bearer ")
		}
		if strings.HasPrefix(authHeader, "access_token=") {
			return strings.TrimPrefix(authHeader, "access_token=")
		}
		return authHeader
	}

	if apiKey := r.URL.Query().Get("api_key"); apiKey != "" {
		if strings.HasPrefix(apiKey, "access_token=") {
			return strings.TrimPrefix(apiKey, "access_token=")
		}
		return apiKey
	}
	accessToken := r.URL.Query().Get("access_token")
	if strings.HasPrefix(accessToken, "access_token=") {
		return strings.TrimPrefix(accessToken, "access_token=")
	}
	return accessToken
}

func GenerateAPIKey() (string, error) {
	bytes := make([]byte, 32)
	_, err := rand.Read(bytes)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func CreateTestUser(db models.Database, username string) (*models.User, error) {
	apiKey, err := GenerateAPIKey()
	if err != nil {
		return nil, err
	}

	user := &models.User{
		Username:    username,
		DisplayName: username,
		APIKey:      apiKey,
	}

	err = db.CreateUser(user)
	if err != nil {
		return nil, err
	}

	fmt.Printf("Created test user: %s\n", username)
	return user, nil
}
