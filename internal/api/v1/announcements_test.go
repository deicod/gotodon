package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/streaming"
)

func mockAnnouncementRequest(method, path string, body string) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	// Set mock context
	ctx := req.Context()
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, int64(12345))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "read write")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	return req, rec
}

func TestAnnouncementsHandler_GetAll(t *testing.T) {
	h := NewAnnouncementsHandler(nil, nil)

	req, rec := mockAnnouncementRequest(http.MethodGet, "/api/v1/announcements", "")

	h.GetAll(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var announcements []Announcement
	if err := json.Unmarshal(rec.Body.Bytes(), &announcements); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
}

func TestAnnouncementsHandler_Dismiss(t *testing.T) {
	h := NewAnnouncementsHandler(nil, nil)

	// Get an announcement ID first
	req1, rec1 := mockAnnouncementRequest(http.MethodGet, "/api/v1/announcements", "")
	h.GetAll(rec1, req1)

	var announcements []Announcement
	_ = json.Unmarshal(rec1.Body.Bytes(), &announcements)

	if len(announcements) == 0 {
		t.Skip("No announcements to dismiss (DB required)")
	}

	// Dismiss it
	req2, rec2 := mockAnnouncementRequest(http.MethodPost, "/api/v1/announcements/"+announcements[0].ID+"/dismiss", "")
	req2.SetPathValue("id", announcements[0].ID)

	h.Dismiss(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec2.Code, http.StatusOK)
	}

	// Get announcements again - should be empty without with_dismissed
	req3, rec3 := mockAnnouncementRequest(http.MethodGet, "/api/v1/announcements", "")
	h.GetAll(rec3, req3)

	var afterDismiss []Announcement
	_ = json.Unmarshal(rec3.Body.Bytes(), &afterDismiss)

	if len(afterDismiss) != 0 {
		t.Error("Dismissed announcement should not appear")
	}
}

func TestAnnouncementsHandler_Reactions(t *testing.T) {
	h := NewAnnouncementsHandler(nil, nil)

	// Get announcement
	req1, rec1 := mockAnnouncementRequest(http.MethodGet, "/api/v1/announcements", "")
	h.GetAll(rec1, req1)

	var announcements []Announcement
	_ = json.Unmarshal(rec1.Body.Bytes(), &announcements)

	if len(announcements) == 0 {
		t.Skip("No announcements (DB required)")
	}

	id := announcements[0].ID

	// Add reaction
	req2, rec2 := mockAnnouncementRequest(http.MethodPut, "/api/v1/announcements/"+id+"/reactions/👍", "")
	req2.SetPathValue("id", id)
	req2.SetPathValue("name", "👍")

	h.AddReaction(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec2.Code, http.StatusOK)
	}

	// Get announcements and check reaction
	req3, rec3 := mockAnnouncementRequest(http.MethodGet, "/api/v1/announcements?with_dismissed=true", "")
	h.GetAll(rec3, req3)

	var withReactions []Announcement
	_ = json.Unmarshal(rec3.Body.Bytes(), &withReactions)

	found := false
	for _, r := range withReactions[0].Reactions {
		if r.Name == "👍" && r.Me && r.Count == 1 {
			found = true
		}
	}

	if !found {
		t.Error("Reaction should be present")
	}

	// Remove reaction
	req4, rec4 := mockAnnouncementRequest(http.MethodDelete, "/api/v1/announcements/"+id+"/reactions/👍", "")
	req4.SetPathValue("id", id)
	req4.SetPathValue("name", "👍")

	h.RemoveReaction(rec4, req4)
}

