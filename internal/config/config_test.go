package config

import (
	"os"
	"strings"
	"testing"
)

func TestDatabaseConfigDSN(t *testing.T) {
	cfg := &DatabaseConfig{
		Host:     "localhost",
		Port:     5432,
		User:     "testuser",
		Password: "testpass",
		Database: "testdb",
		SSLMode:  "disable",
	}

	expected := "postgres://testuser:testpass@localhost:5432/testdb?sslmode=disable"
	got := cfg.DSN()

	if got != expected {
		t.Errorf("DSN() = %q, want %q", got, expected)
	}
}

func TestRedisConfigAddr(t *testing.T) {
	cfg := &RedisConfig{
		Host: "redis.example.com",
		Port: 6380,
	}

	expected := "redis.example.com:6380"
	got := cfg.Addr()

	if got != expected {
		t.Errorf("Addr() = %q, want %q", got, expected)
	}
}

func TestLoadDefaults(t *testing.T) {
	// Clear any existing env vars that might interfere
	envVars := []string{
		"GOTODON_SERVER_PORT",
		"GOTODON_DATABASE_HOST",
		"DB_HOST",
		"LOCAL_DOMAIN",
	}
	for _, v := range envVars {
		_ = os.Unsetenv(v)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Check defaults
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("Server.Host = %q, want %q", cfg.Server.Host, "0.0.0.0")
	}

	if cfg.Server.Port != 3000 {
		t.Errorf("Server.Port = %d, want %d", cfg.Server.Port, 3000)
	}

	if cfg.Database.Host != "localhost" {
		t.Errorf("Database.Host = %q, want %q", cfg.Database.Host, "localhost")
	}

	if cfg.Database.Port != 5432 {
		t.Errorf("Database.Port = %d, want %d", cfg.Database.Port, 5432)
	}

	if cfg.Redis.Host != "localhost" {
		t.Errorf("Redis.Host = %q, want %q", cfg.Redis.Host, "localhost")
	}
}

func TestLoadFromEnv(t *testing.T) {
	// Set test env vars
	t.Setenv("GOTODON_SERVER_PORT", "8080")
	t.Setenv("DB_HOST", "db.example.com")
	t.Setenv("LOCAL_DOMAIN", "example.social")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Server.Port != 8080 {
		t.Errorf("Server.Port = %d, want %d", cfg.Server.Port, 8080)
	}

	if cfg.Database.Host != "db.example.com" {
		t.Errorf("Database.Host = %q, want %q", cfg.Database.Host, "db.example.com")
	}

	if cfg.Instance.LocalDomain != "example.social" {
		t.Errorf("Instance.LocalDomain = %q, want %q", cfg.Instance.LocalDomain, "example.social")
	}
}

func TestLoadFederationInboxMaxBodyBytes(t *testing.T) {
	tests := []struct {
		name    string
		env     string
		want    int64
		wantErr bool
	}{
		{name: "default", want: DefaultInboxMaxBodyBytes},
		{name: "from env", env: "4194304", want: 4 << 20},
		{name: "zero", env: "0", wantErr: true},
		{name: "negative", env: "-1", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != "" {
				t.Setenv("GOTODON_FEDERATION_INBOX_MAX_BODY_BYTES", tt.env)
			}

			cfg, err := Load()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Load() error = nil, want an error for inbox_max_body_bytes %s", tt.env)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.Federation.InboxMaxBodyBytes != tt.want {
				t.Errorf("Federation.InboxMaxBodyBytes = %d, want %d", cfg.Federation.InboxMaxBodyBytes, tt.want)
			}
		})
	}
}

func TestLoadMedia(t *testing.T) {
	tests := []struct {
		name        string
		storagePath string
		baseURL     string
		wantPath    string
		wantBaseURL string
		wantErr     bool
	}{
		{name: "defaults", wantPath: DefaultMediaStoragePath},
		{name: "storage path", storagePath: "/var/lib/gotodon/uploads", wantPath: "/var/lib/gotodon/uploads"},
		{name: "blank storage path", storagePath: "   ", wantErr: true},
		{name: "cdn", baseURL: "https://cdn.example.com/files/", wantPath: DefaultMediaStoragePath, wantBaseURL: "https://cdn.example.com/files"},
		{name: "object storage alias", baseURL: " https://files.example.com ", wantPath: DefaultMediaStoragePath, wantBaseURL: "https://files.example.com"},
		{name: "http for development", baseURL: "http://localhost:3000/files", wantPath: DefaultMediaStoragePath, wantBaseURL: "http://localhost:3000/files"},
		{name: "relative", baseURL: "/files", wantErr: true},
		{name: "no scheme", baseURL: "cdn.example.com/files", wantErr: true},
		{name: "other scheme", baseURL: "ftp://cdn.example.com/files", wantErr: true},
		{name: "no host", baseURL: "https:///files", wantErr: true},
		{name: "credentials", baseURL: "https://user:pass@cdn.example.com", wantErr: true},
		{name: "query", baseURL: "https://cdn.example.com/files?v=1", wantErr: true},
		{name: "empty query", baseURL: "https://cdn.example.com/files?", wantErr: true},
		{name: "fragment", baseURL: "https://cdn.example.com/files#top", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.storagePath != "" {
				t.Setenv("GOTODON_MEDIA_STORAGE_PATH", tt.storagePath)
			}
			if tt.baseURL != "" {
				t.Setenv("GOTODON_MEDIA_BASE_URL", tt.baseURL)
			}

			cfg, err := Load()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Load() error = nil, want an error for storage_path %q, base_url %q", tt.storagePath, tt.baseURL)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.Media.StoragePath != tt.wantPath {
				t.Errorf("Media.StoragePath = %q, want %q", cfg.Media.StoragePath, tt.wantPath)
			}
			if cfg.Media.BaseURL != tt.wantBaseURL {
				t.Errorf("Media.BaseURL = %q, want %q", cfg.Media.BaseURL, tt.wantBaseURL)
			}
		})
	}
}

