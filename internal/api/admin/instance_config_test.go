package admin

import (
	"testing"

	"github.com/deicod/gotodon/internal/config"
)

func TestDefaultInstanceConfig_DomainAndRegistrations(t *testing.T) {
	cfg := &config.Config{
		Instance: config.InstanceConfig{
			LocalDomain:       "local.example",
			WebDomain:         "web.example",
			RegistrationsMode: "approved",
		},
	}

	instance := defaultInstanceConfig(cfg)
	if instance.Domain != "web.example" {
		t.Fatalf("Domain = %q, want %q", instance.Domain, "web.example")
	}
	if !instance.Registrations {
		t.Fatalf("Registrations = false, want true")
	}
	if !instance.ApprovalRequired {
		t.Fatalf("ApprovalRequired = false, want true")
	}
}

func TestApplyInstanceSetting(t *testing.T) {
	instance := defaultInstanceConfig(&config.Config{
		Instance: config.InstanceConfig{LocalDomain: "local.example"},
	})

	applyInstanceSetting(&instance, settingSiteTitle, "New Title")
	if instance.Title != "New Title" {
		t.Fatalf("Title = %q, want %q", instance.Title, "New Title")
	}

	applyInstanceSetting(&instance, settingSiteLanguages, `["en","fr"]`)
	if len(instance.Languages) != 2 || instance.Languages[1] != "fr" {
		t.Fatalf("Languages = %#v, want [\"en\",\"fr\"]", instance.Languages)
	}

	applyInstanceSetting(&instance, settingRegistrationsEnabled, "false")
	if instance.Registrations {
		t.Fatalf("Registrations = true, want false")
	}

	applyInstanceSetting(&instance, settingMaxTootChars, "600")
	if instance.MaxTootChars != 600 {
		t.Fatalf("MaxTootChars = %d, want 600", instance.MaxTootChars)
	}

	original := instance.MaxPollOptions
	applyInstanceSetting(&instance, settingMaxPollOptions, "not-a-number")
	if instance.MaxPollOptions != original {
		t.Fatalf("MaxPollOptions = %d, want %d", instance.MaxPollOptions, original)
	}
}
