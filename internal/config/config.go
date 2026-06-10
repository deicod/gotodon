// Package config provides configuration management for Gotodon.
package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Config holds all configuration for Gotodon.
type Config struct {
	Server      ServerConfig      `mapstructure:"server"`
	Database    DatabaseConfig    `mapstructure:"database"`
	Redis       RedisConfig       `mapstructure:"redis"`
	Workers     WorkersConfig     `mapstructure:"workers"`
	Secrets     SecretsConfig     `mapstructure:"secrets"`
	Instance    InstanceConfig    `mapstructure:"instance"`
	Translation TranslationConfig `mapstructure:"translation"`
	Mail        MailConfig        `mapstructure:"mail"`
}

// ServerConfig holds HTTP server configuration.
type ServerConfig struct {
	Host           string `mapstructure:"host"`
	Port           int    `mapstructure:"port"`
	StreamingPort  int    `mapstructure:"streaming_port"`
	TrustedProxies string `mapstructure:"trusted_proxies"`
}

// DatabaseConfig holds PostgreSQL configuration.
type DatabaseConfig struct {
	Host         string `mapstructure:"host"`
	Port         int    `mapstructure:"port"`
	User         string `mapstructure:"user"`
	Password     string `mapstructure:"password"`
	Database     string `mapstructure:"database"`
	SSLMode      string `mapstructure:"sslmode"`
	MaxOpenConns int    `mapstructure:"max_open_conns"`
	MaxIdleConns int    `mapstructure:"max_idle_conns"`
}

// DSN returns the PostgreSQL connection string.
func (c *DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		c.User, c.Password, c.Host, c.Port, c.Database, c.SSLMode,
	)
}

// RedisConfig holds Redis configuration.
type RedisConfig struct {
	Host      string `mapstructure:"host"`
	Port      int    `mapstructure:"port"`
	Password  string `mapstructure:"password"`
	Database  int    `mapstructure:"database"`
	Namespace string `mapstructure:"namespace"`
}

// Addr returns the Redis address.
func (c *RedisConfig) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// WorkersConfig holds background worker configuration.
type WorkersConfig struct {
	Enabled     bool           `mapstructure:"enabled"`
	Concurrency int            `mapstructure:"concurrency"`
	Queues      map[string]int `mapstructure:"queues"`
}

// SecretsConfig holds secret keys for encryption and signing.
type SecretsConfig struct {
	SecretKeyBase string `mapstructure:"secret_key_base"`
	OTPSecret     string `mapstructure:"otp_secret"`
	VAPIDPrivate  string `mapstructure:"vapid_private_key"`
	VAPIDPublic   string `mapstructure:"vapid_public_key"`
}

// InstanceConfig holds instance-specific configuration.
type InstanceConfig struct {
	LocalDomain       string `mapstructure:"local_domain"`
	WebDomain         string `mapstructure:"web_domain"`
	SingleUserMode    bool   `mapstructure:"single_user_mode"`
	RegistrationsMode string `mapstructure:"registrations_mode"` // open, none, approved
}

// PublicWebDomain returns the browser-facing domain, preferring web_domain
// over local_domain, with a development fallback.
func (c InstanceConfig) PublicWebDomain() string {
	if domain := strings.TrimSpace(c.WebDomain); domain != "" {
		return domain
	}
	if domain := strings.TrimSpace(c.LocalDomain); domain != "" {
		return domain
	}
	return "localhost:3000"
}

// TranslationConfig holds translation service configuration.
type TranslationConfig struct {
	Backend  string `mapstructure:"backend"` // none, deepl, libretranslate
	APIKey   string `mapstructure:"api_key"`
	Endpoint string `mapstructure:"endpoint"` // For LibreTranslate only
	Plan     string `mapstructure:"plan"`     // For DeepL: free or pro
}

// Enabled returns true if translation is configured
func (c *TranslationConfig) Enabled() bool {
	return c.Backend != "" && c.Backend != "none"
}

// MailConfig holds outbound SMTP configuration.
type MailConfig struct {
	SMTPServer      string `mapstructure:"smtp_server"`
	SMTPPort        int    `mapstructure:"smtp_port"`
	SMTPLogin       string `mapstructure:"smtp_login"`
	SMTPPassword    string `mapstructure:"smtp_password"`
	SMTPAuthMethod  string `mapstructure:"smtp_auth_method"` // plain, login, cram_md5, none
	SMTPFromAddress string `mapstructure:"smtp_from_address"`
	SMTPDomain      string `mapstructure:"smtp_domain"`          // HELO/EHLO domain
	SMTPSSL         bool   `mapstructure:"smtp_ssl"`             // implicit TLS (SMTPS)
	SMTPStartTLS    string `mapstructure:"smtp_enable_starttls"` // auto, always, never
	SMTPNoVerify    bool   `mapstructure:"smtp_tls_no_verify"`   // skip certificate verification
	// SMTPVerifyMode mirrors Mastodon's SMTP_OPENSSL_VERIFY_MODE (peer, none);
	// "none" implies SMTPNoVerify.
	SMTPVerifyMode string `mapstructure:"smtp_openssl_verify_mode"`
}

