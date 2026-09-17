package config

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const completeYAML = `
app:
  name: to-do-list
  env: test

server:
  address: ":8080"
  read_timeout: 10s
  write_timeout: 10s
  shutdown_timeout: 15s

db:
  host: localhost
  port: 5432
  name: to_do
  user: postgres
  sslmode: disable
  connect_timeout: 5s
  call_timeout: 5s

jwt:
  ttl: 1h
  refresh_ttl: 720h
  issuer: to-do-list
  min_secret_length: 32

password:
  bcrypt_cost: 12
  min_length: 8

user:
  min_username_length: 3
  max_username_length: 50

task:
  max_title_length: 200
  max_description_length: 4000

pagination:
  default_page_size: 20
  max_page_size: 100

cleanup:
  interval: 1h
  refresh_token_retention: 720h

ratelimit:
  auth_max_requests: 20
  auth_window: 1m

cors:
  allow_origins: "*"
  allow_methods: "GET, POST, PATCH, DELETE, OPTIONS"
  allow_headers: "Origin, Content-Type, Accept, Authorization"

health:
  ready_timeout: 2s

log:
  level: info
  format: json

run_migrations: true
migrations_path: migrations
`

func writeConfig(t *testing.T, yaml string) (configPath, envPath string) {
	t.Helper()
	dir := t.TempDir()
	configPath = filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write temp config.yaml: %v", err)
	}
	return configPath, filepath.Join(dir, ".env")
}

func setSecrets(t *testing.T) {
	t.Helper()
	t.Setenv("JWT_SECRET", strings.Repeat("a", 32))
	t.Setenv("DB_PASSWORD", "s3cret")
}

func loadComplete(t *testing.T) (Config, error) {
	t.Helper()
	configPath, envPath := writeConfig(t, completeYAML)
	return LoadFrom(configPath, envPath)
}

