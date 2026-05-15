package models

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

const (
	apiKeyDigestPrefix  = "hmac-sha256:"
	apiKeyHashSecretEnv = "DENKIT_API_KEY_HASH_SECRET"
)

func APIKeyDigest(apiKey string) (string, error) {
	if strings.HasPrefix(apiKey, apiKeyDigestPrefix) {
		return apiKey, nil
	}
	secret, err := APIKeyHashSecret()
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	if _, err := mac.Write([]byte(apiKey)); err != nil {
		return "", err
	}
	return apiKeyDigestPrefix + hex.EncodeToString(mac.Sum(nil)), nil
}

func IsAPIKeyDigest(value string) bool {
	return strings.HasPrefix(value, apiKeyDigestPrefix)
}

func APIKeyHashSecret() (string, error) {
	secret := os.Getenv(apiKeyHashSecretEnv)
	if secret == "" {
		return "", fmt.Errorf("%s is required", apiKeyHashSecretEnv)
	}
	return secret, nil
}
