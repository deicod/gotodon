package nodeinfo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

type stubQueries struct {
	totalUsers     int64
	activeMonth    int64
	activeHalfyear int64
	localPosts     int64
	settings       []db.GetSettingsByVarsRow

	totalUsersErr     error
	activeMonthErr    error
	activeHalfyearErr error
	localPostsErr     error
	settingsErr       error
}

func (s *stubQueries) CountInstanceUsers(_ context.Context) (int64, error) {
	return s.totalUsers, s.totalUsersErr
}

func (s *stubQueries) CountActiveUsers(_ context.Context) (int64, error) {
	return s.activeMonth, s.activeMonthErr
}

func (s *stubQueries) CountNodeInfoActiveUsersMonth(_ context.Context) (int64, error) {
	return s.activeMonth, s.activeMonthErr
}

func (s *stubQueries) CountNodeInfoActiveUsersHalfyear(_ context.Context) (int64, error) {
	return s.activeHalfyear, s.activeHalfyearErr
}

func (s *stubQueries) CountInstanceStatuses(_ context.Context) (int64, error) {
	return s.localPosts, s.localPostsErr
}

func (s *stubQueries) GetSettingsByVars(_ context.Context, _ []string) ([]db.GetSettingsByVarsRow, error) {
	return s.settings, s.settingsErr
}

