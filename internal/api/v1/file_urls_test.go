package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/media"
)

// fileURLConfig returns a config whose uploads live in a temporary directory
// and are served from the web domain.
func fileURLConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		Instance: config.InstanceConfig{LocalDomain: "social.example", WebDomain: "web.example"},
		Media:    config.MediaConfig{StoragePath: t.TempDir()},
	}
}

// fetchServedFile requests rawURL from the routes the server registers for
// uploads and checks it is served with Mastodon's headers.
func fetchServedFile(t *testing.T, cfg *config.Config, rawURL string, want []byte) {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse %q: %v", rawURL, err)
	}
	if parsed.Scheme != "https" || parsed.Host != "web.example" {
		t.Fatalf("URL %q is not an absolute https://web.example URL", rawURL)
	}

	mux := http.NewServeMux()
	media.NewFileServer(media.NewStorage(cfg).BasePath).RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want %d", rawURL, rec.Code, http.StatusOK)
	}
	if want != nil && !bytes.Equal(rec.Body.Bytes(), want) {
		t.Fatalf("GET %s body differs from the stored file", rawURL)
	}
	for name, value := range map[string]string{
		"Cache-Control":           "public, max-age=2419200, immutable",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "default-src 'none'; form-action 'none'",
	} {
		if got := rec.Header().Get(name); got != value {
			t.Errorf("GET %s %s = %q, want %q", rawURL, name, got, value)
		}
	}
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	return []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")
}

// TestAPIFileURLsAreServed stores each kind of file the API links to where
// gotodon stores it, serializes the record that references it, and fetches
// the URL the API returns.
func TestAPIFileURLsAreServed(t *testing.T) {
	// pgx returns timestamps in the local zone; uploads are dated in UTC.
	previous := time.Local
	time.Local = time.FixedZone("UTC+9", 9*60*60)
	t.Cleanup(func() { time.Local = previous })

	cfg := fileURLConfig(t)
	storage := media.NewStorage(cfg)
	uploader := media.NewUploader(storage)
	ctx := context.Background()
	data := pngBytes(t)

	t.Run("avatar and header", func(t *testing.T) {
		avatar, err := uploader.UploadWithOptions(ctx, 5, "me.png", data, media.UploadOptions{SkipProcessing: true})
		if err != nil {
			t.Fatalf("upload avatar: %v", err)
		}
		header, err := uploader.UploadWithOptions(ctx, 5, "banner.png", data, media.UploadOptions{SkipProcessing: true})
		if err != nil {
			t.Fatalf("upload header: %v", err)
		}
		acc := SerializeAccount(cfg, db.Account{
			ID:             5,
			Username:       "alice",
			AvatarFileName: pgtype.Text{String: avatar.Path, Valid: true},
			HeaderFileName: pgtype.Text{String: header.Path, Valid: true},
		})
		for _, u := range []string{acc.Avatar, acc.AvatarStatic} {
			fetchServedFile(t, cfg, u, data)
		}
		for _, u := range []string{acc.Header, acc.HeaderStatic} {
			fetchServedFile(t, cfg, u, data)
		}

		pinned := accountFromPinRow(cfg, db.ListAccountPinsRow{
			TargetAccountID: 5,
			Username:        "alice",
			AvatarFileName:  pgtype.Text{String: avatar.Path, Valid: true},
			HeaderFileName:  pgtype.Text{String: header.Path, Valid: true},
		})
		fetchServedFile(t, cfg, pinned.Avatar, data)
		fetchServedFile(t, cfg, pinned.Header, data)
	})

	t.Run("media attachment", func(t *testing.T) {
		// Shortly after midnight in UTC+9 is the previous day in UTC.
		createdAt := time.Date(2026, 11, 14, 0, 15, 0, 0, time.Local)
		upload, err := uploader.UploadWithOptions(ctx, 1042, "photo.png", data, media.UploadOptions{SkipProcessing: true, At: createdAt.UTC()})
		if err != nil {
			t.Fatalf("upload: %v", err)
		}
		thumbPath := media.GenerateThumbnailPath(upload.Path)
		thumb := append([]byte{}, data...)
		if err := storage.Save(ctx, thumbPath, thumb); err != nil {
			t.Fatalf("save thumbnail: %v", err)
		}
		record := db.MediaAttachment{
			ID:                9,
			AccountID:         pgtype.Int8{Int64: 1042, Valid: true},
			CreatedAt:         pgtype.Timestamptz{Time: createdAt, Valid: true},
			FileFileName:      pgtype.Text{String: path.Base(upload.Path), Valid: true},
			ThumbnailFileName: pgtype.Text{String: path.Base(thumbPath), Valid: true},
		}

		attach := serializeMediaAttach(storage, record)
		fetchServedFile(t, cfg, attach.URL, data)
		fetchServedFile(t, cfg, attach.PreviewURL, thumb)
		if attach.URL == attach.PreviewURL {
			t.Fatalf("preview_url = url %q, want the thumbnail", attach.URL)
		}

		apiMedia := dbMediaToAPI(storage, record)
		if apiMedia.URL != attach.URL || apiMedia.PreviewURL != attach.PreviewURL {
			t.Fatalf("media API urls = %q, %q; want %q, %q", apiMedia.URL, apiMedia.PreviewURL, attach.URL, attach.PreviewURL)
		}
	})

	t.Run("preview card image", func(t *testing.T) {
		imagePath := "preview_cards/images/0a1b2c3d4e5f60718293.png"
		if err := storage.Save(ctx, imagePath, data); err != nil {
			t.Fatalf("save: %v", err)
		}
		card := serializePreviewCard(storage, db.PreviewCard{ID: 1, Url: "https://example.org/", ImageFileName: pgtype.Text{String: imagePath, Valid: true}}, nil)
		if card == nil || card.Image == nil {
			t.Fatalf("card image missing: %#v", card)
		}
		fetchServedFile(t, cfg, *card.Image, data)
	})

	t.Run("custom emoji", func(t *testing.T) {
		imagePath := "custom_emojis/images/original/0123456789abcdef.png"
		if err := storage.Save(ctx, imagePath, data); err != nil {
			t.Fatalf("save: %v", err)
		}
		fetchServedFile(t, cfg, GetEmojiURL(storage, db.CustomEmoji{Shortcode: "blob", ImageFileName: pgtype.Text{String: imagePath, Valid: true}}), data)
	})

	t.Run("default avatar and header", func(t *testing.T) {
		acc := SerializeAccount(cfg, db.Account{ID: 6, Username: "bob"})
		fetchServedFile(t, cfg, acc.Avatar, nil)
		fetchServedFile(t, cfg, acc.Header, nil)
	})
}