func TestLoadFrom_ReadsEverySettingFromYAML(t *testing.T) {
	setSecrets(t)

	cfg, err := loadComplete(t)
	if err != nil {
		t.Fatalf("LoadFrom() unexpected error: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"AppName", cfg.AppName, "to-do-list"},
		{"Env", cfg.Env, "test"},
		{"ServerAddress", cfg.ServerAddress, ":8080"},
		{"ReadTimeout", cfg.ReadTimeout.String(), "10s"},
		{"ShutdownTimeout", cfg.ShutdownTimeout.String(), "15s"},
		{"DBHost", cfg.DBHost, "localhost"},
		{"DBPort", int(cfg.DBPort), 5432},
		{"DBName", cfg.DBName, "to_do"},
		{"DBUser", cfg.DBUser, "postgres"},
		{"DBSSLMode", cfg.DBSSLMode, "disable"},
		{"DBConnectTimeout", cfg.DBConnectTimeout.String(), "5s"},
		{"DBCallTimeout", cfg.DBCallTimeout.String(), "5s"},
		{"JWTTTL", cfg.JWTTTL.String(), "1h0m0s"},
		{"JWTRefreshTTL", cfg.JWTRefreshTTL.String(), "720h0m0s"},
		{"JWTIssuer", cfg.JWTIssuer, "to-do-list"},
		{"JWTMinSecretLength", cfg.JWTMinSecretLength, 32},
		{"PasswordBcryptCost", cfg.PasswordBcryptCost, 12},
		{"PasswordMinLength", cfg.PasswordMinLength, 8},
		{"UsernameMinLength", cfg.UsernameMinLength, 3},
		{"UsernameMaxLength", cfg.UsernameMaxLength, 50},
		{"TaskMaxTitleLength", cfg.TaskMaxTitleLength, 200},
		{"TaskMaxDescriptionLength", cfg.TaskMaxDescriptionLength, 4000},
		{"DefaultPageSize", cfg.DefaultPageSize, 20},
		{"MaxPageSize", cfg.MaxPageSize, 100},
		{"CleanupInterval", cfg.CleanupInterval.String(), "1h0m0s"},
		{"RefreshTokenRetention", cfg.RefreshTokenRetention.String(), "720h0m0s"},
		{"RateLimitAuthMaxRequests", cfg.RateLimitAuthMaxRequests, 20},
		{"RateLimitAuthWindow", cfg.RateLimitAuthWindow.String(), "1m0s"},
		{"CORSAllowOrigins", cfg.CORSAllowOrigins, "*"},
		{"CORSAllowMethods", cfg.CORSAllowMethods, "GET, POST, PATCH, DELETE, OPTIONS"},
		{"CORSAllowHeaders", cfg.CORSAllowHeaders, "Origin, Content-Type, Accept, Authorization"},
		{"HealthReadyTimeout", cfg.HealthReadyTimeout.String(), "2s"},
		{"RunMigrations", cfg.RunMigrations, true},
		{"MigrationsPath", cfg.MigrationsPath, "migrations"},
		{"LogLevel", cfg.LogLevel, "info"},
		{"LogFormat", cfg.LogFormat, "json"},
	}

	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestLoadFrom_EnvironmentOverridesYAML(t *testing.T) {
	setSecrets(t)
	t.Setenv("SERVER_ADDRESS", ":9090")
	t.Setenv("DB_HOST", "db.internal")
	t.Setenv("DB_PORT", "5433")
	t.Setenv("PASSWORD_BCRYPT_COST", "4")

	cfg, err := loadComplete(t)
	if err != nil {
		t.Fatalf("LoadFrom() unexpected error: %v", err)
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
	if cfg.PasswordBcryptCost != 4 {
		t.Errorf("PasswordBcryptCost = %d, want 4", cfg.PasswordBcryptCost)
	}
}

func TestLoadFrom_DotEnvSuppliesSecretsAndIsOverriddenByEnvironment(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(completeYAML), 0o600); err != nil {
		t.Fatalf("write temp config.yaml: %v", err)
	}

	envPath := filepath.Join(dir, ".env")
	dotenv := "DB_PASSWORD=from-dotenv\nJWT_SECRET=" + strings.Repeat("b", 32) + "\n"
	if err := os.WriteFile(envPath, []byte(dotenv), 0o600); err != nil {
		t.Fatalf("write temp .env: %v", err)
	}

	os.Unsetenv("DB_PASSWORD")
	t.Cleanup(func() { os.Unsetenv("DB_PASSWORD") })
	// A real environment variable must beat the .env file.
	t.Setenv("JWT_SECRET", strings.Repeat("c", 32))

	cfg, err := LoadFrom(configPath, envPath)
	if err != nil {
		t.Fatalf("LoadFrom() unexpected error: %v", err)
	}

	if cfg.DBPassword != "from-dotenv" {
		t.Errorf("DBPassword = %q, want %q", cfg.DBPassword, "from-dotenv")
	}
	if cfg.JWTSecret != strings.Repeat("c", 32) {
		t.Error("JWTSecret came from .env, but a real environment variable should win")
	}
}

func TestLoadFrom_MissingSettingIsReportedNotDefaulted(t *testing.T) {
	setSecrets(t)

	// Drop the whole server section.
	yaml := strings.Replace(completeYAML, `server:
  address: ":8080"
  read_timeout: 10s
  write_timeout: 10s
  shutdown_timeout: 15s
`, "", 1)

	configPath, envPath := writeConfig(t, yaml)

	_, err := LoadFrom(configPath, envPath)
	if err == nil {
		t.Fatal("LoadFrom() with no server settings should return an error, not fall back to a built-in default")
	}
	for _, want := range []string{"server.address", "server.read_timeout", "SERVER_ADDRESS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestLoadFrom_MissingConfigFileReportsEverySetting(t *testing.T) {
	setSecrets(t)
	dir := t.TempDir()

	_, err := LoadFrom(filepath.Join(dir, "does-not-exist.yaml"), filepath.Join(dir, ".env"))
	if err == nil {
		t.Fatal("LoadFrom() with no config file should return an error")
	}
	if !strings.Contains(err.Error(), "was not found or is empty") {
		t.Errorf("error should point at the missing config file, got: %v", err)
	}
}

func TestLoadFrom_InvalidValueIsReportedWithTheKeyName(t *testing.T) {
	setSecrets(t)

	yaml := strings.Replace(completeYAML, "  port: 5432", "  port: not-a-number", 1)
	configPath, envPath := writeConfig(t, yaml)

	_, err := LoadFrom(configPath, envPath)
	if err == nil {
		t.Fatal("LoadFrom() with a non-numeric db.port should return an error")
	}
	if !strings.Contains(err.Error(), "db.port") || !strings.Contains(err.Error(), "not-a-number") {
		t.Errorf("error should name the key and the bad value, got: %v", err)
	}
}

func TestLoadFrom_SecretsAreNeverReadFromYAML(t *testing.T) {
	os.Unsetenv("JWT_SECRET")
	os.Unsetenv("DB_PASSWORD")
	t.Cleanup(func() {
		os.Unsetenv("JWT_SECRET")
		os.Unsetenv("DB_PASSWORD")
	})

	// Even if someone puts secrets in config.yaml, they must not be used.
	yaml := completeYAML + "\ndb_password: from-yaml\njwt_secret: " + strings.Repeat("y", 32) + "\n"
	configPath, envPath := writeConfig(t, yaml)

	_, err := LoadFrom(configPath, envPath)
	if err == nil {
		t.Fatal("LoadFrom() should refuse to take secrets from config.yaml")
	}
	for _, want := range []string{"DB_PASSWORD", "JWT_SECRET", "never in config.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestLoadFrom_ShortJWTSecretRejected(t *testing.T) {
	t.Setenv("JWT_SECRET", "too-short")
	t.Setenv("DB_PASSWORD", "s3cret")

	if _, err := loadComplete(t); err == nil {
		t.Error("LoadFrom() with a short JWT_SECRET should return an error")
	}
}

func TestLoadFrom_JWTSecretLengthRuleComesFromConfig(t *testing.T) {
	secret := strings.Repeat("a", 40)
	t.Setenv("JWT_SECRET", secret)
	t.Setenv("DB_PASSWORD", "s3cret")

	yaml := strings.Replace(completeYAML, "  min_secret_length: 32", "  min_secret_length: 64", 1)
	configPath, envPath := writeConfig(t, yaml)

	_, err := LoadFrom(configPath, envPath)
	if err == nil {
		t.Fatal("a 40-character secret should be rejected when jwt.min_secret_length is 64")
	}
	if !strings.Contains(err.Error(), "64") {
		t.Errorf("error should mention the configured minimum, got: %v", err)
	}
}

func TestLoadFrom_AdminCredentialsMustComeInPairs(t *testing.T) {
	setSecrets(t)
	t.Setenv("ADMIN_USERNAME", "admin")
	t.Setenv("ADMIN_PASSWORD_HASH", "")

	if _, err := loadComplete(t); err == nil {
		t.Error("LoadFrom() with ADMIN_USERNAME but no ADMIN_PASSWORD_HASH should return an error")
	}
}

func TestLoadFrom_AdminCredentialsOptional(t *testing.T) {
	setSecrets(t)
	t.Setenv("ADMIN_USERNAME", "")
	t.Setenv("ADMIN_PASSWORD_HASH", "")

	cfg, err := loadComplete(t)
	if err != nil {
		t.Fatalf("LoadFrom() unexpected error: %v", err)
	}
	if cfg.AdminUsername != "" || cfg.AdminPasswordHash != "" {
		t.Error("admin credentials should stay empty when unset")
	}
}

func TestLoadFrom_RejectsNonPositiveDurations(t *testing.T) {
	setSecrets(t)

	yaml := strings.Replace(completeYAML, "  call_timeout: 5s", "  call_timeout: 0s", 1)
	configPath, envPath := writeConfig(t, yaml)

	_, err := LoadFrom(configPath, envPath)
	if err == nil {
		t.Fatal("a zero db.call_timeout should be rejected")
	}
	if !strings.Contains(err.Error(), "db.call_timeout") {
		t.Errorf("error should name db.call_timeout, got: %v", err)
	}
}

func TestLoadFrom_RejectsUsernameRangeInversion(t *testing.T) {
	setSecrets(t)

	yaml := strings.Replace(completeYAML, "  max_username_length: 50", "  max_username_length: 2", 1)
	configPath, envPath := writeConfig(t, yaml)

	if _, err := LoadFrom(configPath, envPath); err == nil {
		t.Error("min_username_length above max_username_length should be rejected")
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

func TestReadYAML_ParsesSectionsFlatKeysAndComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
# leading comment
server:
  address: ":8080"   # trailing comment
  read_timeout: 10s

flat_key: flat-value
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config.yaml: %v", err)
	}

	values, err := readYAML(path)
	if err != nil {
		t.Fatalf("readYAML() unexpected error: %v", err)
	}

	want := map[string]string{
		"server.address":      ":8080",
		"server.read_timeout": "10s",
		"flat_key":            "flat-value",
	}
	for key, wantValue := range want {
		if values[key] != wantValue {
			t.Errorf("readYAML()[%q] = %q, want %q", key, values[key], wantValue)
		}
	}
}

func TestReadYAML_MissingFileYieldsEmptyMap(t *testing.T) {
	values, err := readYAML(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("readYAML() on a missing file returned an error: %v", err)
	}
	if len(values) != 0 {
		t.Errorf("readYAML() on a missing file = %v, want an empty map", values)
	}
}

func TestReadDotEnv_ParsesKeysAndStripsMatchingQuotes(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	content := "# a comment\n\nQUOTED=\"quoted value\"\nBARE=bare-value\nUNBALANCED=\"still-quoted\nNO_EQUALS_SIGN\n"
	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp .env: %v", err)
	}

	values, err := readDotEnv(envPath)
	if err != nil {
		t.Fatalf("readDotEnv() unexpected error: %v", err)
	}

	want := map[string]string{
		"QUOTED":     "quoted value",
		"BARE":       "bare-value",
		"UNBALANCED": `"still-quoted`,
	}
	for key, wantValue := range want {
		if values[key] != wantValue {
			t.Errorf("readDotEnv()[%q] = %q, want %q", key, values[key], wantValue)
		}
	}
	if _, ok := values["NO_EQUALS_SIGN"]; ok {
		t.Error("a line with no '=' should be skipped, not stored")
	}
}

func TestReadDotEnv_MissingFileIsNotAnError(t *testing.T) {
	values, err := readDotEnv(filepath.Join(t.TempDir(), "does-not-exist.env"))
	if err != nil {
		t.Fatalf("readDotEnv() on a missing file returned an error: %v", err)
	}
	if len(values) != 0 {
		t.Errorf("readDotEnv() on a missing file = %v, want an empty map", values)
	}
}

func TestPasswordHashingCost_ReadsOnlyThatSetting(t *testing.T) {
	os.Unsetenv("JWT_SECRET")
	os.Unsetenv("DB_PASSWORD")
	os.Unsetenv("PASSWORD_BCRYPT_COST")
	t.Cleanup(func() {
		os.Unsetenv("JWT_SECRET")
		os.Unsetenv("DB_PASSWORD")
	})

	configPath, _ := writeConfig(t, completeYAML)

	// No secrets are set, so a full Load would fail; this must not.
	cost, err := PasswordHashingCost(configPath)
	if err != nil {
		t.Fatalf("PasswordHashingCost() unexpected error: %v", err)
	}
	if cost != 12 {
		t.Errorf("PasswordHashingCost() = %d, want 12", cost)
	}
}

func TestPasswordHashingCost_ReportsMissingSetting(t *testing.T) {
	os.Unsetenv("PASSWORD_BCRYPT_COST")
	configPath, _ := writeConfig(t, "log:\n  level: info\n")

	if _, err := PasswordHashingCost(configPath); err == nil {
		t.Error("PasswordHashingCost() with no password.bcrypt_cost should return an error")
	}
}

func TestLoadFrom_EmptyEnvironmentVariableFallsBackToYAML(t *testing.T) {
	setSecrets(t)
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("CORS_ALLOW_ORIGINS", "")
	t.Setenv("RUN_MIGRATIONS", "")

	cfg, err := loadComplete(t)
	if err != nil {
		t.Fatalf("LoadFrom() unexpected error: %v", err)
	}

	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want the config.yaml value %q", cfg.LogLevel, "info")
	}
	if cfg.CORSAllowOrigins != "*" {
		t.Errorf("CORSAllowOrigins = %q, want the config.yaml value %q", cfg.CORSAllowOrigins, "*")
	}
	if !cfg.RunMigrations {
		t.Error("RunMigrations = false, want the config.yaml value true")
	}
}

func TestLoadFrom_RejectsOutOfRangePort(t *testing.T) {
	setSecrets(t)

	for _, port := range []string{"70000", "65536", "0", "-1"} {
		t.Run(port, func(t *testing.T) {
			t.Setenv("DB_PORT", port)

			_, err := loadComplete(t)
			if err == nil {
				t.Fatalf("DB_PORT=%s should be rejected", port)
			}
			if !strings.Contains(err.Error(), "db.port") {
				t.Errorf("error should name db.port, got: %v", err)
			}
		})
	}
}

func TestLoadFrom_UnreadableConfigFileIsAnError(t *testing.T) {
	setSecrets(t)
	dir := t.TempDir()

	configPath := filepath.Join(dir, "config.yaml")
	if err := os.Mkdir(configPath, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, err := LoadFrom(configPath, filepath.Join(dir, ".env"))
	if err == nil {
		t.Fatal("an unreadable config.yaml should be an error")
	}
	if !strings.Contains(err.Error(), "read config") {
		t.Errorf("error should say the config could not be read, got: %v", err)
	}
	if strings.Contains(err.Error(), "is not set") {
		t.Errorf("an unreadable file must not be reported as missing settings, got: %v", err)
	}
}

func TestLoadFrom_LeavesProcessEnvironmentUntouched(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(completeYAML), 0o600); err != nil {
		t.Fatalf("write temp config.yaml: %v", err)
	}

	envPath := filepath.Join(dir, ".env")
	dotenv := "DB_PASSWORD=from-dotenv\nJWT_SECRET=" + strings.Repeat("d", 32) +
		"\nCONFIG_TEST_SIDE_EFFECT=should-not-leak\n"
	if err := os.WriteFile(envPath, []byte(dotenv), 0o600); err != nil {
		t.Fatalf("write temp .env: %v", err)
	}

	os.Unsetenv("DB_PASSWORD")
	os.Unsetenv("JWT_SECRET")
	os.Unsetenv("CONFIG_TEST_SIDE_EFFECT")
	t.Cleanup(func() {
		os.Unsetenv("DB_PASSWORD")
		os.Unsetenv("JWT_SECRET")
		os.Unsetenv("CONFIG_TEST_SIDE_EFFECT")
	})

	if _, err := LoadFrom(configPath, envPath); err != nil {
		t.Fatalf("LoadFrom() unexpected error: %v", err)
	}

	for _, key := range []string{"DB_PASSWORD", "JWT_SECRET", "CONFIG_TEST_SIDE_EFFECT"} {
		if value, ok := os.LookupEnv(key); ok {
			t.Errorf("LoadFrom() exported %s=%q into the process environment", key, value)
		}
	}
}

func TestLoadFrom_OptionalSecretsComeFromDotEnv(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(completeYAML), 0o600); err != nil {
		t.Fatalf("write temp config.yaml: %v", err)
	}

	envPath := filepath.Join(dir, ".env")
	dotenv := "DB_PASSWORD=s3cret\nJWT_SECRET=" + strings.Repeat("e", 32) +
		"\nADMIN_USERNAME=root\nADMIN_PASSWORD_HASH=$2a$12$fakehashvalue\n"
	if err := os.WriteFile(envPath, []byte(dotenv), 0o600); err != nil {
		t.Fatalf("write temp .env: %v", err)
	}

	for _, key := range []string{"DB_PASSWORD", "JWT_SECRET", "ADMIN_USERNAME", "ADMIN_PASSWORD_HASH"} {
		os.Unsetenv(key)
		t.Cleanup(func(key string) func() { return func() { os.Unsetenv(key) } }(key))
	}

	cfg, err := LoadFrom(configPath, envPath)
	if err != nil {
		t.Fatalf("LoadFrom() unexpected error: %v", err)
	}

	if cfg.AdminUsername != "root" {
		t.Errorf("AdminUsername = %q, want %q", cfg.AdminUsername, "root")
	}
	if cfg.AdminPasswordHash == "" {
		t.Error("AdminPasswordHash should have been read from .env")
	}
}

func TestLoadFrom_RejectsPageSizeAboveMaximum(t *testing.T) {
	setSecrets(t)

	yaml := strings.Replace(completeYAML, "  default_page_size: 20", "  default_page_size: 500", 1)
	configPath, envPath := writeConfig(t, yaml)

	_, err := LoadFrom(configPath, envPath)
	if err == nil {
		t.Fatal("a default page size above the maximum should be rejected")
	}
	if !strings.Contains(err.Error(), "pagination.default_page_size") {
		t.Errorf("error should name the key, got: %v", err)
	}
}

func TestLoadFrom_RejectsRefreshTTLShorterThanAccessTTL(t *testing.T) {
	setSecrets(t)

	yaml := strings.Replace(completeYAML, "  refresh_ttl: 720h", "  refresh_ttl: 30m", 1)
	configPath, envPath := writeConfig(t, yaml)

	_, err := LoadFrom(configPath, envPath)
	if err == nil {
		t.Fatal("a refresh TTL shorter than the access TTL should be rejected")
	}
	if !strings.Contains(err.Error(), "jwt.refresh_ttl") {
		t.Errorf("error should name the key, got: %v", err)
	}
}
