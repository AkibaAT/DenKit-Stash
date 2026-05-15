package testdb

import (
	"database/sql"
	"denkit-stash/models"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/lib/pq"
)

func New(t *testing.T) models.Database {
	t.Helper()

	cfg := configFromEnv()
	adminDB, err := sql.Open("postgres", cfg.dsn(cfg.AdminDB))
	if err != nil {
		t.Fatalf("open PostgreSQL admin connection: %v", err)
	}
	if err := adminDB.Ping(); err != nil {
		adminDB.Close()
		t.Skipf("PostgreSQL test database is unavailable: %v", err)
	}

	dbName := fmt.Sprintf("denkit_test_%d_%d", time.Now().UnixNano(), os.Getpid())
	if _, err := adminDB.Exec("CREATE DATABASE " + pq.QuoteIdentifier(dbName)); err != nil {
		adminDB.Close()
		t.Fatalf("create PostgreSQL test database: %v", err)
	}

	t.Cleanup(func() {
		_, _ = adminDB.Exec(`
			SELECT pg_terminate_backend(pid)
			FROM pg_stat_activity
			WHERE datname = $1 AND pid <> pg_backend_pid()`, dbName)
		_, _ = adminDB.Exec("DROP DATABASE IF EXISTS " + pq.QuoteIdentifier(dbName))
		adminDB.Close()
	})

	setEnv(t, "POSTGRES_HOST", cfg.Host)
	setEnv(t, "POSTGRES_PORT", cfg.Port)
	setEnv(t, "POSTGRES_USER", cfg.User)
	setEnv(t, "POSTGRES_PASSWORD", cfg.Password)
	setEnv(t, "POSTGRES_SSLMODE", cfg.SSLMode)
	setEnv(t, "POSTGRES_DB", dbName)
	setEnv(t, "DENKIT_API_KEY_HASH_SECRET", "denkit-test-api-key-hash-secret")

	db, err := models.NewPostgresDatabase()
	if err != nil {
		t.Fatalf("open PostgreSQL test database: %v", err)
	}
	return db
}

type config struct {
	Host     string
	Port     string
	User     string
	Password string
	AdminDB  string
	SSLMode  string
}

func configFromEnv() config {
	return config{
		Host:     firstEnv("DENKIT_TEST_POSTGRES_HOST", "POSTGRES_HOST", "localhost"),
		Port:     firstEnv("DENKIT_TEST_POSTGRES_PORT", "POSTGRES_PORT", "5432"),
		User:     firstEnv("DENKIT_TEST_POSTGRES_USER", "POSTGRES_USER", "postgres"),
		Password: firstEnv("DENKIT_TEST_POSTGRES_PASSWORD", "POSTGRES_PASSWORD", "postgres"),
		AdminDB:  firstEnv("DENKIT_TEST_POSTGRES_DB", "postgres"),
		SSLMode:  firstEnv("DENKIT_TEST_POSTGRES_SSLMODE", "POSTGRES_SSLMODE", "disable"),
	}
}

func (c config) dsn(dbName string) string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s", c.Host, c.Port, c.User, c.Password, dbName, c.SSLMode)
}

func firstEnv(keysAndDefault ...string) string {
	defaultValue := keysAndDefault[len(keysAndDefault)-1]
	for _, key := range keysAndDefault[:len(keysAndDefault)-1] {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return defaultValue
}

func setEnv(t *testing.T, key string, value string) {
	t.Helper()

	previous, existed := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("set %s: %v", key, err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(key, previous)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}
