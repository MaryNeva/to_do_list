// Package config loads process configuration from environment variables
// (optionally pre-populated from a local .env file), validates it and
// exposes it as a typed Config.
//
// This replaces the original project's reflect-based YAML loader
// (configs/handler.yaml), which committed the JWT signing secret and the
// bootstrap admin's password hash directly into the git repository. Secrets
// now come from the environment (or a git-ignored .env file, see
// .env.example) and Load fails fast with a clear error if a required value
// is missing or looks unsafe, instead of the process starting up and
// silently signing tokens with a weak/default secret.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env string

	ServerAddress   string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration

	DBHost     string
	DBPort     uint16
	DBName     string
	DBUser     string
	DBPassword string
	DBSSLMode  string

	JWTSecret string
	JWTTTL    time.Duration
	JWTIssuer string

	AdminUsername     string
	AdminPasswordHash string

	CORSAllowOrigins string

	RunMigrations  bool
	MigrationsPath string

	LogLevel  string
	LogFormat string
}

func Load() (Config, error) {
	loadDotEnv(".env")

	cfg := Config{
		Env: getEnv("APP_ENV", "development"),

		ServerAddress:   getEnv("SERVER_ADDRESS", ":8080"),
		ReadTimeout:     getDuration("SERVER_READ_TIMEOUT", 10*time.Second),
		WriteTimeout:    getDuration("SERVER_WRITE_TIMEOUT", 10*time.Second),
		ShutdownTimeout: getDuration("SERVER_SHUTDOWN_TIMEOUT", 15*time.Second),

		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     uint16(getInt("DB_PORT", 5432)),
		DBName:     getEnv("DB_NAME", "to_do"),
		DBUser:     getEnv("DB_USER", "postgres"),
		DBPassword: getEnv("DB_PASSWORD", ""),
		DBSSLMode:  getEnv("DB_SSLMODE", "disable"),

		JWTSecret: getEnv("JWT_SECRET", ""),
		JWTTTL:    getDuration("JWT_TTL", time.Hour),
		JWTIssuer: getEnv("JWT_ISSUER", "to-do-list"),

		AdminUsername:     getEnv("ADMIN_USERNAME", ""),
		AdminPasswordHash: getEnv("ADMIN_PASSWORD_HASH", ""),

		CORSAllowOrigins: getEnv("CORS_ALLOW_ORIGINS", "*"),

		RunMigrations:  getBool("RUN_MIGRATIONS", true),
		MigrationsPath: getEnv("MIGRATIONS_PATH", "migrations"),

		LogLevel:  getEnv("LOG_LEVEL", "info"),
		LogFormat: getEnv("LOG_FORMAT", "json"),
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

const minJWTSecretLen = 32

func (c Config) validate() error {
	var problems []string

	if len(c.JWTSecret) < minJWTSecretLen {
		problems = append(problems, fmt.Sprintf("JWT_SECRET must be set and at least %d characters long (got %d)", minJWTSecretLen, len(c.JWTSecret)))
	}
	if c.DBPassword == "" {
		problems = append(problems, "DB_PASSWORD must be set")
	}
	if (c.AdminUsername == "") != (c.AdminPasswordHash == "") {
		problems = append(problems, "ADMIN_USERNAME and ADMIN_PASSWORD_HASH must both be set, or both left empty")
	}
	if c.ServerAddress == "" {
		problems = append(problems, "SERVER_ADDRESS must not be empty")
	}

	if len(problems) > 0 {
		return fmt.Errorf("invalid configuration:\n  - %s", strings.Join(problems, "\n  - "))
	}

	return nil
}

func (c Config) DatabaseDSN() string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.DBUser, c.DBPassword),
		Host:   net.JoinHostPort(c.DBHost, strconv.Itoa(int(c.DBPort))),
		Path:   "/" + c.DBName,
	}
	q := u.Query()
	q.Set("sslmode", c.DBSSLMode)
	u.RawQuery = q.Encode()
	return u.String()
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func getInt(key string, fallback int) int {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func getBool(key string, fallback bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func getDuration(key string, fallback time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)

		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}
}
