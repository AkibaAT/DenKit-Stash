package handlers

import (
	"fmt"
	"os"
	"strconv"
)

const (
	defaultRequestBodyLimitBytes int64 = 1 << 20  // 1 MiB for JSON/form control requests.
	defaultUploadSessionMaxBytes int64 = 50 << 30 // 50 GiB per deferred upload session.
	maxRequestBodyLimitEnv             = "DENKIT_MAX_REQUEST_BODY_BYTES"
	maxUploadSessionBytesEnv           = "DENKIT_MAX_UPLOAD_SESSION_BYTES"
)

func maxRequestBodyBytes() int64 {
	return positiveInt64Env(maxRequestBodyLimitEnv, defaultRequestBodyLimitBytes)
}

func maxUploadSessionBytes() int64 {
	return positiveInt64Env(maxUploadSessionBytesEnv, defaultUploadSessionMaxBytes)
}

func positiveInt64Env(key string, fallback int64) int64 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		fmt.Printf("Ignoring invalid %s=%q; using %d\n", key, value, fallback)
		return fallback
	}
	return parsed
}
