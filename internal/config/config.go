package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"to-do-list/internal/auth/password"
)

// Config is the fully resolved and validated process configuration.
type Config struct {
	AppName string
	Env     string

	ServerAddress   string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration
	MaxBodyBytes    int
	TrustedProxies  []string
	ProxyHeader     string

	DBHost           string
	DBPort           uint16
	DBName           string
	DBUser           string
	DBPassword       string
	DBSSLMode        string
	DBConnectTimeout time.Duration
	DBCallTimeout    time.Duration
	DBMaxConns       int
	DBMinConns       int
	DBMaxConnLife    time.Duration
	DBMaxConnIdle    time.Duration

	JWTSecret          string
	JWTTTL             time.Duration
	JWTRefreshTTL      time.Duration
	JWTIssuer          string
	JWTMinSecretLength int

	PasswordBcryptCost    int
	PasswordMinLength     int
	PasswordMaxConcurrent int

	UsernameMinLength int
	UsernameMaxLength int

	TaskMaxTitleLength       int
	TaskMaxDescriptionLength int

	DefaultPageSize int
	MaxPageSize     int

	CleanupInterval       time.Duration
	RefreshTokenRetention time.Duration

	RateLimitAuthMaxRequests int
	RateLimitAuthWindow      time.Duration

	CORSAllowOrigins string
	CORSAllowMethods string
	CORSAllowHeaders string

	HealthReadyTimeout time.Duration

	AdminUsername     string
	AdminPasswordHash string

	RunMigrations  bool
	MigrationsPath string

	LogLevel  string
	LogFormat string

	MetricsEnabled   bool
	MetricsPath      string
	MetricsNamespace string
	// MetricsAddress moves /metrics to a listener of its own. Empty keeps it
	// on the public one, which is a development convenience only.
	MetricsAddress string
}

const (
	DefaultConfigPath       = "config.yaml"
	DefaultEnvPath          = ".env"
	maxBcryptPasswordLength = 72
)

func Load() (Config, error) {
	return LoadFrom(DefaultConfigPath, DefaultEnvPath)
}

