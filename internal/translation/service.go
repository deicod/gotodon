// Package translation provides translation service integrations.
package translation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrTranslationNotConfigured is returned when no translation service is configured
var ErrTranslationNotConfigured = errors.New("translation not configured")

// ErrTranslationFailed is returned when translation fails
var ErrTranslationFailed = errors.New("translation failed")

// Result represents a successful translation
type Result struct {
	Content                string `json:"content"`
	SpoilerText            string `json:"spoiler_text,omitempty"`
	DetectedSourceLanguage string `json:"detected_source_language"`
	Language               string `json:"language"`
	Provider               string `json:"provider"`
}

// Service is the interface for translation services
type Service interface {
	Translate(ctx context.Context, text, targetLang string) (*Result, error)
	Provider() string
}

// Config holds translation service configuration
type Config struct {
	Backend  string // none, deepl, libretranslate
	APIKey   string
	Endpoint string
	Plan     string // free or pro (for DeepL)
}

// NewService creates a translation service based on config
func NewService(cfg Config) Service {
	switch strings.ToLower(cfg.Backend) {
	case "deepl":
		return &DeepLService{
			apiKey: cfg.APIKey,
			plan:   cfg.Plan,
		}
	case "libretranslate":
		return &LibreTranslateService{
			endpoint: cfg.Endpoint,
			apiKey:   cfg.APIKey,
		}
	default:
		return &NoopService{}
	}
}

// NoopService is a no-op translation service for when translation is disabled
type NoopService struct{}

func (s *NoopService) Translate(ctx context.Context, text, targetLang string) (*Result, error) {
	return nil, ErrTranslationNotConfigured
}

func (s *NoopService) Provider() string {
	return ""
}

// DeepLService translates using DeepL API
type DeepLService struct {
	apiKey string
	plan   string
}

func (s *DeepLService) endpoint() string {
	if s.plan == "free" {
		return "https://api-free.deepl.com/v2/translate"
	}
	return "https://api.deepl.com/v2/translate"
}

func (s *DeepLService) Translate(ctx context.Context, text, targetLang string) (*Result, error) {
	if s.apiKey == "" {
		return nil, ErrTranslationNotConfigured
	}

	reqBody := fmt.Sprintf("text=%s&target_lang=%s",
		urlEncode(text), strings.ToUpper(targetLang))

	req, err := http.NewRequestWithContext(ctx, "POST", s.endpoint(),
		strings.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTranslationFailed, err)
	}

	req.Header.Set("Authorization", "DeepL-Auth-Key "+s.apiKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTranslationFailed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d", ErrTranslationFailed, resp.StatusCode)
	}

	var result struct {
		Translations []struct {
			DetectedSourceLanguage string `json:"detected_source_language"`
			Text                   string `json:"text"`
		} `json:"translations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTranslationFailed, err)
	}

	if len(result.Translations) == 0 {
		return nil, ErrTranslationFailed
	}

	return &Result{
		Content:                result.Translations[0].Text,
		DetectedSourceLanguage: strings.ToLower(result.Translations[0].DetectedSourceLanguage),
		Language:               strings.ToLower(targetLang),
		Provider:               "DeepL.com",
	}, nil
}

func (s *DeepLService) Provider() string {
	return "DeepL.com"
}

// LibreTranslateService translates using LibreTranslate API
type LibreTranslateService struct {
	endpoint string
	apiKey   string
}

func (s *LibreTranslateService) Translate(ctx context.Context, text, targetLang string) (*Result, error) {
	if s.endpoint == "" {
		return nil, ErrTranslationNotConfigured
	}

	reqData := map[string]string{
		"q":      text,
		"source": "auto",
		"target": targetLang,
	}
	if s.apiKey != "" {
		reqData["api_key"] = s.apiKey
	}

	body, _ := json.Marshal(reqData)
	req, err := http.NewRequestWithContext(ctx, "POST", s.endpoint+"/translate",
		bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTranslationFailed, err)
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTranslationFailed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d", ErrTranslationFailed, resp.StatusCode)
	}

	var result struct {
		TranslatedText   string                 `json:"translatedText"`
		DetectedLanguage map[string]interface{} `json:"detectedLanguage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTranslationFailed, err)
	}

	detectedLang := ""
	if lang, ok := result.DetectedLanguage["language"].(string); ok {
		detectedLang = lang
	}

	return &Result{
		Content:                result.TranslatedText,
		DetectedSourceLanguage: detectedLang,
		Language:               targetLang,
		Provider:               "LibreTranslate",
	}, nil
}

func (s *LibreTranslateService) Provider() string {
	return "LibreTranslate"
}

// urlEncode performs URL encoding for form data using Go's standard library
func urlEncode(s string) string {
	return url.QueryEscape(s)
}
