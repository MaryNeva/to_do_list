package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the fully resolved and validated process configuration.
type Config struct {
	AppName string
	Env     string

	ServerAddress   string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration

	DBHost           string
	DBPort           uint16
	DBName           string
	DBUser           string
	DBPassword       string
	DBSSLMode        string
	DBConnectTimeout time.Duration
	DBCallTimeout    time.Duration

	JWTSecret          string
	JWTTTL             time.Duration
	JWTIssuer          string
	JWTMinSecretLength int

	PasswordBcryptCost int
	PasswordMinLength  int

	UsernameMinLength int
	UsernameMaxLength int

	TaskMaxTitleLength       int
	TaskMaxDescriptionLength int

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
}

const (
	DefaultConfigPath       = "config.yaml"
	DefaultEnvPath          = ".env"
	maxBcryptPasswordLength = 72
)

// Load resolves configuration using the default file locations.
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

		DBHost:           r.str("DB_HOST", "db.host"),
		DBPort:           r.port("DB_PORT", "db.port"),
		DBName:           r.str("DB_NAME", "db.name"),
		DBUser:           r.str("DB_USER", "db.user"),
		DBPassword:       r.secret("DB_PASSWORD"),
		DBSSLMode:        r.str("DB_SSLMODE", "db.sslmode"),
		DBConnectTimeout: r.duration("DB_CONNECT_TIMEOUT", "db.connect_timeout"),
		DBCallTimeout:    r.duration("DB_CALL_TIMEOUT", "db.call_timeout"),

		JWTSecret:          r.secret("JWT_SECRET"),
		JWTTTL:             r.duration("JWT_TTL", "jwt.ttl"),
		JWTIssuer:          r.str("JWT_ISSUER", "jwt.issuer"),
		JWTMinSecretLength: r.integer("JWT_MIN_SECRET_LENGTH", "jwt.min_secret_length"),

		PasswordBcryptCost: r.integer("PASSWORD_BCRYPT_COST", "password.bcrypt_cost"),
		PasswordMinLength:  r.integer("PASSWORD_MIN_LENGTH", "password.min_length"),

		UsernameMinLength: r.integer("USER_MIN_USERNAME_LENGTH", "user.min_username_length"),
		UsernameMaxLength: r.integer("USER_MAX_USERNAME_LENGTH", "user.max_username_length"),

		TaskMaxTitleLength:       r.integer("TASK_MAX_TITLE_LENGTH", "task.max_title_length"),
		TaskMaxDescriptionLength: r.integer("TASK_MAX_DESCRIPTION_LENGTH", "task.max_description_length"),

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
	validatePositiveDuration("ratelimit.auth_window", c.RateLimitAuthWindow)
	validatePositiveDuration("health.ready_timeout", c.HealthReadyTimeout)

	validatePositiveInt("jwt.min_secret_length", c.JWTMinSecretLength)
	validatePositiveInt("password.bcrypt_cost", c.PasswordBcryptCost)
	validatePositiveInt("password.min_length", c.PasswordMinLength)
	validatePositiveInt("user.min_username_length", c.UsernameMinLength)
	validatePositiveInt("user.max_username_length", c.UsernameMaxLength)
	validatePositiveInt("task.max_title_length", c.TaskMaxTitleLength)
	validatePositiveInt("task.max_description_length", c.TaskMaxDescriptionLength)
	validatePositiveInt("ratelimit.auth_max_requests", c.RateLimitAuthMaxRequests)

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

func readDotEnv(path string) (map[string]string, error) {
	values := make(map[string]string)

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return values, nil
		}

		return nil, err
	}

	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}

		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}

		value = strings.TrimSpace(value)
		value = trimOptionalQuotes(value)

		values[key] = value
	}

	return values, nil
}

func readYAML(path string) (map[string]string, error) {
	values := make(map[string]string)

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return values, nil
		}

		return nil, err
	}

	var section string

	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		indented := line != trimmed

		key, rawValue, found := strings.Cut(trimmed, ":")
		if !found {
			continue
		}

		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}

		value := parseScalar(rawValue)

		if !indented {
			if value == "" {
				section = key
				continue
			}

			section = ""
			values[key] = value
			continue
		}

		if section == "" {
			continue
		}

		values[section+"."+key] = value
	}

	return values, nil
}

// parseScalar extracts a scalar value from the supported YAML subset.
func parseScalar(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}

	if value[0] == '"' || value[0] == '\'' {
		quote := value[0]

		if end := strings.IndexByte(value[1:], quote); end >= 0 {
			return value[1 : end+1]
		}

		return value[1:]
	}

	if index := strings.Index(value, " #"); index >= 0 {
		value = value[:index]
	}

	return strings.TrimSpace(value)
}

func trimOptionalQuotes(value string) string {
	if len(value) < 2 {
		return value
	}

	first := value[0]
	last := value[len(value)-1]

	if (first == '"' && last == '"') ||
		(first == '\'' && last == '\'') {
		return value[1 : len(value)-1]
	}

	return value
}
