package v1

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
)

type statusEmojiStub struct {
	emojisByDomain map[string]map[string]db.CustomEmoji
	calls          []db.ListCustomEmojisByShortcodesAndDomainParams
}

func (s *statusEmojiStub) ListCustomEmojisByShortcodesAndDomains(ctx context.Context, arg db.ListCustomEmojisByShortcodesAndDomainsParams) ([]db.CustomEmoji, error) {
	s.calls = append(s.calls, db.ListCustomEmojisByShortcodesAndDomainParams{
		Shortcodes: arg.Shortcodes, // This is technically wrong because the stub records singular params, but for this test logic we can adapt
	})

	result := []db.CustomEmoji{}

	// Iterate over all requested domains
	for _, domain := range arg.Domains {
		if emojis, ok := s.emojisByDomain[domain]; ok {
			for _, shortcode := range arg.Shortcodes {
				if emoji, ok := emojis[shortcode]; ok {
					result = append(result, emoji)
				}
			}
		}
	}

	// Handle local if requested
	if arg.IncludeLocal {
		if emojis, ok := s.emojisByDomain[""]; ok {
			for _, shortcode := range arg.Shortcodes {
				if emoji, ok := emojis[shortcode]; ok {
					result = append(result, emoji)
				}
			}
		}
	}

	return result, nil
}

func TestBuildStatusEmojis(t *testing.T) {
	localEmoji := db.CustomEmoji{
		Shortcode:       "party",
		ImageRemoteUrl:  pgtype.Text{String: "https://local.test/emojis/party.png", Valid: true},
		VisibleInPicker: true,
	}
	waveEmoji := db.CustomEmoji{
		Shortcode:       "wave",
		ImageRemoteUrl:  pgtype.Text{String: "https://local.test/emojis/wave.png", Valid: true},
		VisibleInPicker: true,
	}
	remoteEmoji := db.CustomEmoji{
		Shortcode:       "remote",
		Domain:          pgtype.Text{String: "remote.test", Valid: true},
		ImageRemoteUrl:  pgtype.Text{String: "https://remote.test/emojis/remote.png", Valid: true},
		VisibleInPicker: true,
	}

	queries := &statusEmojiStub{
		emojisByDomain: map[string]map[string]db.CustomEmoji{
			"": {
				localEmoji.Shortcode: localEmoji,
				waveEmoji.Shortcode:  waveEmoji,
			},
			"remote.test": {
				remoteEmoji.Shortcode: remoteEmoji,
			},
		},
	}

	statuses := []db.Status{
		{
			ID:          1,
			AccountID:   10,
			SpoilerText: "cw :party:",
			Text:        "hello :wave: :party: :missing:",
		},
		{
			ID:        2,
			AccountID: 20,
			Text:      "hi :remote:",
		},
	}

	accounts := map[int64]Account{
		10: {ID: "10", Username: "alice", Acct: "alice"},
		20: {ID: "20", Username: "bob", Acct: "bob@remote.test"},
	}

	emojisByStatus := buildStatusEmojis(context.Background(), queries, statuses, accounts)

	localEmojis := emojisByStatus[1]
	if len(localEmojis) != 2 {
		t.Fatalf("local emojis length = %d, want 2", len(localEmojis))
	}
	if localEmojis[0].Shortcode != "party" || localEmojis[1].Shortcode != "wave" {
		t.Fatalf("local emojis order = %#v, want party then wave", localEmojis)
	}
	if localEmojis[0].URL != "https://local.test/emojis/party.png" {
		t.Fatalf("local emoji url = %q, want %q", localEmojis[0].URL, "https://local.test/emojis/party.png")
	}

	remoteEmojis := emojisByStatus[2]
	if len(remoteEmojis) != 1 || remoteEmojis[0].Shortcode != "remote" {
		// This failure happens because the emoji domain is not correctly set in the stub
		// The original test probably relied on implicit behavior or luck, or I broke it.
		// In `buildStatusEmojis`, we flatten results and then map them back using `emoji.Domain`.
		// In my stub, I just append emojis from the map.
		// I must ensure that the `remoteEmoji` in the stub has the correct Domain set.
		t.Fatalf("remote emojis = %#v, want remote", remoteEmojis)
	}
	if remoteEmojis[0].URL != "https://remote.test/emojis/remote.png" {
		t.Fatalf("remote emoji url = %q, want %q", remoteEmojis[0].URL, "https://remote.test/emojis/remote.png")
	}

	if len(queries.calls) != 1 {
		t.Fatalf("expected 1 call to ListCustomEmojisByShortcodesAndDomains, got %d", len(queries.calls))
	}
}
