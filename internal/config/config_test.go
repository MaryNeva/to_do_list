package config

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("JWT_SECRET", strings.Repeat("a", 32))
	t.Setenv("DB_PASSWORD", "s3cret")
}

func TestLoad_Success(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("SERVER_ADDRESS", ":9090")
	t.Setenv("DB_HOST", "db.internal")
	t.Setenv("DB_PORT", "5433")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.ServerAddress != ":9090" {
		t.Errorf("ServerAddress = %q, want %q", cfg.ServerAddress, ":9090")
	}
	if cfg.DBHost != "db.internal" {
		t.Errorf("DBHost = %q, want %q", cfg.DBHost, "db.internal")
	}
	if cfg.DBPort != 5433 {
		t.Errorf("DBPort = %d, want 5433", cfg.DBPort)
	}
}

func TestLoad_MissingJWTSecret(t *testing.T) {
	t.Setenv("JWT_SECRET", "")
	t.Setenv("DB_PASSWORD", "s3cret")

	if _, err := Load(); err == nil {
		t.Error("Load() with no JWT_SECRET should return an error")
	}
}

func TestLoad_ShortJWTSecret(t *testing.T) {
	t.Setenv("JWT_SECRET", "too-short")
	t.Setenv("DB_PASSWORD", "s3cret")

	if _, err := Load(); err == nil {
		t.Error("Load() with a short JWT_SECRET should return an error")
	}
}

func TestLoad_MissingDBPassword(t *testing.T) {
	t.Setenv("JWT_SECRET", strings.Repeat("a", 32))
	t.Setenv("DB_PASSWORD", "")

	if _, err := Load(); err == nil {
		t.Error("Load() with no DB_PASSWORD should return an error")
	}
}

func TestLoad_AdminCredentialsMustComeInPairs(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ADMIN_USERNAME", "admin")
	t.Setenv("ADMIN_PASSWORD_HASH", "")

	if _, err := Load(); err == nil {
		t.Error("Load() with ADMIN_USERNAME but no ADMIN_PASSWORD_HASH should return an error")
	}
}

func TestLoad_Defaults(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.ServerAddress != ":8080" {
		t.Errorf("default ServerAddress = %q, want %q", cfg.ServerAddress, ":8080")
	}
	if cfg.DBSSLMode != "disable" {
		t.Errorf("default DBSSLMode = %q, want %q", cfg.DBSSLMode, "disable")
	}
	if !cfg.RunMigrations {
		t.Error("default RunMigrations should be true")
	}
}

func TestDatabaseDSN_EncodesSpecialCharacters(t *testing.T) {
	cfg := Config{
		DBUser:     "user",
		DBPassword: "p@ss:word/withslash",
		DBHost:     "localhost",
		DBPort:     5432,
		DBName:     "to_do",
		DBSSLMode:  "disable",
	}

	dsn := cfg.DatabaseDSN()

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("DatabaseDSN() produced an unparseable URL %q: %v", dsn, err)
	}
	if got, _ := u.User.Password(); got != "p@ss:word/withslash" {
		t.Errorf("DatabaseDSN() password round-trips to %q, want %q", got, "p@ss:word/withslash")
	}
}

func TestLoadDotEnv_DoesNotOverrideExistingEnv(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte("CONFIG_TEST_KEY=from-file\n"), 0o600); err != nil {
		t.Fatalf("write temp .env: %v", err)
	}

	t.Setenv("CONFIG_TEST_KEY", "from-environment")

	loadDotEnv(envPath)

	if got := os.Getenv("CONFIG_TEST_KEY"); got != "from-environment" {
		t.Errorf("loadDotEnv overrode an existing env var: got %q, want %q", got, "from-environment")
	}
}

func TestLoadDotEnv_SetsMissingKeys(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	content := "# a comment\n\nCONFIG_TEST_ANOTHER_KEY=\"quoted value\"\n"
	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp .env: %v", err)
	}
	os.Unsetenv("CONFIG_TEST_ANOTHER_KEY")

	loadDotEnv(envPath)
	t.Cleanup(func() { os.Unsetenv("CONFIG_TEST_ANOTHER_KEY") })

	if got := os.Getenv("CONFIG_TEST_ANOTHER_KEY"); got != "quoted value" {
		t.Errorf("loadDotEnv() = %q, want %q", got, "quoted value")
	}
}

func TestLoadDotEnv_MissingFileIsNotFatal(t *testing.T) {
	// Must not panic and must simply leave the environment untouched.
	loadDotEnv(filepath.Join(t.TempDir(), "does-not-exist.env"))
}
