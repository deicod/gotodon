package media

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalStorage(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "gotodon-storage-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	storage := NewLocalStorage(tmpDir, "https://example.com/files")
	ctx := context.Background()

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
		path := "exists/test.txt"
		_ = storage.Save(ctx, path, []byte("test"))

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

func TestGenerateStoragePath(t *testing.T) {
	path := GenerateStoragePath(12345, "photo.jpg")

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

	t.Run("Reject unsupported type", func(t *testing.T) {
		data := []byte("not an image")
		_, err := uploader.Upload(ctx, 12345, "test.txt", data)
		if err != ErrUnsupportedType {
			t.Errorf("Should reject unsupported type, got error: %v", err)
		}
	})
}