func TestSerializeAccountImageURLs(t *testing.T) {
	cfg := fileURLConfig(t)
	cdn := fileURLConfig(t)
	cdn.Media.BaseURL = "https://cdn.example.net/media"

	tests := []struct {
		name       string
		cfg        *config.Config
		account    db.Account
		wantAvatar string
		wantHeader string
	}{
		{
			name:       "stored images",
			cfg:        cfg,
			account:    db.Account{ID: 1, Username: "a", AvatarFileName: pgtype.Text{String: "media_attachments/files/1/1/a.png", Valid: true}, HeaderFileName: pgtype.Text{String: "media_attachments/files/1/1/h.png", Valid: true}},
			wantAvatar: "https://web.example/files/media_attachments/files/1/1/a.png",
			wantHeader: "https://web.example/files/media_attachments/files/1/1/h.png",
		},
		{
			name:       "configured base URL",
			cfg:        cdn,
			account:    db.Account{ID: 1, Username: "a", AvatarFileName: pgtype.Text{String: "media_attachments/files/1/1/a.png", Valid: true}},
			wantAvatar: "https://cdn.example.net/media/media_attachments/files/1/1/a.png",
			wantHeader: "https://web.example/headers/original/missing.png",
		},
		{
			name:       "remote images stay unchanged",
			cfg:        cfg,
			account:    db.Account{ID: 2, Username: "r", Domain: pgtype.Text{String: "remote.example", Valid: true}, AvatarRemoteUrl: pgtype.Text{String: "https://remote.example/a.png", Valid: true}, HeaderRemoteUrl: "https://remote.example/h.png"},
			wantAvatar: "https://remote.example/a.png",
			wantHeader: "https://remote.example/h.png",
		},
		{
			name:       "no images",
			cfg:        cfg,
			account:    db.Account{ID: 3, Username: "n"},
			wantAvatar: "https://web.example/avatars/original/missing.png",
			wantHeader: "https://web.example/headers/original/missing.png",
		},
		{
			name:       "relative remote URL is ignored",
			cfg:        cfg,
			account:    db.Account{ID: 4, Username: "x", AvatarRemoteUrl: pgtype.Text{String: "/a.png", Valid: true}, HeaderRemoteUrl: "h.png"},
			wantAvatar: "https://web.example/avatars/original/missing.png",
			wantHeader: "https://web.example/headers/original/missing.png",
		},
		{
			name:       "suspended",
			cfg:        cfg,
			account:    db.Account{ID: 5, Username: "s", SuspendedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}, AvatarFileName: pgtype.Text{String: "a.png", Valid: true}},
			wantAvatar: "https://web.example/avatars/original/missing.png",
			wantHeader: "https://web.example/headers/original/missing.png",
		},
		{
			// No domain is invented; serve refuses to start without one.
			name:       "no configuration",
			cfg:        nil,
			account:    db.Account{ID: 6, Username: "d", AvatarFileName: pgtype.Text{String: "a.png", Valid: true}},
			wantAvatar: "https:///files/a.png",
			wantHeader: "https:///headers/original/missing.png",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acc := SerializeAccount(tt.cfg, tt.account)
			if acc.Avatar != tt.wantAvatar || acc.AvatarStatic != tt.wantAvatar {
				t.Errorf("avatar = %q, avatar_static = %q, want %q", acc.Avatar, acc.AvatarStatic, tt.wantAvatar)
			}
			if acc.Header != tt.wantHeader || acc.HeaderStatic != tt.wantHeader {
				t.Errorf("header = %q, header_static = %q, want %q", acc.Header, acc.HeaderStatic, tt.wantHeader)
			}
		})
	}
}

