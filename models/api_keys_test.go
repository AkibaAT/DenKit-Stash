package models

import "testing"

func TestAPIKeyDigestRequiresSecret(t *testing.T) {
	t.Setenv(apiKeyHashSecretEnv, "")

	if _, err := APIKeyDigest("secret"); err == nil {
		t.Fatal("expected API key digest to require a hash secret")
	}
}

func TestAPIKeyDigestIsDeterministicAndNonRecoverable(t *testing.T) {
	t.Setenv(apiKeyHashSecretEnv, "test-secret")

	first, err := APIKeyDigest("secret")
	if err != nil {
		t.Fatalf("digest first key: %v", err)
	}
	second, err := APIKeyDigest("secret")
	if err != nil {
		t.Fatalf("digest second key: %v", err)
	}
	if first != second {
		t.Fatalf("expected deterministic digest, got %q and %q", first, second)
	}
	if first == "secret" {
		t.Fatal("digest returned raw API key")
	}
	if !IsAPIKeyDigest(first) {
		t.Fatalf("expected digest prefix, got %q", first)
	}
}
