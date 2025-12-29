package v1

import (
	"errors"
	"net/http"
	"testing"
)

func TestParseScheduledStatusParamsStatusField(t *testing.T) {
	req, err := parseScheduledStatusParams([]byte(`{
		"status":"hello",
		"media_ids":["10","20"],
		"in_reply_to_id":"30",
		"sensitive":true,
		"spoiler_text":"cw",
		"visibility":"unlisted",
		"language":"en"
	}`))
	if err != nil {
		t.Fatalf("parseScheduledStatusParams error = %v", err)
	}
	if req.Status != "hello" {
		t.Fatalf("Status = %q, want %q", req.Status, "hello")
	}
	if len(req.MediaIDs) != 2 || req.MediaIDs[0] != "10" || req.MediaIDs[1] != "20" {
		t.Fatalf("MediaIDs = %#v, want [10 20]", req.MediaIDs)
	}
	if req.InReplyToID == nil || *req.InReplyToID != "30" {
		t.Fatalf("InReplyToID = %#v, want 30", req.InReplyToID)
	}
	if !req.Sensitive {
		t.Fatalf("Sensitive = false, want true")
	}
	if req.SpoilerText != "cw" {
		t.Fatalf("SpoilerText = %q, want %q", req.SpoilerText, "cw")
	}
	if req.Visibility != "unlisted" {
		t.Fatalf("Visibility = %q, want %q", req.Visibility, "unlisted")
	}
	if req.Language == nil || *req.Language != "en" {
		t.Fatalf("Language = %#v, want en", req.Language)
	}
}

func TestParseScheduledStatusParamsTextFallback(t *testing.T) {
	req, err := parseScheduledStatusParams([]byte(`{
		"text":"hello",
		"media_ids":[1,2],
		"in_reply_to_id":4
	}`))
	if err != nil {
		t.Fatalf("parseScheduledStatusParams error = %v", err)
	}
	if req.Status != "hello" {
		t.Fatalf("Status = %q, want %q", req.Status, "hello")
	}
	if len(req.MediaIDs) != 2 || req.MediaIDs[0] != "1" || req.MediaIDs[1] != "2" {
		t.Fatalf("MediaIDs = %#v, want [1 2]", req.MediaIDs)
	}
	if req.InReplyToID == nil || *req.InReplyToID != "4" {
		t.Fatalf("InReplyToID = %#v, want 4", req.InReplyToID)
	}
}

func TestParseScheduledStatusParamsInvalid(t *testing.T) {
	_, err := parseScheduledStatusParams([]byte(`{"status":`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	var createErr statusCreateError
	if !errors.As(err, &createErr) {
		t.Fatalf("expected statusCreateError, got %T", err)
	}
	if createErr.Status != http.StatusBadRequest {
		t.Fatalf("Status = %d, want %d", createErr.Status, http.StatusBadRequest)
	}
}

func TestParseScheduledStatusParamsEmpty(t *testing.T) {
	_, err := parseScheduledStatusParams([]byte(""))
	if err == nil {
		t.Fatal("expected error for empty params")
	}
}
