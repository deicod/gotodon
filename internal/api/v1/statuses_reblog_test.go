package v1

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

type statusSerializerStub struct {
	accountsByID     map[int64]db.Account
	statusesByID     map[int64]db.Status
	mediaByStatusID  map[int64][]db.MediaAttachment
	quotesByStatusID map[int64]db.Quote
}

func (s *statusSerializerStub) GetAccountsByIDs(ctx context.Context, ids []int64) ([]db.Account, error) {
	accounts := make([]db.Account, 0, len(ids))
	for _, id := range ids {
		if account, ok := s.accountsByID[id]; ok {
			accounts = append(accounts, account)
		}
	}
	return accounts, nil
}

func (s *statusSerializerStub) GetAccountByID(ctx context.Context, id int64) (db.Account, error) {
	if account, ok := s.accountsByID[id]; ok {
		return account, nil
	}
	return db.Account{}, nil
}

func (s *statusSerializerStub) GetMediaAttachmentsByStatusIDs(ctx context.Context, statusIds []int64) ([]db.MediaAttachment, error) {
	attachments := []db.MediaAttachment{}
	for _, id := range statusIds {
		if list, ok := s.mediaByStatusID[id]; ok {
			attachments = append(attachments, list...)
		}
	}
	return attachments, nil
}

func (s *statusSerializerStub) GetMentionsByStatusIDs(ctx context.Context, statusIds []int64) ([]db.Mention, error) {
	return nil, nil
}

func (s *statusSerializerStub) GetStatusStatsByIDs(ctx context.Context, statusIds []int64) ([]db.StatusStat, error) {
	return nil, nil
}

func (s *statusSerializerStub) GetTagsByStatusIDs(ctx context.Context, statusIds []int64) ([]db.GetTagsByStatusIDsRow, error) {
	return nil, nil
}

func (s *statusSerializerStub) ListConversationMutes(ctx context.Context, arg db.ListConversationMutesParams) ([]int64, error) {
	return nil, nil
}

func (s *statusSerializerStub) GetPreviewCardsByStatusIDs(ctx context.Context, statusIds []int64) ([]db.GetPreviewCardsByStatusIDsRow, error) {
	return nil, nil
}

func (s *statusSerializerStub) GetQuotesByStatusIDs(ctx context.Context, statusIds []int64) ([]db.Quote, error) {
	quotes := []db.Quote{}
	for _, id := range statusIds {
		if quote, ok := s.quotesByStatusID[id]; ok {
			quotes = append(quotes, quote)
		}
	}
	return quotes, nil
}

func (s *statusSerializerStub) GetStatusesByIDs(ctx context.Context, ids []int64) ([]db.Status, error) {
	statuses := make([]db.Status, 0, len(ids))
	for _, id := range ids {
		if status, ok := s.statusesByID[id]; ok {
			statuses = append(statuses, status)
		}
	}
	return statuses, nil
}

func (s *statusSerializerStub) ListAccountStatsByAccountIDs(ctx context.Context, accountIds []int64) ([]db.AccountStat, error) {
	return nil, nil
}

func (s *statusSerializerStub) ListUserSettingsByAccountIDs(ctx context.Context, accountIds []int64) ([]db.ListUserSettingsByAccountIDsRow, error) {
	return nil, nil
}

func (s *statusSerializerStub) ListUserRolesByAccountIDs(ctx context.Context, accountIds []int64) ([]db.ListUserRolesByAccountIDsRow, error) {
	return nil, nil
}

func (s *statusSerializerStub) ListCustomEmojisByShortcodesAndDomains(ctx context.Context, arg db.ListCustomEmojisByShortcodesAndDomainsParams) ([]db.CustomEmoji, error) {
	return nil, nil
}

func (s *statusSerializerStub) ListCustomEmojisByShortcodesAndDomain(ctx context.Context, arg db.ListCustomEmojisByShortcodesAndDomainParams) ([]db.CustomEmoji, error) {
	return nil, nil
}