// Enabled returns true if outbound mail is configured.
func (c *MailConfig) Enabled() bool {
	return c.SMTPServer != ""
}

// Load reads configuration from file and environment variables.
func Load() (*Config, error) {
	v := viper.New()

	// Set defaults
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.port", 3000)
	v.SetDefault("server.streaming_port", 4000)
	v.SetDefault("database.host", "localhost")
	v.SetDefault("database.port", 5432)
	v.SetDefault("database.user", "gotodon")
	v.SetDefault("database.database", "gotodon_development")
	v.SetDefault("database.sslmode", "disable")
	v.SetDefault("database.max_open_conns", 25)
	v.SetDefault("database.max_idle_conns", 5)
	v.SetDefault("redis.host", "localhost")
	v.SetDefault("redis.port", 6379)
	v.SetDefault("redis.database", 0)
	v.SetDefault("workers.enabled", true)
	v.SetDefault("workers.queues", map[string]int{
		"critical": 6,
		"default":  3,
		"low":      1,
	})
	v.SetDefault("instance.registrations_mode", "open")
	v.SetDefault("mail.smtp_port", 587)
	v.SetDefault("mail.smtp_auth_method", "plain")
	v.SetDefault("mail.smtp_enable_starttls", "auto")
	v.SetDefault("mail.smtp_from_address", "notifications@localhost")

	// Config file settings
	v.SetConfigName("gotodon")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath("/etc/gotodon")
	v.AddConfigPath("$HOME/.config/gotodon")

	// Environment variables
	v.SetEnvPrefix("GOTODON")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Also support Mastodon-style env vars for compatibility
	_ = v.BindEnv("database.host", "DB_HOST")
	_ = v.BindEnv("database.port", "DB_PORT")
	_ = v.BindEnv("database.user", "DB_USER")
	_ = v.BindEnv("database.password", "DB_PASS")
	_ = v.BindEnv("database.database", "DB_NAME")
	_ = v.BindEnv("redis.host", "REDIS_HOST")
	_ = v.BindEnv("redis.port", "REDIS_PORT")
	_ = v.BindEnv("redis.password", "REDIS_PASSWORD")
	_ = v.BindEnv("workers.enabled", "WORKERS_ENABLED")
	_ = v.BindEnv("workers.concurrency", "WORKERS_CONCURRENCY")
	_ = v.BindEnv("instance.local_domain", "LOCAL_DOMAIN")
	_ = v.BindEnv("instance.web_domain", "WEB_DOMAIN")
	_ = v.BindEnv("secrets.secret_key_base", "SECRET_KEY_BASE")
	_ = v.BindEnv("secrets.otp_secret", "OTP_SECRET")
	_ = v.BindEnv("secrets.vapid_private_key", "VAPID_PRIVATE_KEY")
	_ = v.BindEnv("secrets.vapid_public_key", "VAPID_PUBLIC_KEY")
	// Mail env vars (Mastodon compatible)
	_ = v.BindEnv("mail.smtp_server", "SMTP_SERVER")
	_ = v.BindEnv("mail.smtp_port", "SMTP_PORT")
	_ = v.BindEnv("mail.smtp_login", "SMTP_LOGIN")
	_ = v.BindEnv("mail.smtp_password", "SMTP_PASSWORD")
	_ = v.BindEnv("mail.smtp_auth_method", "SMTP_AUTH_METHOD")
	_ = v.BindEnv("mail.smtp_from_address", "SMTP_FROM_ADDRESS")
	_ = v.BindEnv("mail.smtp_domain", "SMTP_DOMAIN")
	_ = v.BindEnv("mail.smtp_ssl", "SMTP_SSL")
	_ = v.BindEnv("mail.smtp_enable_starttls", "SMTP_ENABLE_STARTTLS")
	_ = v.BindEnv("mail.smtp_openssl_verify_mode", "SMTP_OPENSSL_VERIFY_MODE")
	// Translation service env vars (Mastodon compatible)
	_ = v.BindEnv("translation.api_key", "DEEPL_API_KEY")
	_ = v.BindEnv("translation.plan", "DEEPL_PLAN")
	_ = v.BindEnv("translation.endpoint", "LIBRE_TRANSLATE_ENDPOINT")

	// Read config file (optional)
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("reading config: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}

	// Mastodon expresses TLS verification as SMTP_OPENSSL_VERIFY_MODE=none|peer.
	if strings.EqualFold(cfg.Mail.SMTPVerifyMode, "none") {
		cfg.Mail.SMTPNoVerify = true
	}

	return &cfg, nil
}