func TestAccountFromPinRowImageURLs(t *testing.T) {
	cfg := fileURLConfig(t)
	remote := accountFromPinRow(cfg, db.ListAccountPinsRow{
		TargetAccountID: 7,
		Username:        "r",
		Domain:          pgtype.Text{String: "remote.example", Valid: true},
		AvatarRemoteUrl: pgtype.Text{String: "https://remote.example/a.png", Valid: true},
	})
	if remote.Avatar != "https://remote.example/a.png" {
		t.Errorf("avatar = %q, want the remote URL", remote.Avatar)
	}
	if remote.Header != "https://web.example/headers/original/missing.png" {
		t.Errorf("header = %q, want the default header", remote.Header)
	}
}

func TestMediaAttachmentURLs(t *testing.T) {
	storage := media.NewLocalStorage(t.TempDir(), "https://web.example/files")
	createdAt := time.Date(2026, 3, 2, 23, 30, 0, 0, time.UTC)
	local := db.MediaAttachment{
		AccountID:    pgtype.Int8{Int64: 3, Valid: true},
		CreatedAt:    pgtype.Timestamptz{Time: createdAt, Valid: true},
		FileFileName: pgtype.Text{String: "abc.png", Valid: true},
	}
	withThumb := local
	withThumb.ThumbnailFileName = pgtype.Text{String: "abc_thumb.png", Valid: true}
	remote := db.MediaAttachment{
		RemoteUrl:          "https://remote.example/a.mp4",
		ThumbnailRemoteUrl: pgtype.Text{String: "https://remote.example/a.jpg", Valid: true},
	}
	remoteNoThumb := db.MediaAttachment{RemoteUrl: "https://remote.example/a.png"}
	relativeRemote := db.MediaAttachment{RemoteUrl: "/a.png", ThumbnailRemoteUrl: pgtype.Text{String: "a.jpg", Valid: true}}
	noDate := local
	noDate.CreatedAt = pgtype.Timestamptz{}

	tests := []struct {
		name        string
		attachment  db.MediaAttachment
		wantURL     string
		wantPreview string
	}{
		{name: "local without thumbnail", attachment: local, wantURL: "https://web.example/files/media_attachments/files/3/3/2026/03/02/abc.png", wantPreview: "https://web.example/files/media_attachments/files/3/3/2026/03/02/abc.png"},
		{name: "local with thumbnail", attachment: withThumb, wantURL: "https://web.example/files/media_attachments/files/3/3/2026/03/02/abc.png", wantPreview: "https://web.example/files/media_attachments/files/3/3/2026/03/02/abc_thumb.png"},
		{name: "remote", attachment: remote, wantURL: "https://remote.example/a.mp4", wantPreview: "https://remote.example/a.jpg"},
		{name: "remote without thumbnail", attachment: remoteNoThumb, wantURL: "https://remote.example/a.png", wantPreview: "https://remote.example/a.png"},
		{name: "relative remote URLs are dropped", attachment: relativeRemote, wantURL: "", wantPreview: ""},
		{name: "local without created_at", attachment: noDate, wantURL: "", wantPreview: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attach := serializeMediaAttach(storage, tt.attachment)
			if attach.URL != tt.wantURL || attach.PreviewURL != tt.wantPreview {
				t.Errorf("serializeMediaAttach url = %q, preview_url = %q; want %q, %q", attach.URL, attach.PreviewURL, tt.wantURL, tt.wantPreview)
			}
			apiMedia := dbMediaToAPI(storage, tt.attachment)
			if apiMedia.URL != tt.wantURL || apiMedia.PreviewURL != tt.wantPreview {
				t.Errorf("dbMediaToAPI url = %q, preview_url = %q; want %q, %q", apiMedia.URL, apiMedia.PreviewURL, tt.wantURL, tt.wantPreview)
			}
		})
	}
}