func (s *statusSerializerStub) GetAccountsByUsernames(ctx context.Context, usernames []string) ([]db.Account, error) {
	return nil, nil
}

func (s *statusSerializerStub) GetAccountsByUsernamesAndDomains(ctx context.Context, arg db.GetAccountsByUsernamesAndDomainsParams) ([]db.Account, error) {
	return nil, nil
}

func (s *statusSerializerStub) ListFavouritedStatusIDs(ctx context.Context, arg db.ListFavouritedStatusIDsParams) ([]int64, error) {
	return nil, nil
}

func (s *statusSerializerStub) ListBookmarkedStatusIDs(ctx context.Context, arg db.ListBookmarkedStatusIDsParams) ([]int64, error) {
	return nil, nil
}

func (s *statusSerializerStub) ListRebloggedStatusIDs(ctx context.Context, arg db.ListRebloggedStatusIDsParams) ([]pgtype.Int8, error) {
	return nil, nil
}

func (s *statusSerializerStub) ListPinnedStatusIDs(ctx context.Context, arg db.ListPinnedStatusIDsParams) ([]int64, error) {
	return nil, nil
}

func TestSerializeStatuses_EmbedsReblog(t *testing.T) {
	now := time.Date(2024, 2, 3, 4, 5, 6, 0, time.UTC)
	original := db.Status{
		ID:         10,
		AccountID:  1,
		Text:       "hello",
		CreatedAt:  pgtype.Timestamptz{Time: now, Valid: true},
		Visibility: 0,
		Uri:        pgtype.Text{String: "https://example.com/users/alice/statuses/10", Valid: true},
		Url:        pgtype.Text{String: "https://example.com/@alice/10", Valid: true},
	}
	reblog := db.Status{
		ID:         20,
		AccountID:  2,
		Text:       "",
		CreatedAt:  pgtype.Timestamptz{Time: now.Add(2 * time.Minute), Valid: true},
		Visibility: 0,
		ReblogOfID: pgtype.Int8{Int64: 10, Valid: true},
		Uri:        pgtype.Text{String: "https://example.com/users/bob/statuses/20", Valid: true},
		Url:        pgtype.Text{String: "https://example.com/@bob/20", Valid: true},
	}

	attachment := db.MediaAttachment{
		ID:                 99,
		StatusID:           pgtype.Int8{Int64: 10, Valid: true},
		Type:               MediaTypeImage,
		RemoteUrl:          "https://cdn.example.com/files/1.jpg",
		ThumbnailRemoteUrl: pgtype.Text{String: "https://cdn.example.com/files/1_thumb.jpg", Valid: true},
	}

	stub := &statusSerializerStub{
		accountsByID: map[int64]db.Account{
			1: {ID: 1, Username: "alice", Uri: "https://example.com/users/alice", Url: pgtype.Text{String: "https://example.com/@alice", Valid: true}},
			2: {ID: 2, Username: "bob", Uri: "https://example.com/users/bob", Url: pgtype.Text{String: "https://example.com/@bob", Valid: true}},
		},
		statusesByID: map[int64]db.Status{
			10: original,
		},
		mediaByStatusID: map[int64][]db.MediaAttachment{
			10: {attachment},
		},
	}

	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	got := serializeStatusesInternal(context.Background(), stub, cfg, []db.Status{reblog}, statusSerializeOptions{
		includeReblogs: true,
		includeQuotes:  true,
	})

	if len(got) != 1 {
		t.Fatalf("statuses length = %d, want 1", len(got))
	}
	if got[0].Reblog == nil {
		t.Fatalf("reblog is nil, want embedded status")
	}
	if got[0].Reblog.ID != "10" {
		t.Fatalf("reblog id = %q, want %q", got[0].Reblog.ID, "10")
	}
	if got[0].Reblog.Account.ID != "1" {
		t.Fatalf("reblog account id = %q, want %q", got[0].Reblog.Account.ID, "1")
	}
	if len(got[0].Reblog.MediaAttachments) != 1 {
		t.Fatalf("reblog media_attachments length = %d, want 1", len(got[0].Reblog.MediaAttachments))
	}
	if got[0].Reblog.MediaAttachments[0].URL != "https://cdn.example.com/files/1.jpg" {
		t.Fatalf("reblog media url = %q, want %q", got[0].Reblog.MediaAttachments[0].URL, "https://cdn.example.com/files/1.jpg")
	}
}

