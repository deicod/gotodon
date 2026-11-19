package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deicod/gotodon/internal/config"
)

func TestLocalStorage(t *testing.T) {
	storage := NewLocalStorage(filepath.Join(t.TempDir(), "uploads"), "https://example.com/files")
	ctx := context.Background()

	t.Run("Missing storage directory", func(t *testing.T) {
		const name = "test/file.txt"
		if _, err := storage.Load(ctx, name); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("Load() error = %v, want %v", err, os.ErrNotExist)
		}
		if exists, err := storage.Exists(ctx, name); exists || err != nil {
			t.Errorf("Exists() = %v, %v, want false, nil", exists, err)
		}
		if err := storage.Delete(ctx, name); err != nil {
			t.Errorf("Delete() error = %v, want nil", err)
		}
		if _, err := os.Lstat(storage.BasePath); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("storage directory should still be absent, got %v", err)
		}
	})

	t.Run("Save and Load", func(t *testing.T) {
		data := []byte("test content")
		path := "test/file.txt"

		if err := storage.Save(ctx, path, data); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		loaded, err := storage.Load(ctx, path)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}

		if string(loaded) != string(data) {
			t.Errorf("Load() = %q, want %q", loaded, data)
		}
	})

	t.Run("URL", func(t *testing.T) {
		url := storage.URL("media/file.jpg")
		expected := "https://example.com/files/media/file.jpg"
		if url != expected {
			t.Errorf("URL() = %q, want %q", url, expected)
		}
	})

	t.Run("Exists", func(t *testing.T) {
		path := "exists.txt"
		if err := storage.Save(ctx, path, []byte("test")); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		exists, err := storage.Exists(ctx, path)
		if err != nil {
			t.Fatalf("Exists() error = %v", err)
		}
		if !exists {
			t.Error("Exists() = false, want true")
		}

		exists, err = storage.Exists(ctx, "nonexistent.txt")
		if err != nil {
			t.Fatalf("Exists() error = %v", err)
		}
		if exists {
			t.Error("Exists() = true for nonexistent file")
		}
	})

	t.Run("Delete", func(t *testing.T) {
		path := "delete/test.txt"
		_ = storage.Save(ctx, path, []byte("test"))

		if err := storage.Delete(ctx, path); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}

		exists, _ := storage.Exists(ctx, path)
		if exists {
			t.Error("File should not exist after delete")
		}

		// Delete nonexistent should not error
		if err := storage.Delete(ctx, "nonexistent.txt"); err != nil {
			t.Errorf("Delete nonexistent should not error: %v", err)
		}
	})
}

func TestLocalStorageRejectsInvalidPaths(t *testing.T) {
	parent := t.TempDir()
	outside := filepath.Join(parent, "x")
	tests := []struct {
		name string
		path string
	}{
		{name: "parent traversal", path: "../x"},
		{name: "nested traversal", path: "a/../../x"},
		{name: "unclean path within root", path: "a/../x"},
		{name: "leading dot segment", path: "./x"},
		{name: "repeated separator", path: "a//x"},
		{name: "trailing separator", path: "x/"},
		{name: "absolute path", path: outside},
		{name: "NUL byte", path: "a/nu\x00l"},
		{name: "storage root", path: "."},
		{name: "empty path", path: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := filepath.Join(parent, "uploads")
			assertStoragePathRejected(t, NewLocalStorage(root, ""), tt.path, os.ErrInvalid)
			for _, name := range []string{root, outside} {
				if _, err := os.Lstat(name); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("%q should still be absent, got %v", name, err)
				}
			}
		})
	}
}

func TestLocalStorageRejectsSymlinkEscapes(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "uploads")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	const original = "outside storage"
	writeFile(t, parent, "outside/x", []byte(original))
	for link, target := range map[string]string{
		"dir":  filepath.Join("..", "outside"),
		"leaf": filepath.Join("..", "outside", "x"),
	} {
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}
	storage := NewLocalStorage(root, "")
	for _, name := range []string{"dir/x", "dir/missing/x", "leaf"} {
		t.Run(name, func(t *testing.T) {
			assertStoragePathRejected(t, storage, name, nil)
		})
	}
	if data, err := os.ReadFile(filepath.Join(parent, "outside", "x")); err != nil || string(data) != original {
		t.Errorf("outside file changed: got %q, %v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(parent, "outside", "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("outside directory should still be absent, got %v", err)
	}
	if _, err := os.Readlink(filepath.Join(root, "leaf")); err != nil {
		t.Errorf("leaf symlink should still exist, got %v", err)
	}
}