func LoadFrom(configPath, envPath string) (Config, error) {
	yamlValues, err := readYAML(configPath)
	if err != nil {
		return Config{}, fmt.Errorf("read config %q: %w", configPath, err)
	}

	dotenvValues, err := readDotEnv(envPath)
	if err != nil {
		return Config{}, fmt.Errorf("read env file %q: %w", envPath, err)
	}

	r := resolver{
		yaml:   yamlValues,
		dotenv: dotenvValues,
	}

	cfg := Config{
		AppName: r.str("APP_NAME", "app.name"),
		Env:     r.str("APP_ENV", "app.env"),

		ServerAddress:   r.str("SERVER_ADDRESS", "server.address"),
		ReadTimeout:     r.duration("SERVER_READ_TIMEOUT", "server.read_timeout"),
		WriteTimeout:    r.duration("SERVER_WRITE_TIMEOUT", "server.write_timeout"),
		ShutdownTimeout: r.duration("SERVER_SHUTDOWN_TIMEOUT", "server.shutdown_timeout"),
		MaxBodyBytes:    r.integer("SERVER_MAX_BODY_BYTES", "server.max_body_bytes"),
		TrustedProxies:  r.list("SERVER_TRUSTED_PROXIES", "server.trusted_proxies"),
		ProxyHeader:     r.optionalStr("SERVER_PROXY_HEADER", "server.proxy_header"),

		DBHost:           r.str("DB_HOST", "db.host"),
		DBPort:           r.port("DB_PORT", "db.port"),
		DBName:           r.str("DB_NAME", "db.name"),
		DBUser:           r.str("DB_USER", "db.user"),
		DBPassword:       r.secret("DB_PASSWORD"),
		DBSSLMode:        r.str("DB_SSLMODE", "db.sslmode"),
		DBConnectTimeout: r.duration("DB_CONNECT_TIMEOUT", "db.connect_timeout"),
		DBCallTimeout:    r.duration("DB_CALL_TIMEOUT", "db.call_timeout"),
		DBMaxConns:       r.integer("DB_MAX_CONNECTIONS", "db.max_connections"),
		DBMinConns:       r.integer("DB_MIN_CONNECTIONS", "db.min_connections"),
		DBMaxConnLife:    r.duration("DB_MAX_CONN_LIFETIME", "db.max_conn_lifetime"),
		DBMaxConnIdle:    r.duration("DB_MAX_CONN_IDLE_TIME", "db.max_conn_idle_time"),

		JWTSecret:          r.secret("JWT_SECRET"),
		JWTTTL:             r.duration("JWT_TTL", "jwt.ttl"),
		JWTRefreshTTL:      r.duration("JWT_REFRESH_TTL", "jwt.refresh_ttl"),
		JWTIssuer:          r.str("JWT_ISSUER", "jwt.issuer"),
		JWTMinSecretLength: r.integer("JWT_MIN_SECRET_LENGTH", "jwt.min_secret_length"),

		PasswordBcryptCost:    r.integer("PASSWORD_BCRYPT_COST", "password.bcrypt_cost"),
		PasswordMinLength:     r.integer("PASSWORD_MIN_LENGTH", "password.min_length"),
		PasswordMaxConcurrent: r.integer("PASSWORD_MAX_CONCURRENT_HASHES", "password.max_concurrent_hashes"),

		UsernameMinLength: r.integer("USER_MIN_USERNAME_LENGTH", "user.min_username_length"),
		UsernameMaxLength: r.integer("USER_MAX_USERNAME_LENGTH", "user.max_username_length"),

		TaskMaxTitleLength:       r.integer("TASK_MAX_TITLE_LENGTH", "task.max_title_length"),
		TaskMaxDescriptionLength: r.integer("TASK_MAX_DESCRIPTION_LENGTH", "task.max_description_length"),

		DefaultPageSize: r.integer("PAGINATION_DEFAULT_PAGE_SIZE", "pagination.default_page_size"),
		MaxPageSize:     r.integer("PAGINATION_MAX_PAGE_SIZE", "pagination.max_page_size"),

		CleanupInterval:       r.duration("CLEANUP_INTERVAL", "cleanup.interval"),
		RefreshTokenRetention: r.duration("CLEANUP_REFRESH_TOKEN_RETENTION", "cleanup.refresh_token_retention"),

		RateLimitAuthMaxRequests: r.integer("RATELIMIT_AUTH_MAX_REQUESTS", "ratelimit.auth_max_requests"),
		RateLimitAuthWindow:      r.duration("RATELIMIT_AUTH_WINDOW", "ratelimit.auth_window"),

		CORSAllowOrigins: r.str("CORS_ALLOW_ORIGINS", "cors.allow_origins"),
		CORSAllowMethods: r.str("CORS_ALLOW_METHODS", "cors.allow_methods"),
		CORSAllowHeaders: r.str("CORS_ALLOW_HEADERS", "cors.allow_headers"),

		HealthReadyTimeout: r.duration("HEALTH_READY_TIMEOUT", "health.ready_timeout"),

		AdminUsername:     r.optionalSecret("ADMIN_USERNAME"),
		AdminPasswordHash: r.optionalSecret("ADMIN_PASSWORD_HASH"),

		RunMigrations:  r.boolean("RUN_MIGRATIONS", "run_migrations"),
		MigrationsPath: r.str("MIGRATIONS_PATH", "migrations_path"),

		LogLevel:  r.str("LOG_LEVEL", "log.level"),
		LogFormat: r.str("LOG_FORMAT", "log.format"),

		MetricsEnabled:   r.boolean("METRICS_ENABLED", "observability.metrics_enabled"),
		MetricsPath:      r.str("METRICS_PATH", "observability.metrics_path"),
		MetricsNamespace: r.str("METRICS_NAMESPACE", "observability.metrics_namespace"),
		MetricsAddress:   r.optionalStr("METRICS_ADDRESS", "observability.metrics_address"),
	}

	if err := r.err(configPath); err != nil {
		return Config{}, err
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func PasswordHashingCost(configPath string) (int, error) {
	yamlValues, err := readYAML(configPath)
	if err != nil {
		return 0, fmt.Errorf("read config %q: %w", configPath, err)
	}

	r := resolver{
		yaml: yamlValues,
	}

	cost := r.integer(
		"PASSWORD_BCRYPT_COST",
		"password.bcrypt_cost",
	)

	if err := r.err(configPath); err != nil {
		return 0, err
	}

	if cost <= 0 {
		return 0, fmt.Errorf(
			"invalid configuration:\n  - password.bcrypt_cost must be greater than zero",
		)
	}

	return cost, nil
}

func DatabaseURL(configPath, envPath string) (string, error) {
	yamlValues, err := readYAML(configPath)
	if err != nil {
		return "", fmt.Errorf("read config %q: %w", configPath, err)
	}

	dotenvValues, err := readDotEnv(envPath)
	if err != nil {
		return "", fmt.Errorf("read env file %q: %w", envPath, err)
	}

	r := resolver{yaml: yamlValues, dotenv: dotenvValues}

	cfg := Config{
		DBHost:     r.str("DB_HOST", "db.host"),
		DBPort:     r.port("DB_PORT", "db.port"),
		DBName:     r.str("DB_NAME", "db.name"),
		DBUser:     r.str("DB_USER", "db.user"),
		DBPassword: r.secret("DB_PASSWORD"),
		DBSSLMode:  r.str("DB_SSLMODE", "db.sslmode"),
	}

	if err := r.err(configPath); err != nil {
		return "", err
	}

	return cfg.DatabaseDSN(), nil
}

type resolver struct {
	yaml     map[string]string
	dotenv   map[string]string
	problems []string
}

func (r *resolver) lookup(envKey, yamlKey string) (string, bool) {
	if value, ok := os.LookupEnv(envKey); ok && value != "" {
		return value, true
	}

	if value := r.dotenv[envKey]; value != "" {
		return value, true
	}

	if value := r.yaml[yamlKey]; value != "" {
		return value, true
	}

	return "", false
}

func (r *resolver) str(envKey, yamlKey string) string {
	value, ok := r.lookup(envKey, yamlKey)
	if !ok {
		r.missing(envKey, yamlKey)
		return ""
	}

	return value
}

// optionalStr resolves a setting that may legitimately be empty.
func (r *resolver) optionalStr(envKey, yamlKey string) string {
	value, _ := r.lookup(envKey, yamlKey)
	return value
}

// list splits a comma-separated setting; empty means an empty list.
func (r *resolver) list(envKey, yamlKey string) []string {
	value, ok := r.lookup(envKey, yamlKey)
	if !ok {
		return nil
	}

	var items []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

func (r *resolver) integer(envKey, yamlKey string) int {
	value, ok := r.lookup(envKey, yamlKey)
	if !ok {
		r.missing(envKey, yamlKey)
		return 0
	}

	n, err := strconv.Atoi(value)
	if err != nil {
		r.invalid(envKey, yamlKey, value, "a whole number")
		return 0
	}

	return n
}

func (r *resolver) port(envKey, yamlKey string) uint16 {
	value, ok := r.lookup(envKey, yamlKey)
	if !ok {
		r.missing(envKey, yamlKey)
		return 0
	}

	port, err := strconv.ParseUint(value, 10, 16)
	if err != nil || port == 0 {
		r.invalid(
			envKey,
			yamlKey,
			value,
			"an integer between 1 and 65535",
		)
		return 0
	}

	return uint16(port)
}

func (r *resolver) boolean(envKey, yamlKey string) bool {
	value, ok := r.lookup(envKey, yamlKey)
	if !ok {
		r.missing(envKey, yamlKey)
		return false
	}

	result, err := strconv.ParseBool(value)
	if err != nil {
		r.invalid(envKey, yamlKey, value, "true or false")
		return false
	}

	return result
}

func (r *resolver) duration(envKey, yamlKey string) time.Duration {
	value, ok := r.lookup(envKey, yamlKey)
	if !ok {
		r.missing(envKey, yamlKey)
		return 0
	}

	duration, err := time.ParseDuration(value)
	if err != nil {
		r.invalid(
			envKey,
			yamlKey,
			value,
			`a duration such as "10s" or "1h"`,
		)
		return 0
	}

	return duration
}

func (r *resolver) secret(envKey string) string {
	if value, ok := os.LookupEnv(envKey); ok && value != "" {
		return value
	}

	if value := r.dotenv[envKey]; value != "" {
		return value
	}

	r.problems = append(
		r.problems,
		fmt.Sprintf(
			"%s is not set (it is a secret: put it in .env or the environment, never in config.yaml)",
			envKey,
		),
	)

	return ""
}

// optionalSecret resolves an optional secret without consulting config.yaml.
func (r *resolver) optionalSecret(envKey string) string {
	if value, ok := os.LookupEnv(envKey); ok && value != "" {
		return value
	}

	return r.dotenv[envKey]
}

func (r *resolver) missing(envKey, yamlKey string) {
	r.problems = append(
		r.problems,
		fmt.Sprintf(
			"%s is not set (expected in config.yaml as %q, or as the environment variable %s)",
			yamlKey,
			yamlKey,
			envKey,
		),
	)
}

func (r *resolver) invalid(envKey, yamlKey, value, want string) {
	r.problems = append(
		r.problems,
		fmt.Sprintf(
			"%s (or %s) is %q, which is not %s",
			yamlKey,
			envKey,
			value,
			want,
		),
	)
}

func (r *resolver) err(configPath string) error {
	if len(r.problems) == 0 {
		return nil
	}

	var hint string

	if len(r.yaml) == 0 {
		hint = fmt.Sprintf(
			"\n\n%s was not found or is empty - copy it from the repository, "+
				"or supply every setting as an environment variable.",
			configPath,
		)
	}

	return fmt.Errorf(
		"invalid configuration:\n  - %s%s",
		strings.Join(r.problems, "\n  - "),
		hint,
	)
}

// validate checks semantic constraints that cannot be handled while parsing.
func (c Config) validate() error {
	var problems []string

	validatePositiveDuration := func(name string, value time.Duration) {
		if value <= 0 {
			problems = append(
				problems,
				fmt.Sprintf("%s must be greater than zero", name),
			)
		}
	}

	validatePositiveInt := func(name string, value int) {
		if value <= 0 {
			problems = append(
				problems,
				fmt.Sprintf("%s must be greater than zero", name),
			)
		}
	}

	validatePositiveDuration("server.read_timeout", c.ReadTimeout)
	validatePositiveDuration("server.write_timeout", c.WriteTimeout)
	validatePositiveDuration("server.shutdown_timeout", c.ShutdownTimeout)
	validatePositiveDuration("db.connect_timeout", c.DBConnectTimeout)
	validatePositiveDuration("db.call_timeout", c.DBCallTimeout)
	validatePositiveDuration("jwt.ttl", c.JWTTTL)
	validatePositiveDuration("jwt.refresh_ttl", c.JWTRefreshTTL)
	validatePositiveDuration("ratelimit.auth_window", c.RateLimitAuthWindow)
	validatePositiveDuration("health.ready_timeout", c.HealthReadyTimeout)
	validatePositiveDuration("cleanup.interval", c.CleanupInterval)
	validatePositiveDuration("cleanup.refresh_token_retention", c.RefreshTokenRetention)

	validatePositiveInt("jwt.min_secret_length", c.JWTMinSecretLength)
	validatePositiveInt("password.bcrypt_cost", c.PasswordBcryptCost)
	validatePositiveInt("password.min_length", c.PasswordMinLength)
	validatePositiveInt("user.min_username_length", c.UsernameMinLength)
	validatePositiveInt("user.max_username_length", c.UsernameMaxLength)
	validatePositiveInt("task.max_title_length", c.TaskMaxTitleLength)
	validatePositiveInt("task.max_description_length", c.TaskMaxDescriptionLength)
	validatePositiveInt("ratelimit.auth_max_requests", c.RateLimitAuthMaxRequests)
	validatePositiveInt("pagination.default_page_size", c.DefaultPageSize)
	validatePositiveInt("pagination.max_page_size", c.MaxPageSize)
	validatePositiveInt("server.max_body_bytes", c.MaxBodyBytes)
	validatePositiveInt("db.max_connections", c.DBMaxConns)
	validatePositiveInt("password.max_concurrent_hashes", c.PasswordMaxConcurrent)
	validatePositiveDuration("db.max_conn_lifetime", c.DBMaxConnLife)
	validatePositiveDuration("db.max_conn_idle_time", c.DBMaxConnIdle)

	if c.DBMinConns < 0 {
		problems = append(problems, "db.min_connections must be zero or more")
	}
	if c.DBMinConns > c.DBMaxConns {
		problems = append(problems, fmt.Sprintf(
			"db.min_connections (%d) is above db.max_connections (%d)",
			c.DBMinConns, c.DBMaxConns))
	}
	if c.DBMaxConnIdle > c.DBMaxConnLife {
		problems = append(problems, fmt.Sprintf(
			"db.max_conn_idle_time (%s) is above db.max_conn_lifetime (%s), so it can never take effect",
			c.DBMaxConnIdle, c.DBMaxConnLife))
	}

	// A forwarding header is client-supplied. Believing it without naming who
	// may set it lets anyone claim any address, which would hand every caller
	// their own rate-limit budget.
	if c.ProxyHeader != "" && len(c.TrustedProxies) == 0 {
		problems = append(problems,
			"server.proxy_header is set but server.trusted_proxies is empty: "+
				"any client could then spoof its address")
	}
	for _, proxy := range c.TrustedProxies {
		if net.ParseIP(proxy) == nil {
			if _, _, err := net.ParseCIDR(proxy); err != nil {
				problems = append(problems, fmt.Sprintf(
					"server.trusted_proxies contains %q, which is neither an IP address nor a CIDR range", proxy))
			}
		}
	}

	if c.MetricsAddress != "" {
		if _, _, err := net.SplitHostPort(c.MetricsAddress); err != nil {
			problems = append(problems, fmt.Sprintf(
				"observability.metrics_address is %q, which is not a host:port such as \":9101\"", c.MetricsAddress))
		}
	}

	if len(c.JWTSecret) < c.JWTMinSecretLength {
		problems = append(
			problems,
			fmt.Sprintf(
				"JWT_SECRET must be at least %d characters long "+
					"(jwt.min_secret_length), got %d",
				c.JWTMinSecretLength,
				len(c.JWTSecret),
			),
		)
	}

	if (c.AdminUsername == "") != (c.AdminPasswordHash == "") {
		problems = append(
			problems,
			"ADMIN_USERNAME and ADMIN_PASSWORD_HASH must both be set, or both left empty",
		)
	}

	if c.AdminPasswordHash != "" {
		if err := password.ValidHash(c.AdminPasswordHash); err != nil {
			problems = append(problems, fmt.Sprintf(
				"ADMIN_PASSWORD_HASH is not a bcrypt hash (%v); generate one with 'make gen-admin-hash'", err))
		}
	}

	if c.PasswordMinLength > maxBcryptPasswordLength {
		problems = append(
			problems,
			fmt.Sprintf(
				"password.min_length is %d, above bcrypt's %d-byte limit - "+
					"no password could satisfy it",
				c.PasswordMinLength,
				maxBcryptPasswordLength,
			),
		)
	}

	if c.DefaultPageSize > c.MaxPageSize {
		problems = append(
			problems,
			fmt.Sprintf(
				"pagination.default_page_size (%d) is above pagination.max_page_size (%d)",
				c.DefaultPageSize,
				c.MaxPageSize,
			),
		)
	}

	if c.JWTRefreshTTL <= c.JWTTTL {
		problems = append(
			problems,
			fmt.Sprintf(
				"jwt.refresh_ttl (%s) must be longer than jwt.ttl (%s)",
				c.JWTRefreshTTL,
				c.JWTTTL,
			),
		)
	}

	if !strings.HasPrefix(c.MetricsPath, "/") {
		problems = append(
			problems,
			fmt.Sprintf(
				"observability.metrics_path (%q) must start with a slash",
				c.MetricsPath,
			),
		)
	}

	if !isPrometheusName(c.MetricsNamespace) {
		problems = append(
			problems,
			fmt.Sprintf(
				"observability.metrics_namespace (%q) must match [a-zA-Z_][a-zA-Z0-9_]* - "+
					"it is prefixed to every metric name",
				c.MetricsNamespace,
			),
		)
	}

	if c.UsernameMinLength > c.UsernameMaxLength {
		problems = append(
			problems,
			fmt.Sprintf(
				"user.min_username_length (%d) is above "+
					"user.max_username_length (%d)",
				c.UsernameMinLength,
				c.UsernameMaxLength,
			),
		)
	}

	if len(problems) > 0 {
		return fmt.Errorf(
			"invalid configuration:\n  - %s",
			strings.Join(problems, "\n  - "),
		)
	}

	return nil
}

func isPrometheusName(s string) bool {
	if s == "" {
		return false
	}

	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}

	return true
}

func (c Config) DatabaseDSN() string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.DBUser, c.DBPassword),
		Host: net.JoinHostPort(
			c.DBHost,
			strconv.FormatUint(uint64(c.DBPort), 10),
		),
		Path: "/" + c.DBName,
	}

	query := u.Query()
	query.Set("sslmode", c.DBSSLMode)

	u.RawQuery = query.Encode()

	return u.String()
}