func TestAnnouncementsHandler_PublishesReactionEvent(t *testing.T) {
	queries := &stubAnnouncementQueries{}
	publisher := &stubEventPublisher{}
	h := NewAnnouncementsHandler(queries, nil)
	h.SetPublisher(publisher)

	req, rec := mockAnnouncementRequest(http.MethodPut, "/api/v1/announcements/100/reactions/👍", "")
	req.SetPathValue("id", "100")
	req.SetPathValue("name", "👍")

	h.AddReaction(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if len(publisher.events) != 1 {
		t.Fatalf("events = %d, want 1", len(publisher.events))
	}
	event := publisher.events[0]
	if event.eventType != streaming.EventAnnouncementReaction {
		t.Fatalf("eventType = %q, want %q", event.eventType, streaming.EventAnnouncementReaction)
	}
	wantStreams := []string{streaming.StreamUser}
	if !reflect.DeepEqual(event.streams, wantStreams) {
		t.Fatalf("streams = %#v, want %#v", event.streams, wantStreams)
	}
	payload, ok := event.payload.(map[string]any)
	if !ok {
		t.Fatalf("payload type = %T, want map[string]any", event.payload)
	}
	if payload["announcement_id"] != "100" {
		t.Fatalf("announcement_id = %#v, want %q", payload["announcement_id"], "100")
	}
	if payload["name"] != "👍" {
		t.Fatalf("name = %#v, want %q", payload["name"], "👍")
	}
	if payload["me"] != false {
		t.Fatalf("me = %#v, want false", payload["me"])
	}
	if payload["count"] != 1 {
		t.Fatalf("count = %#v, want 1", payload["count"])
	}
}

type stubAnnouncementQueries struct {
	announcements          []db.Announcement
	reactionsByAnnID       map[int64][]db.AnnouncementReaction
	statusesByID           map[int64]db.Status
	accountsByID           map[int64]db.Account
	localAccountsByName    map[string]db.Account
	remoteAccountsByKey    map[string]db.Account
	emojisByID             map[int64]db.CustomEmoji
	emojisByShortcode      map[string]db.CustomEmoji
	addedReaction          db.AnnouncementReaction
	addedReactionRequested bool
}

func (s *stubAnnouncementQueries) ListAnnouncements(ctx context.Context, arg db.ListAnnouncementsParams) ([]db.Announcement, error) {
	return s.announcements, nil
}

func (s *stubAnnouncementQueries) ListAnnouncementReactions(ctx context.Context, announcementID int64) ([]db.AnnouncementReaction, error) {
	return s.reactionsByAnnID[announcementID], nil
}

func (s *stubAnnouncementQueries) AddAnnouncementReaction(ctx context.Context, arg db.AddAnnouncementReactionParams) (db.AnnouncementReaction, error) {
	s.addedReactionRequested = true
	reaction := s.addedReaction
	if reaction.AnnouncementID == 0 {
		reaction.AnnouncementID = arg.AnnouncementID
	}
	if reaction.Name == "" {
		reaction.Name = arg.Name
	}
	if !reaction.AccountID.Valid {
		reaction.AccountID = arg.AccountID
	}
	if s.reactionsByAnnID == nil {
		s.reactionsByAnnID = make(map[int64][]db.AnnouncementReaction)
	}
	s.reactionsByAnnID[arg.AnnouncementID] = append(s.reactionsByAnnID[arg.AnnouncementID], reaction)
	s.addedReaction = reaction
	return reaction, nil
}

func (s *stubAnnouncementQueries) RemoveAnnouncementReaction(ctx context.Context, arg db.RemoveAnnouncementReactionParams) error {
	if s.reactionsByAnnID == nil {
		return nil
	}
	reactions := s.reactionsByAnnID[arg.AnnouncementID]
	remaining := reactions[:0]
	for _, reaction := range reactions {
		if reaction.Name != arg.Name {
			remaining = append(remaining, reaction)
			continue
		}
		if reaction.AccountID.Valid && reaction.AccountID.Int64 == arg.AccountID.Int64 {
			continue
		}
		remaining = append(remaining, reaction)
	}
	if len(remaining) == 0 {
		delete(s.reactionsByAnnID, arg.AnnouncementID)
	} else {
		s.reactionsByAnnID[arg.AnnouncementID] = remaining
	}
	return nil
}

func (s *stubAnnouncementQueries) DismissAnnouncement(ctx context.Context, arg db.DismissAnnouncementParams) error {
	return nil
}

func (s *stubAnnouncementQueries) GetStatusesByIDs(ctx context.Context, ids []int64) ([]db.Status, error) {
	statuses := make([]db.Status, 0, len(ids))
	for _, id := range ids {
		if status, ok := s.statusesByID[id]; ok {
			statuses = append(statuses, status)
		}
	}
	return statuses, nil
}

func (s *stubAnnouncementQueries) GetAccountsByIDs(ctx context.Context, ids []int64) ([]db.Account, error) {
	accounts := make([]db.Account, 0, len(ids))
	for _, id := range ids {
		if acc, ok := s.accountsByID[id]; ok {
			accounts = append(accounts, acc)
		}
	}
	return accounts, nil
}

func (s *stubAnnouncementQueries) GetAccountsByUsernames(ctx context.Context, usernames []string) ([]db.Account, error) {
	accounts := make([]db.Account, 0, len(usernames))
	for _, username := range usernames {
		if acc, ok := s.localAccountsByName[strings.ToLower(username)]; ok {
			accounts = append(accounts, acc)
		}
	}
	return accounts, nil
}

func (s *stubAnnouncementQueries) GetAccountsByUsernamesAndDomains(ctx context.Context, arg db.GetAccountsByUsernamesAndDomainsParams) ([]db.Account, error) {
	accounts := make([]db.Account, 0, len(arg.Usernames))
	for i, username := range arg.Usernames {
		if i >= len(arg.Domains) {
			continue
		}
		key := mentionKey(username, arg.Domains[i])
		if acc, ok := s.remoteAccountsByKey[key]; ok {
			accounts = append(accounts, acc)
		}
	}
	return accounts, nil
}

func (s *stubAnnouncementQueries) ListCustomEmojisByShortcodesAndDomain(ctx context.Context, arg db.ListCustomEmojisByShortcodesAndDomainParams) ([]db.CustomEmoji, error) {
	emojis := make([]db.CustomEmoji, 0, len(arg.Shortcodes))
	for _, shortcode := range arg.Shortcodes {
		if emoji, ok := s.emojisByShortcode[shortcode]; ok {
			emojis = append(emojis, emoji)
		}
	}
	return emojis, nil
}

func (s *stubAnnouncementQueries) ListCustomEmojisByIDs(ctx context.Context, ids []int64) ([]db.CustomEmoji, error) {
	emojis := make([]db.CustomEmoji, 0, len(ids))
	for _, id := range ids {
		if emoji, ok := s.emojisByID[id]; ok {
			emojis = append(emojis, emoji)
		}
	}
	return emojis, nil
}

func TestAnnouncementsHandler_GetAllDetails(t *testing.T) {
	cfg := &config.Config{
		Instance: config.InstanceConfig{
			LocalDomain: "local.test",
			WebDomain:   "web.example",
		},
	}

	publishedAt := time.Date(2024, 6, 4, 12, 0, 0, 0, time.UTC)
	createdAt := publishedAt.Add(-24 * time.Hour)

	announcement := db.Announcement{
		ID:          100,
		Text:        "Hello @alice and @bob@example.com #Go :party:",
		Published:   true,
		AllDay:      false,
		CreatedAt:   pgtype.Timestamptz{Time: createdAt, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: createdAt, Valid: true},
		PublishedAt: pgtype.Timestamptz{Time: publishedAt, Valid: true},
		StatusIds:   []int64{200},
	}

	localAccount := db.Account{
		ID:       50,
		Username: "alice",
		Url:      pgtype.Text{String: "https://web.example/@alice", Valid: true},
	}
	remoteAccount := db.Account{
		ID:       51,
		Username: "bob",
		Domain:   pgtype.Text{String: "example.com", Valid: true},
		Url:      pgtype.Text{String: "https://example.com/@bob", Valid: true},
	}

	status := db.Status{
		ID:        200,
		AccountID: localAccount.ID,
		Uri:       pgtype.Text{String: "https://local.test/users/alice/statuses/200", Valid: true},
	}

	emoji := db.CustomEmoji{
		ID:               10,
		Shortcode:        "party",
		ImageRemoteUrl:   pgtype.Text{String: "https://web.example/emojis/party.png", Valid: true},
		VisibleInPicker:  true,
		ImageFileName:    pgtype.Text{String: "party.png", Valid: true},
		ImageContentType: pgtype.Text{String: "image/png", Valid: true},
	}

	queries := &stubAnnouncementQueries{
		announcements: []db.Announcement{announcement},
		reactionsByAnnID: map[int64][]db.AnnouncementReaction{
			announcement.ID: {
				{
					Name:          "party",
					AccountID:     pgtype.Int8{Int64: 12345, Valid: true},
					CustomEmojiID: pgtype.Int8{Int64: emoji.ID, Valid: true},
				},
				{
					Name:          "party",
					AccountID:     pgtype.Int8{Int64: 999, Valid: true},
					CustomEmojiID: pgtype.Int8{Int64: emoji.ID, Valid: true},
				},
				{
					Name:      "👍",
					AccountID: pgtype.Int8{Int64: 999, Valid: true},
				},
			},
		},
		statusesByID: map[int64]db.Status{
			status.ID: status,
		},
		accountsByID: map[int64]db.Account{
			localAccount.ID: localAccount,
		},
		localAccountsByName: map[string]db.Account{
			"alice": localAccount,
		},
		remoteAccountsByKey: map[string]db.Account{
			mentionKey("bob", "example.com"): remoteAccount,
		},
		emojisByID: map[int64]db.CustomEmoji{
			emoji.ID: emoji,
		},
		emojisByShortcode: map[string]db.CustomEmoji{
			emoji.Shortcode: emoji,
		},
	}

	h := NewAnnouncementsHandler(queries, cfg)
	req, rec := mockAnnouncementRequest(http.MethodGet, "/api/v1/announcements", "")
	h.GetAll(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var announcements []Announcement
	if err := json.Unmarshal(rec.Body.Bytes(), &announcements); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if len(announcements) != 1 {
		t.Fatalf("announcements = %d, want %d", len(announcements), 1)
	}

	ann := announcements[0]
	if !ann.PublishedAt.Equal(publishedAt) {
		t.Fatalf("published_at = %s, want %s", ann.PublishedAt, publishedAt)
	}

	if len(ann.Statuses) != 1 {
		t.Fatalf("statuses = %d, want %d", len(ann.Statuses), 1)
	}
	if ann.Statuses[0].URL != "https://web.example/@alice/200" {
		t.Fatalf("status url = %q, want %q", ann.Statuses[0].URL, "https://web.example/@alice/200")
	}

	tagOK := false
	for _, tag := range ann.Tags {
		if tag.Name == "go" && tag.URL == "https://web.example/tags/go" {
			tagOK = true
		}
	}
	if !tagOK {
		t.Fatalf("tag not serialized as expected: %+v", ann.Tags)
	}

	emojiOK := false
	for _, e := range ann.Emojis {
		if e.Shortcode == "party" && e.URL == "https://web.example/emojis/party.png" {
			emojiOK = true
		}
	}
	if !emojiOK {
		t.Fatalf("emoji not serialized as expected: %+v", ann.Emojis)
	}

	mentionLocal := false
	mentionRemote := false
	for _, mention := range ann.Mentions {
		if mention.Username == "alice" && mention.Acct == "alice" && mention.URL == "https://web.example/@alice" {
			mentionLocal = true
		}
		if mention.Username == "bob" && mention.Acct == "bob@example.com" && mention.URL == "https://example.com/@bob" {
			mentionRemote = true
		}
	}
	if !mentionLocal || !mentionRemote {
		t.Fatalf("mentions not serialized as expected: %+v", ann.Mentions)
	}

	var partyReaction *AnnouncementReaction
	var thumbsReaction *AnnouncementReaction
	for i := range ann.Reactions {
		if ann.Reactions[i].Name == "party" {
			partyReaction = &ann.Reactions[i]
		}
		if ann.Reactions[i].Name == "👍" {
			thumbsReaction = &ann.Reactions[i]
		}
	}
	if partyReaction == nil || partyReaction.Count != 2 || !partyReaction.Me || partyReaction.URL != "https://web.example/emojis/party.png" {
		t.Fatalf("party reaction not serialized as expected: %+v", ann.Reactions)
	}
	if thumbsReaction == nil || thumbsReaction.Count != 1 || thumbsReaction.Me {
		t.Fatalf("thumbs reaction not serialized as expected: %+v", ann.Reactions)
	}
}