func TestSerializeStatuses_EmbedsQuote(t *testing.T) {
	now := time.Date(2024, 2, 3, 4, 5, 6, 0, time.UTC)
	quoted := db.Status{
		ID:         10,
		AccountID:  1,
		Text:       "quoted",
		CreatedAt:  pgtype.Timestamptz{Time: now, Valid: true},
		Visibility: 0,
		Uri:        pgtype.Text{String: "https://example.com/users/alice/statuses/10", Valid: true},
		Url:        pgtype.Text{String: "https://example.com/@alice/10", Valid: true},
	}
	quoting := db.Status{
		ID:         20,
		AccountID:  2,
		Text:       "quoting",
		CreatedAt:  pgtype.Timestamptz{Time: now.Add(2 * time.Minute), Valid: true},
		Visibility: 0,
		Uri:        pgtype.Text{String: "https://example.com/users/bob/statuses/20", Valid: true},
		Url:        pgtype.Text{String: "https://example.com/@bob/20", Valid: true},
	}

	attachment := db.MediaAttachment{
		ID:                 99,
		StatusID:           pgtype.Int8{Int64: 10, Valid: true},
		Type:               MediaTypeImage,
		RemoteUrl:          "https://cdn.example.com/files/1.jpg",
		ThumbnailRemoteUrl: pgtype.Text{String: "https://cdn.example.com/files/1_thumb.jpg", Valid: true},
	}

	stub := &statusSerializerStub{
		accountsByID: map[int64]db.Account{
			1: {ID: 1, Username: "alice", Uri: "https://example.com/users/alice", Url: pgtype.Text{String: "https://example.com/@alice", Valid: true}},
			2: {ID: 2, Username: "bob", Uri: "https://example.com/users/bob", Url: pgtype.Text{String: "https://example.com/@bob", Valid: true}},
		},
		statusesByID: map[int64]db.Status{
			10: quoted,
		},
		mediaByStatusID: map[int64][]db.MediaAttachment{
			10: {attachment},
		},
		quotesByStatusID: map[int64]db.Quote{
			20: {StatusID: 20, QuotedStatusID: pgtype.Int8{Int64: 10, Valid: true}},
		},
	}

	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	got := serializeStatusesInternal(context.Background(), stub, cfg, []db.Status{quoting}, statusSerializeOptions{
		includeReblogs: true,
		includeQuotes:  true,
	})

	if len(got) != 1 {
		t.Fatalf("statuses length = %d, want 1", len(got))
	}
	if got[0].QuoteID == nil || *got[0].QuoteID != "10" {
		t.Fatalf("quote_id = %v, want %q", got[0].QuoteID, "10")
	}
	if got[0].Quote == nil {
		t.Fatalf("quote is nil, want embedded status")
	}
	if got[0].Quote.ID != "10" {
		t.Fatalf("quote id = %q, want %q", got[0].Quote.ID, "10")
	}
	if len(got[0].Quote.MediaAttachments) != 1 {
		t.Fatalf("quote media_attachments length = %d, want 1", len(got[0].Quote.MediaAttachments))
	}
	if got[0].Quote.MediaAttachments[0].URL != "https://cdn.example.com/files/1.jpg" {
		t.Fatalf("quote media url = %q, want %q", got[0].Quote.MediaAttachments[0].URL, "https://cdn.example.com/files/1.jpg")
	}
}