func TestHandler_Discovery(t *testing.T) {
	h := NewHandler(nil, &config.Config{
		Instance: config.InstanceConfig{
			WebDomain: "web.example",
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/.well-known/nodeinfo", nil)
	rec := httptest.NewRecorder()

	h.Discovery(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	links, ok := body["links"].([]any)
	if !ok || len(links) != 1 {
		t.Fatalf("links shape = %#v, want single link", body["links"])
	}
	link, ok := links[0].(map[string]any)
	if !ok {
		t.Fatalf("link shape = %#v", links[0])
	}
	if got := link["rel"]; got != nodeInfoSchemaRel {
		t.Fatalf("rel = %v, want %q", got, nodeInfoSchemaRel)
	}
	if got := link["href"]; got != "https://web.example/nodeinfo/2.0" {
		t.Fatalf("href = %v, want %q", got, "https://web.example/nodeinfo/2.0")
	}
}

func TestHandler_Show_MastodonShapeWithRealData(t *testing.T) {
	queries := &stubQueries{
		totalUsers:     11,
		activeMonth:    7,
		activeHalfyear: 9,
		localPosts:     1234,
		settings: []db.GetSettingsByVarsRow{
			{
				Var:   settingSiteTitle,
				Value: pgtype.Text{String: "My Gotodon", Valid: true},
			},
			{
				Var:   settingSiteDescription,
				Value: pgtype.Text{String: "My short description", Valid: true},
			},
		},
	}
	h := NewHandler(queries, &config.Config{
		Instance: config.InstanceConfig{
			RegistrationsMode: "open",
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/nodeinfo/2.0", nil)
	rec := httptest.NewRecorder()

	h.Show(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if got := body["version"]; got != "2.0" {
		t.Fatalf("version = %v, want %q", got, "2.0")
	}

	software, ok := body["software"].(map[string]any)
	if !ok {
		t.Fatalf("software shape = %#v", body["software"])
	}
	if software["name"] != "mastodon" {
		t.Fatalf("software.name = %v, want mastodon", software["name"])
	}
	if software["version"] != "4.5.3" {
		t.Fatalf("software.version = %v, want 4.5.3", software["version"])
	}

	if _, hasSnake := body["open_registrations"]; hasSnake {
		t.Fatalf("unexpected snake_case field open_registrations present")
	}
	if got := body["openRegistrations"]; got != true {
		t.Fatalf("openRegistrations = %v, want true", got)
	}

	usage, ok := body["usage"].(map[string]any)
	if !ok {
		t.Fatalf("usage shape = %#v", body["usage"])
	}
	users, ok := usage["users"].(map[string]any)
	if !ok {
		t.Fatalf("usage.users shape = %#v", usage["users"])
	}
	if users["total"] != float64(11) {
		t.Fatalf("usage.users.total = %v, want 11", users["total"])
	}
	if users["activeMonth"] != float64(7) {
		t.Fatalf("usage.users.activeMonth = %v, want 7", users["activeMonth"])
	}
	if users["activeHalfyear"] != float64(9) {
		t.Fatalf("usage.users.activeHalfyear = %v, want 9", users["activeHalfyear"])
	}
	if usage["localPosts"] != float64(1234) {
		t.Fatalf("usage.localPosts = %v, want 1234", usage["localPosts"])
	}

	metadata, ok := body["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata shape = %#v", body["metadata"])
	}
	if metadata["nodeName"] != "My Gotodon" {
		t.Fatalf("metadata.nodeName = %v, want My Gotodon", metadata["nodeName"])
	}
	if metadata["nodeDescription"] != "My short description" {
		t.Fatalf("metadata.nodeDescription = %v, want My short description", metadata["nodeDescription"])
	}
}

func TestHandler_Show_RegistrationSemanticsAndFallbacks(t *testing.T) {
	t.Run("settings override config for open_registrations", func(t *testing.T) {
		h := NewHandler(&stubQueries{
			settings: []db.GetSettingsByVarsRow{
				{
					Var:   settingRegistrationsEnabled,
					Value: pgtype.Text{String: "false", Valid: true},
				},
			},
		}, &config.Config{
			Instance: config.InstanceConfig{
				RegistrationsMode: "open",
			},
		})
		req := httptest.NewRequest(http.MethodGet, "/nodeinfo/2.0", nil)
		rec := httptest.NewRecorder()

		h.Show(rec, req)

		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if got := body["openRegistrations"]; got != false {
			t.Fatalf("openRegistrations = %v, want false from settings override", got)
		}
	})

	t.Run("single-user mode forces closed registrations", func(t *testing.T) {
		h := NewHandler(&stubQueries{}, &config.Config{
			Instance: config.InstanceConfig{
				RegistrationsMode: "open",
				SingleUserMode:    true,
			},
		})
		req := httptest.NewRequest(http.MethodGet, "/nodeinfo/2.0", nil)
		rec := httptest.NewRecorder()

		h.Show(rec, req)

		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if got := body["openRegistrations"]; got != false {
			t.Fatalf("openRegistrations = %v, want false", got)
		}
	})

	t.Run("default metadata and zeroed usage when data unavailable", func(t *testing.T) {
		h := NewHandler(&stubQueries{
			totalUsersErr:     context.DeadlineExceeded,
			activeMonthErr:    context.DeadlineExceeded,
			activeHalfyearErr: context.DeadlineExceeded,
			localPostsErr:     context.DeadlineExceeded,
			settingsErr:       context.DeadlineExceeded,
		}, &config.Config{
			Instance: config.InstanceConfig{
				RegistrationsMode: "none",
			},
		})

		req := httptest.NewRequest(http.MethodGet, "/nodeinfo/2.0", nil)
		rec := httptest.NewRecorder()
		h.Show(rec, req)

		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}

		usage := body["usage"].(map[string]any)
		users := usage["users"].(map[string]any)
		if users["total"] != float64(0) || users["activeMonth"] != float64(0) || users["activeHalfyear"] != float64(0) {
			t.Fatalf("unexpected usage.users fallback: %#v", users)
		}
		if usage["localPosts"] != float64(0) {
			t.Fatalf("localPosts fallback = %v, want 0", usage["localPosts"])
		}
		metadata := body["metadata"].(map[string]any)
		if metadata["nodeName"] != defaultNodeInfoMetadataName {
			t.Fatalf("metadata.nodeName = %v, want %q", metadata["nodeName"], defaultNodeInfoMetadataName)
		}
		if metadata["nodeDescription"] != defaultNodeInfoMetadataShortDesc {
			t.Fatalf("metadata.nodeDescription = %v, want %q", metadata["nodeDescription"], defaultNodeInfoMetadataShortDesc)
		}
		if got := body["openRegistrations"]; got != false {
			t.Fatalf("openRegistrations = %v, want false", got)
		}
	})

	t.Run("invalid settings value falls back to config mode", func(t *testing.T) {
		h := NewHandler(&stubQueries{
			settings: []db.GetSettingsByVarsRow{
				{
					Var:   settingRegistrationsEnabled,
					Value: pgtype.Text{String: "not-a-bool", Valid: true},
				},
			},
		}, &config.Config{
			Instance: config.InstanceConfig{
				RegistrationsMode: "none",
			},
		})
		req := httptest.NewRequest(http.MethodGet, "/nodeinfo/2.0", nil)
		rec := httptest.NewRecorder()

		h.Show(rec, req)

		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if got := body["openRegistrations"]; got != false {
			t.Fatalf("openRegistrations = %v, want false from config fallback", got)
		}
	})
}

func TestHandler_Show_CachesResponse(t *testing.T) {
	queries := &stubQueries{
		totalUsers:     10,
		activeMonth:    3,
		activeHalfyear: 5,
		localPosts:     99,
	}
	h := NewHandler(queries, &config.Config{
		Instance: config.InstanceConfig{
			RegistrationsMode: "open",
		},
	})
	h.cacheTTL = 30 * time.Minute

	req1 := httptest.NewRequest(http.MethodGet, "/nodeinfo/2.0", nil)
	rec1 := httptest.NewRecorder()
	h.Show(rec1, req1)

	var body1 map[string]any
	if err := json.Unmarshal(rec1.Body.Bytes(), &body1); err != nil {
		t.Fatalf("unmarshal first response: %v", err)
	}
	usage1 := body1["usage"].(map[string]any)
	users1 := usage1["users"].(map[string]any)
	if users1["total"] != float64(10) {
		t.Fatalf("first usage.users.total = %v, want 10", users1["total"])
	}

	// Mutate the backing query values; cached response should still be served.
	queries.totalUsers = 42
	queries.activeMonth = 12
	queries.activeHalfyear = 18
	queries.localPosts = 777

	req2 := httptest.NewRequest(http.MethodGet, "/nodeinfo/2.0", nil)
	rec2 := httptest.NewRecorder()
	h.Show(rec2, req2)

	if got := rec2.Header().Get("Cache-Control"); got != nodeInfoPublicCacheControl(1800) {
		t.Fatalf("Cache-Control = %q, want %q", got, nodeInfoPublicCacheControl(1800))
	}

	var body2 map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &body2); err != nil {
		t.Fatalf("unmarshal second response: %v", err)
	}
	usage2 := body2["usage"].(map[string]any)
	users2 := usage2["users"].(map[string]any)
	if users2["total"] != float64(10) {
		t.Fatalf("second usage.users.total = %v, want cached 10", users2["total"])
	}
	if usage2["localPosts"] != float64(99) {
		t.Fatalf("second usage.localPosts = %v, want cached 99", usage2["localPosts"])
	}
}

func TestHandler_Show_DoesNotCacheDegradedFallback(t *testing.T) {
	queries := &stubQueries{
		totalUsersErr:     context.DeadlineExceeded,
		activeMonthErr:    context.DeadlineExceeded,
		activeHalfyearErr: context.DeadlineExceeded,
		localPostsErr:     context.DeadlineExceeded,
		settingsErr:       context.DeadlineExceeded,
	}
	h := NewHandler(queries, &config.Config{
		Instance: config.InstanceConfig{
			RegistrationsMode: "open",
		},
	})
	h.cacheTTL = 30 * time.Minute

	req1 := httptest.NewRequest(http.MethodGet, "/nodeinfo/2.0", nil)
	rec1 := httptest.NewRecorder()
	h.Show(rec1, req1)
	if got := rec1.Header().Get("Cache-Control"); got != nodeInfoNoStoreCacheControl {
		t.Fatalf("first Cache-Control = %q, want %q", got, nodeInfoNoStoreCacheControl)
	}

	var body1 map[string]any
	if err := json.Unmarshal(rec1.Body.Bytes(), &body1); err != nil {
		t.Fatalf("unmarshal first response: %v", err)
	}
	usage1 := body1["usage"].(map[string]any)
	users1 := usage1["users"].(map[string]any)
	if users1["total"] != float64(0) {
		t.Fatalf("first usage.users.total = %v, want 0 fallback", users1["total"])
	}

	// Recover data source: if degraded response was cached, this second response would still be zero.
	queries.totalUsersErr = nil
	queries.activeMonthErr = nil
	queries.activeHalfyearErr = nil
	queries.localPostsErr = nil
	queries.settingsErr = nil
	queries.totalUsers = 21
	queries.activeMonth = 8
	queries.activeHalfyear = 13
	queries.localPosts = 456

	req2 := httptest.NewRequest(http.MethodGet, "/nodeinfo/2.0", nil)
	rec2 := httptest.NewRecorder()
	h.Show(rec2, req2)
	if got := rec2.Header().Get("Cache-Control"); got != nodeInfoPublicCacheControl(1800) {
		t.Fatalf("second Cache-Control = %q, want %q", got, nodeInfoPublicCacheControl(1800))
	}

	var body2 map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &body2); err != nil {
		t.Fatalf("unmarshal second response: %v", err)
	}
	usage2 := body2["usage"].(map[string]any)
	users2 := usage2["users"].(map[string]any)
	if users2["total"] != float64(21) {
		t.Fatalf("second usage.users.total = %v, want 21 (degraded response must not be cached)", users2["total"])
	}
	if usage2["localPosts"] != float64(456) {
		t.Fatalf("second usage.localPosts = %v, want 456", usage2["localPosts"])
	}
}

func TestHandler_Show_CacheHitUsesRemainingMaxAge(t *testing.T) {
	queries := &stubQueries{
		totalUsers:     5,
		activeMonth:    2,
		activeHalfyear: 4,
		localPosts:     50,
	}
	h := NewHandler(queries, &config.Config{
		Instance: config.InstanceConfig{
			RegistrationsMode: "open",
		},
	})
	h.cacheTTL = 30 * time.Minute

	// Prime cache.
	req1 := httptest.NewRequest(http.MethodGet, "/nodeinfo/2.0", nil)
	rec1 := httptest.NewRecorder()
	h.Show(rec1, req1)

	// Force cache to have ~90 seconds left to verify dynamic max-age on hit.
	h.cache.mu.Lock()
	h.cache.expiresAt = time.Now().Add(90 * time.Second)
	h.cache.mu.Unlock()

	req2 := httptest.NewRequest(http.MethodGet, "/nodeinfo/2.0", nil)
	rec2 := httptest.NewRecorder()
	h.Show(rec2, req2)

	got := rec2.Header().Get("Cache-Control")
	want := nodeInfoPublicCacheControl(90)
	if got != want {
		t.Fatalf("cache-hit Cache-Control = %q, want %q", got, want)
	}
}