type customEmojiListStub struct {
	db.Querier
	emojis []db.CustomEmoji
}

func (s *customEmojiListStub) ListCustomEmojis(context.Context) ([]db.CustomEmoji, error) {
	return s.emojis, nil
}

func TestCustomEmojisListReturnsAbsoluteURLs(t *testing.T) {
	cfg := fileURLConfig(t)
	h := NewCustomEmojisHandler(&customEmojiListStub{emojis: []db.CustomEmoji{
		{Shortcode: "local", ImageFileName: pgtype.Text{String: "custom_emojis/images/original/a.png", Valid: true}, VisibleInPicker: true},
		{Shortcode: "remote", Domain: pgtype.Text{String: "remote.example", Valid: true}, ImageRemoteUrl: pgtype.Text{String: "https://remote.example/e.png", Valid: true}},
		{Shortcode: "remote_blank", ImageRemoteUrl: pgtype.Text{String: "", Valid: true}, ImageFileName: pgtype.Text{String: "custom_emojis/images/original/b.png", Valid: true}},
	}}, cfg)

	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/api/v1/custom_emojis", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	assertNoRelativeFileURLs(t, rec.Body.Bytes())

	var got []CustomEmoji
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := map[string]string{
		"local":        "https://web.example/files/custom_emojis/images/original/a.png",
		"remote":       "https://remote.example/e.png",
		"remote_blank": "https://web.example/files/custom_emojis/images/original/b.png",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d emojis, want %d", len(got), len(want))
	}
	for _, emoji := range got {
		if emoji.URL != want[emoji.Shortcode] || emoji.StaticURL != want[emoji.Shortcode] {
			t.Errorf("%s: url = %q, static_url = %q, want %q", emoji.Shortcode, emoji.URL, emoji.StaticURL, want[emoji.Shortcode])
		}
	}
}

// assertNoRelativeFileURLs fails when a JSON document holds a string that
// points at stored files or default images without a scheme and host.
func assertNoRelativeFileURLs(t *testing.T, body []byte) {
	t.Helper()
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var walk func(any)
	walk = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			for _, item := range v {
				walk(item)
			}
		case []any:
			for _, item := range v {
				walk(item)
			}
		case string:
			for _, prefix := range []string{"/files/", "/avatars/", "/headers/"} {
				if strings.HasPrefix(v, prefix) {
					t.Errorf("relative file URL %q in response", v)
				}
			}
		}
	}
	walk(doc)
}