func assertStoragePathRejected(t *testing.T, storage *LocalStorage, path string, want error) {
	t.Helper()
	ctx := t.Context()
	data, loadErr := storage.Load(ctx, path)
	if data != nil {
		t.Errorf("Load(%q) returned data", path)
	}
	exists, existsErr := storage.Exists(ctx, path)
	if exists {
		t.Errorf("Exists(%q) = true", path)
	}
	for _, op := range []struct {
		name string
		err  error
	}{
		{"Save", storage.Save(ctx, path, []byte("replacement"))},
		{"Load", loadErr},
		{"Delete", storage.Delete(ctx, path)},
		{"Exists", existsErr},
	} {
		if op.err == nil {
			t.Errorf("%s(%q) succeeded, want an error", op.name, path)
		} else if want != nil && !errors.Is(op.err, want) {
			t.Errorf("%s(%q) error = %v, want %v", op.name, path, op.err, want)
		}
	}
}

func TestGenerateStoragePath(t *testing.T) {
	path := GenerateStoragePath(12345, "photo.jpg", time.Time{})

	if filepath.Ext(path) != ".jpg" {
		t.Errorf("Extension should be .jpg, got %s", filepath.Ext(path))
	}

	if len(path) < 24 || path[:24] != "media_attachments/files/" {
		t.Errorf("Path should start with media_attachments/files/, got %s", path)
	}
}

func TestGenerateThumbnailPath(t *testing.T) {
	tests := []struct {
		original string
		want     string
	}{
		{"path/to/image.jpg", "path/to/image_thumb.jpg"},
		{"file.png", "file_thumb.png"},
	}

	for _, tt := range tests {
		got := GenerateThumbnailPath(tt.original)
		if got != tt.want {
			t.Errorf("GenerateThumbnailPath(%q) = %q, want %q", tt.original, got, tt.want)
		}
	}
}

func TestUploader(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "gotodon-uploader-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	storage := NewLocalStorage(tmpDir, "https://example.com/files")
	uploader := NewUploader(storage)
	ctx := context.Background()

	t.Run("Upload image", func(t *testing.T) {
		// Create a simple PNG
		imageData := createTestImage(100, 100)

		result, err := uploader.Upload(ctx, 12345, "test.png", imageData)
		if err != nil {
			t.Fatalf("Upload() error = %v", err)
		}

		if result.MediaType != MediaTypeImage {
			t.Errorf("MediaType = %v, want %v", result.MediaType, MediaTypeImage)
		}

		if result.ContentType != "image/png" {
			t.Errorf("ContentType = %q, want %q", result.ContentType, "image/png")
		}

		if result.Meta == nil {
			t.Error("Meta should not be nil")
		} else {
			if result.Meta.Original.Width != 100 {
				t.Errorf("Meta.Original.Width = %d, want 100", result.Meta.Original.Width)
			}
		}

		if result.Blurhash == "" {
			t.Error("Blurhash should be generated")
		}

		// Verify file was saved
		exists, _ := storage.Exists(ctx, result.Path)
		if !exists {
			t.Error("File should exist in storage")
		}
	})

	t.Run("Upload image without processing", func(t *testing.T) {
		imageData := createTestImage(100, 100)

		result, err := uploader.UploadWithOptions(ctx, 12345, "test.png", imageData, UploadOptions{SkipProcessing: true})
		if err != nil {
			t.Fatalf("UploadWithOptions() error = %v", err)
		}

		if result.Meta != nil {
			t.Error("Meta should be nil when processing is skipped")
		}

		if result.Blurhash != "" {
			t.Error("Blurhash should be empty when processing is skipped")
		}
	})

	t.Run("Reject unsupported type", func(t *testing.T) {
		data := []byte("not an image")
		_, err := uploader.Upload(ctx, 12345, "test.txt", data)
		if err != ErrUnsupportedType {
			t.Errorf("Should reject unsupported type, got error: %v", err)
		}
	})
}

