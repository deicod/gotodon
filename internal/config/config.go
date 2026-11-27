// Package config provides configuration management for Gotodon.
package config

import (
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"

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
	Federation  FederationConfig  `mapstructure:"federation"`
	Media       MediaConfig       `mapstructure:"media"`
}

// ServerConfig holds HTTP server configuration.
type ServerConfig struct {
	Host          string `mapstructure:"host"`
	Port          int    `mapstructure:"port"`
	StreamingPort int    `mapstructure:"streaming_port"`
	// TrustedProxies lists the reverse proxies whose X-Forwarded-For and
	// X-Real-IP headers are believed, as IP addresses or CIDR ranges
	// separated by commas or white space, like Mastodon's TRUSTED_PROXY_IP.
	// Empty selects DefaultTrustedProxies. A list replaces the defaults.
	TrustedProxies string `mapstructure:"trusted_proxies"`
}

// DefaultTrustedProxies are the proxies trusted when
// ServerConfig.TrustedProxies is empty: loopback, private and link-local
// networks, the defaults Rails applies for Mastodon.
var DefaultTrustedProxies = []string{
	"127.0.0.0/8",
	"::1/128",
	"fc00::/7",
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"169.254.0.0/16",
	"fe80::/10",
}

// TrustedProxyPrefixes parses TrustedProxies into networks; a bare address
// is a single-address network. Empty selects DefaultTrustedProxies.
func (c ServerConfig) TrustedProxyPrefixes() ([]netip.Prefix, error) {
	entries := strings.FieldsFunc(c.TrustedProxies, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
	if len(entries) == 0 {
		entries = DefaultTrustedProxies
	}
	prefixes := make([]netip.Prefix, 0, len(entries))
	for _, entry := range entries {
		prefix, err := parseTrustedProxy(entry)
		if err != nil {
			return nil, err
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

func parseTrustedProxy(entry string) (netip.Prefix, error) {
	invalid := fmt.Errorf("server.trusted_proxies entry %q must be an IP address or CIDR range", entry)
	if strings.Contains(entry, "/") {
		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			return netip.Prefix{}, invalid
		}
		if prefix.Addr().Is4In6() {
			// 4in6 client addresses are unmapped before matching.
			if prefix.Bits() < 96 {
				return netip.Prefix{}, invalid
			}
			prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
		}
		return prefix.Masked(), nil
	}
	addr, err := netip.ParseAddr(entry)
	if err != nil || addr.Zone() != "" {
		return netip.Prefix{}, invalid
	}
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()), nil
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

// FederationDomain returns the domain of the instance's accounts and
// ActivityPub IDs: local_domain. It is empty only when local_domain is not
// configured, which Validate rejects and serve refuses to start with.
func (c InstanceConfig) FederationDomain() string {
	return strings.TrimSpace(c.LocalDomain)
}

// PublicWebDomain returns the browser-facing domain gotodon is reached at:
// web_domain, otherwise local_domain. It is empty only when neither is
// configured.
func (c InstanceConfig) PublicWebDomain() string {
	if domain := strings.TrimSpace(c.WebDomain); domain != "" {
		return domain
	}
	return c.FederationDomain()
}

// Validate checks the domains: local_domain is required, and both must be a
// host name, optionally with a port, without scheme, path or credentials.
func (c InstanceConfig) Validate() error {
	if strings.TrimSpace(c.LocalDomain) == "" {
		return fmt.Errorf("instance.local_domain is required: set LOCAL_DOMAIN or instance.local_domain to the domain of the instance's accounts")
	}
	if err := validateDomain("instance.local_domain", c.LocalDomain); err != nil {
		return err
	}
	if strings.TrimSpace(c.WebDomain) != "" {
		if err := validateDomain("instance.web_domain", c.WebDomain); err != nil {
			return err
		}
	}
	return nil
}

func validateDomain(key, raw string) error {
	value := strings.TrimSpace(raw)
	invalid := fmt.Errorf("%s %q must be a host name such as social.example, optionally with a port, without scheme or path", key, raw)
	if strings.ContainsAny(value, "/\\?#@ \t") {
		return invalid
	}
	parsed, err := url.Parse("https://" + value)
	if err != nil || parsed.Host != value || parsed.Hostname() == "" {
		return invalid
	}
	if port := parsed.Port(); port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return invalid
		}
	} else if strings.HasSuffix(value, ":") {
		return invalid
	}
	return nil
}

// FederationDomain returns c.Instance.FederationDomain(), or "" for a nil
// config.
func (c *Config) FederationDomain() string {
	if c == nil {
		return ""
	}
	return c.Instance.FederationDomain()
}

// PublicWebDomain returns c.Instance.PublicWebDomain(), or "" for a nil
// config.
func (c *Config) PublicWebDomain() string {
	if c == nil {
		return ""
	}
	return c.Instance.PublicWebDomain()
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

// DefaultInboxMaxBodyBytes is the default for
// FederationConfig.InboxMaxBodyBytes: 1 MiB, the limit Mastodon applies to the
// ActivityPub objects it fetches.
const DefaultInboxMaxBodyBytes int64 = 1 << 20

// FederationConfig holds ActivityPub federation configuration.
type FederationConfig struct {
	// InboxMaxBodyBytes is the largest request body accepted by the inboxes.
	// Larger deliveries are refused with 413 before they are parsed.
	InboxMaxBodyBytes int64 `mapstructure:"inbox_max_body_bytes"`
}

// DefaultMediaStoragePath is the default for MediaConfig.StoragePath.
const DefaultMediaStoragePath = "./uploads"

// MediaConfig holds where uploaded files are stored and the URL they are
// served at.
type MediaConfig struct {
	// StoragePath is the directory uploads are written to. gotodon serves it
	// at /files/.
	StoragePath string `mapstructure:"storage_path"`
	// BaseURL is the public URL of the storage root, for a CDN or an object
	// storage alias in front of it, like Mastodon's CDN_HOST and
	// S3_ALIAS_HOST. Empty means gotodon's own /files route on the web
	// domain. Load rejects anything but an absolute http or https URL without
	// query or fragment, and strips trailing slashes.
	BaseURL string `mapstructure:"base_url"`
}

// NormalizeMediaBaseURL validates a media base URL and returns it without
// surrounding space or trailing slashes. An empty value stays empty.
func NormalizeMediaBaseURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("media.base_url %q is not a URL: %w", raw, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("media.base_url %q must use http or https", raw)
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return "", fmt.Errorf("media.base_url %q has no host", raw)
	}
	if parsed.User != nil {
		return "", fmt.Errorf("media.base_url %q must not contain credentials", raw)
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(value, "#") {
		return "", fmt.Errorf("media.base_url %q must not contain a query or fragment", raw)
	}
	return strings.TrimRight(value, "/"), nil
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
	v.SetDefault("federation.inbox_max_body_bytes", DefaultInboxMaxBodyBytes)
	v.SetDefault("media.storage_path", DefaultMediaStoragePath)
	// Registered so GOTODON_MEDIA_BASE_URL reaches Unmarshal.
	v.SetDefault("media.base_url", "")

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
	_ = v.BindEnv("server.trusted_proxies", "GOTODON_SERVER_TRUSTED_PROXIES", "TRUSTED_PROXY_IP")
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

	if _, err := cfg.Server.TrustedProxyPrefixes(); err != nil {
		return nil, err
	}

	if cfg.Federation.InboxMaxBodyBytes <= 0 {
		return nil, fmt.Errorf("federation.inbox_max_body_bytes must be positive, got %d", cfg.Federation.InboxMaxBodyBytes)
	}

	cfg.Media.StoragePath = strings.TrimSpace(cfg.Media.StoragePath)
	if cfg.Media.StoragePath == "" {
		return nil, fmt.Errorf("media.storage_path must not be empty")
	}
	baseURL, err := NormalizeMediaBaseURL(cfg.Media.BaseURL)
	if err != nil {
		return nil, err
	}
	cfg.Media.BaseURL = baseURL

	return &cfg, nil
}
