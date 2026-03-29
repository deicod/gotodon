package hostmeta

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/deicod/gotodon/internal/config"
)

func TestHandler_Show_DefaultXML(t *testing.T) {
	h := NewHandler(&config.Config{
		Instance: config.InstanceConfig{
			LocalDomain: "social.example",
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/.well-known/host-meta", nil)
	rec := httptest.NewRecorder()

	h.Show(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != hostMetaXMLType {
		t.Fatalf("content-type = %q, want %q", got, hostMetaXMLType)
	}
	assertCommonHeaders(t, rec.Header())

	body := rec.Body.String()
	if !strings.Contains(body, `<Link rel="lrdd" template="https://social.example/.well-known/webfinger?resource={uri}"/>`) {
		t.Fatalf("body missing expected webfinger template: %q", body)
	}
}

func TestHandler_Show_JSONWhenAcceptJSON(t *testing.T) {
	h := NewHandler(&config.Config{
		Instance: config.InstanceConfig{
			LocalDomain: "social.example",
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/.well-known/host-meta", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()

	h.Show(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != hostMetaJSONType {
		t.Fatalf("content-type = %q, want %q", got, hostMetaJSONType)
	}
	assertCommonHeaders(t, rec.Header())

	var body struct {
		Links []struct {
			Rel      string `json:"rel"`
			Template string `json:"template"`
		} `json:"links"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Links) != 1 {
		t.Fatalf("links len = %d, want 1", len(body.Links))
	}
	if body.Links[0].Rel != "lrdd" {
		t.Fatalf("links[0].rel = %q, want %q", body.Links[0].Rel, "lrdd")
	}
	if body.Links[0].Template != "https://social.example/.well-known/webfinger?resource={uri}" {
		t.Fatalf("links[0].template = %q", body.Links[0].Template)
	}
}

func TestHandler_Show_TemplateUsesWebDomain(t *testing.T) {
	h := NewHandler(&config.Config{
		Instance: config.InstanceConfig{
			LocalDomain: "social.example",
			WebDomain:   "web.example",
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/.well-known/host-meta", nil)
	req.Header.Set("Accept", "application/json, application/xrd+xml")
	rec := httptest.NewRecorder()

	h.Show(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	links, ok := body["links"].([]any)
	if !ok || len(links) != 1 {
		t.Fatalf("links shape = %#v", body["links"])
	}
	link, ok := links[0].(map[string]any)
	if !ok {
		t.Fatalf("link shape = %#v", links[0])
	}
	if got := link["template"]; got != "https://web.example/.well-known/webfinger?resource={uri}" {
		t.Fatalf("template = %v, want web domain template", got)
	}
}

func TestHandler_Show_HostMetaJSONRouteReturnsJSONWithoutAccept(t *testing.T) {
	h := NewHandler(&config.Config{
		Instance: config.InstanceConfig{
			LocalDomain: "social.example",
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/.well-known/host-meta.json", nil)
	rec := httptest.NewRecorder()

	h.Show(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != hostMetaJSONType {
		t.Fatalf("content-type = %q, want %q", got, hostMetaJSONType)
	}
	assertCommonHeaders(t, rec.Header())

	var body struct {
		Links []struct {
			Rel      string `json:"rel"`
			Template string `json:"template"`
		} `json:"links"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Links) != 1 {
		t.Fatalf("links len = %d, want 1", len(body.Links))
	}
	if body.Links[0].Rel != "lrdd" {
		t.Fatalf("links[0].rel = %q, want %q", body.Links[0].Rel, "lrdd")
	}
	if body.Links[0].Template != "https://social.example/.well-known/webfinger?resource={uri}" {
		t.Fatalf("links[0].template = %q", body.Links[0].Template)
	}
}

func TestHandler_Show_HostMetaJSONRouteMatchesAcceptJSONVariant(t *testing.T) {
	h := NewHandler(&config.Config{
		Instance: config.InstanceConfig{
			LocalDomain: "social.example",
		},
	})

	acceptReq := httptest.NewRequest(http.MethodGet, "/.well-known/host-meta", nil)
	acceptReq.Header.Set("Accept", "application/json")
	acceptRec := httptest.NewRecorder()
	h.Show(acceptRec, acceptReq)

	jsonRouteReq := httptest.NewRequest(http.MethodGet, "/.well-known/host-meta.json", nil)
	jsonRouteRec := httptest.NewRecorder()
	h.Show(jsonRouteRec, jsonRouteReq)

	if acceptRec.Code != http.StatusOK || jsonRouteRec.Code != http.StatusOK {
		t.Fatalf("status mismatch accept=%d json-route=%d", acceptRec.Code, jsonRouteRec.Code)
	}
	if !reflect.DeepEqual(acceptRec.Body.Bytes(), jsonRouteRec.Body.Bytes()) {
		t.Fatalf("json bodies differ: accept=%q json-route=%q", acceptRec.Body.String(), jsonRouteRec.Body.String())
	}
}

func assertCommonHeaders(t *testing.T, h http.Header) {
	t.Helper()
	if got := h.Get("Cache-Control"); got != hostMetaCacheControl {
		t.Fatalf("cache-control = %q, want %q", got, hostMetaCacheControl)
	}
	varyValues := h.Values("Vary")
	if len(varyValues) == 0 {
		t.Fatalf("vary header missing")
	}
	all := strings.ToLower(strings.Join(varyValues, ","))
	if !strings.Contains(all, "accept") {
		t.Fatalf("vary = %q, missing Accept", strings.Join(varyValues, ","))
	}
	if !strings.Contains(all, "origin") {
		t.Fatalf("vary = %q, missing Origin", strings.Join(varyValues, ","))
	}
}