func TestFileURL(t *testing.T) {
	storage := NewLocalStorage(t.TempDir(), "https://cdn.example.com/files/")
	tests := []struct {
		name    string
		storage Storage
		stored  string
		want    string
	}{
		{name: "empty", storage: storage, stored: "", want: ""},
		{name: "blank", storage: storage, stored: "   ", want: ""},
		{name: "stored path", storage: storage, stored: "media_attachments/files/1/1/2026/11/14/a.png", want: "https://cdn.example.com/files/media_attachments/files/1/1/2026/11/14/a.png"},
		{name: "surrounding space", storage: storage, stored: " accounts/a.png ", want: "https://cdn.example.com/files/accounts/a.png"},
		{name: "leading slash", storage: storage, stored: "/custom_emojis/images/a.png", want: "https://cdn.example.com/files/custom_emojis/images/a.png"},
		{name: "escaped segments", storage: storage, stored: "preview_cards/images/a b#c?.png", want: "https://cdn.example.com/files/preview_cards/images/a%20b%23c%3F.png"},
		{name: "remote https", storage: storage, stored: "https://remote.example/avatar.png", want: "https://remote.example/avatar.png"},
		{name: "remote http", storage: storage, stored: "http://remote.example/avatar.png", want: "http://remote.example/avatar.png"},
		{name: "remote upper-case scheme", storage: storage, stored: "HTTPS://remote.example/a.png", want: "HTTPS://remote.example/a.png"},
		{name: "only slashes", storage: storage, stored: "///", want: ""},
		{name: "no storage", storage: nil, stored: "a.png", want: ""},
		{name: "no storage, remote", storage: nil, stored: "https://remote.example/a.png", want: "https://remote.example/a.png"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FileURL(tt.storage, tt.stored); got != tt.want {
				t.Errorf("FileURL(%q) = %q, want %q", tt.stored, got, tt.want)
			}
		})
	}
}

func TestNewStorage(t *testing.T) {
	tests := []struct {
		name      string
		cfg       *config.Config
		wantRoot  string
		wantURL   string
		wantSite  string
		wantImage string
	}{
		{
			// No domain is invented; serve refuses to start without one.
			name:      "nil config",
			cfg:       nil,
			wantRoot:  config.DefaultMediaStoragePath,
			wantURL:   "https:///files/a.png",
			wantSite:  "https://",
			wantImage: "https:///avatars/original/missing.png",
		},
		{
			name:      "no domain",
			cfg:       &config.Config{Media: config.MediaConfig{StoragePath: "/srv/uploads"}},
			wantRoot:  "/srv/uploads",
			wantURL:   "https:///files/a.png",
			wantSite:  "https://",
			wantImage: "https:///avatars/original/missing.png",
		},
		{
			name:      "local domain",
			cfg:       &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}},
			wantRoot:  config.DefaultMediaStoragePath,
			wantURL:   "https://example.com/files/a.png",
			wantSite:  "https://example.com",
			wantImage: "https://example.com/avatars/original/missing.png",
		},
		{
			name:      "web domain",
			cfg:       &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com", WebDomain: "social.example.com"}},
			wantRoot:  config.DefaultMediaStoragePath,
			wantURL:   "https://social.example.com/files/a.png",
			wantSite:  "https://social.example.com",
			wantImage: "https://social.example.com/avatars/original/missing.png",
		},
		{
			name: "configured storage and base URL",
			cfg: &config.Config{
				Instance: config.InstanceConfig{LocalDomain: "example.com"},
				Media:    config.MediaConfig{StoragePath: "/srv/uploads", BaseURL: "https://cdn.example.net/media/"},
			},
			wantRoot:  "/srv/uploads",
			wantURL:   "https://cdn.example.net/media/a.png",
			wantSite:  "https://example.com",
			wantImage: "https://example.com/avatars/original/missing.png",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage := NewStorage(tt.cfg)
			if storage.BasePath != tt.wantRoot {
				t.Errorf("BasePath = %q, want %q", storage.BasePath, tt.wantRoot)
			}
			if got := FileURL(storage, "a.png"); got != tt.wantURL {
				t.Errorf("FileURL = %q, want %q", got, tt.wantURL)
			}
			if got := SiteURL(tt.cfg); got != tt.wantSite {
				t.Errorf("SiteURL = %q, want %q", got, tt.wantSite)
			}
			if got := MissingAvatarURL(tt.cfg); got != tt.wantImage {
				t.Errorf("MissingAvatarURL = %q, want %q", got, tt.wantImage)
			}
			if got, want := MissingHeaderURL(tt.cfg), strings.Replace(tt.wantImage, "/avatars/", "/headers/", 1); got != want {
				t.Errorf("MissingHeaderURL = %q, want %q", got, want)
			}
		})
	}
}

// setLocalZone runs a test with time.Local set to zone.
func setLocalZone(t *testing.T, zone *time.Location) {
	t.Helper()
	previous := time.Local
	time.Local = zone
	t.Cleanup(func() { time.Local = previous })
}