func TestInstanceDomains(t *testing.T) {
	tests := []struct {
		name           string
		instance       InstanceConfig
		wantFederation string
		wantPublic     string
	}{
		{name: "nothing configured", instance: InstanceConfig{}, wantFederation: "", wantPublic: ""},
		{name: "local domain", instance: InstanceConfig{LocalDomain: " social.example "}, wantFederation: "social.example", wantPublic: "social.example"},
		{name: "web domain", instance: InstanceConfig{LocalDomain: "social.example", WebDomain: "web.social.example"}, wantFederation: "social.example", wantPublic: "web.social.example"},
		{name: "blank web domain", instance: InstanceConfig{LocalDomain: "social.example", WebDomain: "  "}, wantFederation: "social.example", wantPublic: "social.example"},
		{name: "web domain alone is no federation domain", instance: InstanceConfig{WebDomain: "web.social.example"}, wantFederation: "", wantPublic: "web.social.example"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.instance.FederationDomain(); got != tt.wantFederation {
				t.Errorf("FederationDomain() = %q, want %q", got, tt.wantFederation)
			}
			if got := tt.instance.PublicWebDomain(); got != tt.wantPublic {
				t.Errorf("PublicWebDomain() = %q, want %q", got, tt.wantPublic)
			}
			cfg := &Config{Instance: tt.instance}
			if got := cfg.FederationDomain(); got != tt.wantFederation {
				t.Errorf("Config.FederationDomain() = %q, want %q", got, tt.wantFederation)
			}
			if got := cfg.PublicWebDomain(); got != tt.wantPublic {
				t.Errorf("Config.PublicWebDomain() = %q, want %q", got, tt.wantPublic)
			}
		})
	}

	var nilCfg *Config
	if got := nilCfg.FederationDomain(); got != "" {
		t.Errorf("nil Config.FederationDomain() = %q, want empty", got)
	}
	if got := nilCfg.PublicWebDomain(); got != "" {
		t.Errorf("nil Config.PublicWebDomain() = %q, want empty", got)
	}
}

func TestInstanceConfigValidate(t *testing.T) {
	tests := []struct {
		name     string
		instance InstanceConfig
		wantErr  string
	}{
		{name: "local domain", instance: InstanceConfig{LocalDomain: "social.example"}},
		{name: "local and web domain", instance: InstanceConfig{LocalDomain: "social.example", WebDomain: "web.social.example"}},
		{name: "development port", instance: InstanceConfig{LocalDomain: "localhost:3000"}},
		{name: "IPv6 with port", instance: InstanceConfig{LocalDomain: "[::1]:3000"}},
		{name: "internationalized", instance: InstanceConfig{LocalDomain: "bücher.example"}},
		{name: "surrounding space", instance: InstanceConfig{LocalDomain: " social.example "}},
		{name: "missing", instance: InstanceConfig{}, wantErr: "instance.local_domain is required"},
		{name: "blank", instance: InstanceConfig{LocalDomain: "  "}, wantErr: "instance.local_domain is required"},
		{name: "only web domain", instance: InstanceConfig{WebDomain: "web.social.example"}, wantErr: "instance.local_domain is required"},
		{name: "scheme", instance: InstanceConfig{LocalDomain: "https://social.example"}, wantErr: "instance.local_domain"},
		{name: "path", instance: InstanceConfig{LocalDomain: "social.example/mastodon"}, wantErr: "instance.local_domain"},
		{name: "credentials", instance: InstanceConfig{LocalDomain: "user@social.example"}, wantErr: "instance.local_domain"},
		{name: "query", instance: InstanceConfig{LocalDomain: "social.example?x"}, wantErr: "instance.local_domain"},
		{name: "inner space", instance: InstanceConfig{LocalDomain: "social example"}, wantErr: "instance.local_domain"},
		{name: "empty port", instance: InstanceConfig{LocalDomain: "social.example:"}, wantErr: "instance.local_domain"},
		{name: "port out of range", instance: InstanceConfig{LocalDomain: "social.example:70000"}, wantErr: "instance.local_domain"},
		{name: "port only", instance: InstanceConfig{LocalDomain: ":3000"}, wantErr: "instance.local_domain"},
		{name: "invalid web domain", instance: InstanceConfig{LocalDomain: "social.example", WebDomain: "https://web.social.example/"}, wantErr: "instance.web_domain"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.instance.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %v, want one mentioning %q", err, tt.wantErr)
			}
		})
	}
}