func ReadEnvFile(path string) (map[string]string, error) {
	values := make(map[string]string)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return values, nil
	}
	if err != nil {
		return nil, err
	}
	for n, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if !ok || !validEnvKey(key) {
			return nil, fmt.Errorf("invalid environment assignment at line %d", n+1)
		}
		if _, ok := values[key]; ok {
			return nil, fmt.Errorf("duplicate environment key %s at line %d", key, n+1)
		}
		if len(value) > 0 && (value[0] == '"' || value[0] == '\'') {
			if len(value) < 2 || value[len(value)-1] != value[0] {
				return nil, fmt.Errorf("unclosed environment quote at line %d", n+1)
			}
			value = value[1 : len(value)-1]
		}
		if strings.ContainsRune(value, 0) {
			return nil, fmt.Errorf("invalid environment value at line %d", n+1)
		}
		values[key] = value
	}
	return values, nil
}
func validEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, c := range key {
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
func readDotEnv(path string) (map[string]string, error) { return ReadEnvFile(path) }

// CommandEnvironment applies the same non-empty environment override policy as
// LoadFrom, for developer tools which previously sourced .env as shell code.
func CommandEnvironment(path string, environment []string) ([]string, error) {
	values, err := ReadEnvFile(path)
	if err != nil {
		return nil, err
	}
	for _, entry := range environment {
		k, v, ok := strings.Cut(entry, "=")
		if ok {
			if _, exists := values[k]; v != "" || !exists {
				values[k] = v
			}
		}
	}
	result := make([]string, 0, len(values))
	for k, v := range values {
		result = append(result, k+"="+v)
	}
	return result, nil
}

// fileConfig is the complete non-secret YAML schema. Pointers distinguish missing values from zero values.
type fileConfig struct {
	App struct {
		Name *string `yaml:"name"`
		Env  *string `yaml:"env"`
	} `yaml:"app"`
	Server struct {
		Address         *string `yaml:"address"`
		ReadTimeout     *string `yaml:"read_timeout"`
		WriteTimeout    *string `yaml:"write_timeout"`
		ShutdownTimeout *string `yaml:"shutdown_timeout"`
		MaxBodyBytes    *int    `yaml:"max_body_bytes"`
		TrustedProxies  *string `yaml:"trusted_proxies"`
		ProxyHeader     *string `yaml:"proxy_header"`
	} `yaml:"server"`
	DB struct {
		Host           *string `yaml:"host"`
		Port           *int    `yaml:"port"`
		Name           *string `yaml:"name"`
		User           *string `yaml:"user"`
		SSLMode        *string `yaml:"sslmode"`
		ConnectTimeout *string `yaml:"connect_timeout"`
		CallTimeout    *string `yaml:"call_timeout"`
		MaxConns       *int    `yaml:"max_connections"`
		MinConns       *int    `yaml:"min_connections"`
		MaxConnLife    *string `yaml:"max_conn_lifetime"`
		MaxConnIdle    *string `yaml:"max_conn_idle_time"`
	} `yaml:"db"`
	JWT struct {
		TTL             *string `yaml:"ttl"`
		RefreshTTL      *string `yaml:"refresh_ttl"`
		Issuer          *string `yaml:"issuer"`
		MinSecretLength *int    `yaml:"min_secret_length"`
	} `yaml:"jwt"`
	Password struct {
		BcryptCost    *int `yaml:"bcrypt_cost"`
		MinLength     *int `yaml:"min_length"`
		MaxConcurrent *int `yaml:"max_concurrent_hashes"`
	} `yaml:"password"`
	User struct {
		MinUsernameLength *int `yaml:"min_username_length"`
		MaxUsernameLength *int `yaml:"max_username_length"`
	} `yaml:"user"`
	Task struct {
		MaxTitleLength       *int `yaml:"max_title_length"`
		MaxDescriptionLength *int `yaml:"max_description_length"`
	} `yaml:"task"`
	Pagination struct {
		DefaultPageSize *int `yaml:"default_page_size"`
		MaxPageSize     *int `yaml:"max_page_size"`
	} `yaml:"pagination"`
	Cleanup struct {
		Interval              *string `yaml:"interval"`
		RefreshTokenRetention *string `yaml:"refresh_token_retention"`
	} `yaml:"cleanup"`
	Ratelimit struct {
		AuthMaxRequests *int    `yaml:"auth_max_requests"`
		AuthWindow      *string `yaml:"auth_window"`
	} `yaml:"ratelimit"`
	CORS struct {
		AllowOrigins *string `yaml:"allow_origins"`
		AllowMethods *string `yaml:"allow_methods"`
		AllowHeaders *string `yaml:"allow_headers"`
	} `yaml:"cors"`
	Health struct {
		ReadyTimeout *string `yaml:"ready_timeout"`
	} `yaml:"health"`
	Log struct {
		Level  *string `yaml:"level"`
		Format *string `yaml:"format"`
	} `yaml:"log"`
	Observability struct {
		MetricsEnabled   *bool   `yaml:"metrics_enabled"`
		MetricsPath      *string `yaml:"metrics_path"`
		MetricsNamespace *string `yaml:"metrics_namespace"`
		MetricsAddress   *string `yaml:"metrics_address"`
	} `yaml:"observability"`
	RunMigrations  *bool   `yaml:"run_migrations"`
	MigrationsPath *string `yaml:"migrations_path"`
}

func readYAML(path string) (map[string]string, error) {
	values := make(map[string]string)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return values, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg fileConfig
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return values, nil
		}
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("configuration must contain exactly one YAML document")
	}
	flattenConfig(reflect.ValueOf(cfg), "", values)
	return values, nil
}
func flattenConfig(v reflect.Value, prefix string, out map[string]string) {
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		key := prefix + v.Type().Field(i).Tag.Get("yaml")
		if field.Kind() == reflect.Struct {
			flattenConfig(field, key+".", out)
		} else if !field.IsNil() {
			out[key] = fmt.Sprint(field.Elem().Interface())
		}
	}
}