func TestMediaStoragePathIsDatedInUTC(t *testing.T) {
	berlin := time.FixedZone("UTC+1", 60*60)
	setLocalZone(t, berlin)
	// 00:30 in UTC+1 is still the previous day in UTC.
	createdAt := time.Date(2026, 11, 14, 0, 30, 0, 0, berlin)

	want := "media_attachments/files/42/1042/2026/11/13/a.png"
	if got := MediaStoragePath(1042, createdAt, "a.png"); got != want {
		t.Fatalf("MediaStoragePath = %q, want %q", got, want)
	}
	if got := MediaStoragePath(1042, createdAt.UTC(), "a.png"); got != want {
		t.Fatalf("MediaStoragePath(UTC) = %q, want %q", got, want)
	}

	paths := MediaStoragePaths(1042, createdAt, "a.png")
	wantPaths := []string{want, "media_attachments/files/42/1042/2026/11/14/a.png"}
	if strings.Join(paths, ",") != strings.Join(wantPaths, ",") {
		t.Fatalf("MediaStoragePaths = %v, want %v", paths, wantPaths)
	}

	setLocalZone(t, time.UTC)
	if got := MediaStoragePaths(1042, createdAt, "a.png"); len(got) != 1 || got[0] != want {
		t.Fatalf("MediaStoragePaths in UTC = %v, want [%s]", got, want)
	}

	for _, tt := range []struct {
		accountID int64
		createdAt time.Time
		filename  string
	}{
		{0, createdAt, "a.png"},
		{1042, time.Time{}, "a.png"},
		{1042, createdAt, ""},
	} {
		if got := MediaStoragePath(tt.accountID, tt.createdAt, tt.filename); got != "" {
			t.Errorf("MediaStoragePath(%d, %v, %q) = %q, want empty", tt.accountID, tt.createdAt, tt.filename, got)
		}
		if got := MediaStoragePaths(tt.accountID, tt.createdAt, tt.filename); got != nil {
			t.Errorf("MediaStoragePaths(%d, %v, %q) = %v, want nil", tt.accountID, tt.createdAt, tt.filename, got)
		}
	}
}

func TestUploadPathMatchesPersistedCreatedAt(t *testing.T) {
	setLocalZone(t, time.FixedZone("UTC-8", -8*60*60))
	root := t.TempDir()
	uploader := NewUploader(NewLocalStorage(root, "https://example.com/files"))
	// 20:00 on the 13th in UTC-8 is the 14th in UTC.
	at := time.Date(2026, 11, 13, 20, 0, 0, 0, time.Local)

	result, err := uploader.UploadWithOptions(context.Background(), 7, "a.png", createTestImage(4, 4), UploadOptions{At: at})
	if err != nil {
		t.Fatalf("UploadWithOptions() error = %v", err)
	}
	if want := MediaStoragePath(7, at, filepath.Base(result.Path)); result.Path != want {
		t.Fatalf("Path = %q, want %q", result.Path, want)
	}
	if !strings.Contains(result.Path, "/2026/11/14/") {
		t.Fatalf("Path = %q, want it dated 2026/11/14 (UTC)", result.Path)
	}
	if want := "https://example.com/files/" + result.Path; result.URL != want {
		t.Fatalf("URL = %q, want %q", result.URL, want)
	}
}

func TestUploadNamesFilesByContentType(t *testing.T) {
	uploader := NewUploader(NewLocalStorage(t.TempDir(), "https://example.com/files"))
	tests := []struct {
		filename string
		wantExt  string
	}{
		{filename: "photo.png", wantExt: ".png"},
		{filename: "photo.html", wantExt: ".png"},
		{filename: "photo", wantExt: ".png"},
		{filename: "photo.png#x?y", wantExt: ".png"},
		{filename: "../../etc/passwd", wantExt: ".png"},
	}
	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			result, err := uploader.Upload(context.Background(), 1, tt.filename, createTestImage(2, 2))
			if err != nil {
				t.Fatalf("Upload() error = %v", err)
			}
			if got := filepath.Ext(result.Path); got != tt.wantExt {
				t.Errorf("stored extension = %q, want %q (path %q)", got, tt.wantExt, result.Path)
			}
			if !strings.HasPrefix(result.Path, "media_attachments/files/1/1/") || strings.Contains(result.Path, "..") {
				t.Errorf("path %q escapes the account's directory", result.Path)
			}
		})
	}
}
