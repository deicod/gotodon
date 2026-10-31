package config

import (
	"os"
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
