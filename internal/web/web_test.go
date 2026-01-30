package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type upperString string

func (u *upperString) UnmarshalText(text []byte) error {
	*u = upperString(strings.ToUpper(string(text)))
	return nil
}

func TestBindJSON(t *testing.T) {
	type payload struct {
		Name    string `json:"name"`
		Count   int    `json:"count"`
		Enabled bool   `json:"enabled"`
	}

	tests := []struct {
		name        string
		contentType string
	}{
		{name: "explicit content type", contentType: "application/json"},
		{name: "content type omitted", contentType: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"name":"Alice","count":3,"enabled":true}`
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}

			var got payload
			if err := Bind(req, &got); err != nil {
				t.Fatalf("Bind error: %v", err)
			}

			if got.Name != "Alice" || got.Count != 3 || got.Enabled != true {
				t.Fatalf("unexpected payload: %+v", got)
			}
		})
	}
}

func TestBindFormSimple(t *testing.T) {
	type payload struct {
		Name    string `form:"name"`
		Count   int    `form:"count"`
		Enabled bool   `form:"enabled"`
	}

	values := url.Values{}
	values.Set("name", "Bob")
	values.Set("count", "5")
	values.Set("enabled", "on")

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var got payload
	if err := Bind(req, &got); err != nil {
		t.Fatalf("Bind error: %v", err)
	}

	if got.Name != "Bob" || got.Count != 5 || got.Enabled != true {
		t.Fatalf("unexpected payload: %+v", got)
	}
}

func TestBindFormNested(t *testing.T) {
	type alerts struct {
		Follow  bool `form:"follow"`
		Mention bool `form:"mention"`
	}

	type payload struct {
		Alerts *alerts `form:"alerts"`
	}

	values := url.Values{}
	values.Set("alerts[follow]", "1")
	values.Set("alerts[mention]", "0")

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var got payload
	if err := Bind(req, &got); err != nil {
		t.Fatalf("Bind error: %v", err)
	}
	if got.Alerts == nil {
		t.Fatal("expected alerts to be set")
	}
	if got.Alerts.Follow != true || got.Alerts.Mention != false {
		t.Fatalf("unexpected alerts: %+v", got.Alerts)
	}
}

func TestBindFormSlicesPointersAndTextUnmarshal(t *testing.T) {
	type payload struct {
		Tags    []string    `form:"tags"`
		IDs     []int64     `form:"ids"`
		Choices []int       `form:"choices"`
		Counts  []uint      `form:"counts"`
		Note    *string     `form:"note"`
		Title   *string     `form:"title"`
		Limit   *int64      `form:"limit"`
		Max     *int64      `form:"max"`
		Level   upperString `form:"level"`
	}

	values := url.Values{}
	values.Add("tags[]", "alpha")
	values.Add("tags[]", "beta")
	values.Add("ids[]", "10")
	values.Add("ids[]", "20")
	values.Add("choices[]", "1")
	values.Add("choices[]", "2")
	values.Add("counts[]", "3")
	values.Add("counts[]", "4")
	values.Set("note", "")
	values.Set("title", "hello")
	values.Set("limit", "")
	values.Set("max", "42")
	values.Set("level", "admin")

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var got payload
	if err := Bind(req, &got); err != nil {
		t.Fatalf("Bind error: %v", err)
	}

	if len(got.Tags) != 2 || got.Tags[0] != "alpha" || got.Tags[1] != "beta" {
		t.Fatalf("unexpected tags: %+v", got.Tags)
	}
	if len(got.IDs) != 2 || got.IDs[0] != 10 || got.IDs[1] != 20 {
		t.Fatalf("unexpected ids: %+v", got.IDs)
	}
	if len(got.Choices) != 2 || got.Choices[0] != 1 || got.Choices[1] != 2 {
		t.Fatalf("unexpected choices: %+v", got.Choices)
	}
	if len(got.Counts) != 2 || got.Counts[0] != 3 || got.Counts[1] != 4 {
		t.Fatalf("unexpected counts: %+v", got.Counts)
	}
	if got.Note != nil {
		t.Fatalf("expected note to be nil, got %v", *got.Note)
	}
	if got.Title == nil || *got.Title != "hello" {
		t.Fatalf("unexpected title: %v", got.Title)
	}
	if got.Limit != nil {
		t.Fatalf("expected limit to be nil, got %v", *got.Limit)
	}
	if got.Max == nil || *got.Max != 42 {
		t.Fatalf("unexpected max: %v", got.Max)
	}
	if got.Level != "ADMIN" {
		t.Fatalf("unexpected level: %q", got.Level)
	}
}

func TestBindFormInvalidBool(t *testing.T) {
	type payload struct {
		Enabled bool `form:"enabled"`
	}

	values := url.Values{}
	values.Set("enabled", "maybe")

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var got payload
	if err := Bind(req, &got); err == nil {
		t.Fatal("expected error for invalid boolean")
	}
}
