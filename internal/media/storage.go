// Package media provides file storage abstraction.
package media

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Storage is the interface for media file storage
type Storage interface {
	Save(ctx context.Context, path string, data []byte) error
	Load(ctx context.Context, path string) ([]byte, error)
	Delete(ctx context.Context, path string) error
	URL(path string) string
	Exists(ctx context.Context, path string) (bool, error)
}

// LocalStorage implements Storage using the local filesystem
type LocalStorage struct {
	BasePath string
	BaseURL  string
}

// NewLocalStorage creates a new local storage instance
func NewLocalStorage(basePath, baseURL string) *LocalStorage {
	return &LocalStorage{
		BasePath: basePath,
		BaseURL:  baseURL,
	}
}

// Save saves data to a file
func (s *LocalStorage) Save(_ context.Context, path string, data []byte) error {
	fullPath := filepath.Join(s.BasePath, path)

	// Create directories if needed
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}

	// Write file
	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		return fmt.Errorf("writing file %s: %w", fullPath, err)
	}

	return nil
}

// Load loads data from a file
func (s *LocalStorage) Load(_ context.Context, path string) ([]byte, error) {
	fullPath := filepath.Join(s.BasePath, path)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("reading file %s: %w", fullPath, err)
	}
	return data, nil
}

// Delete deletes a file
func (s *LocalStorage) Delete(_ context.Context, path string) error {
	fullPath := filepath.Join(s.BasePath, path)
	if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deleting file %s: %w", fullPath, err)
	}
	return nil
}

// URL returns the public URL for a file
func (s *LocalStorage) URL(path string) string {
	return s.BaseURL + "/" + path
}

// Exists checks if a file exists
func (s *LocalStorage) Exists(_ context.Context, path string) (bool, error) {
	fullPath := filepath.Join(s.BasePath, path)
	_, err := os.Stat(fullPath)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// GenerateStoragePath generates a storage path for a media file
// Format: media_attachments/files/<account_id>/<date>/<random>.<ext>
func GenerateStoragePath(accountID int64, filename string) string {
	now := time.Now()
	ext := filepath.Ext(filename)
	randomName := GenerateFilename(filename)

	return filepath.Join(
		"media_attachments",
		"files",
		fmt.Sprintf("%d", accountID%1000),
		fmt.Sprintf("%d", accountID),
		now.Format("2006/01/02"),
		randomName[:len(randomName)-len(filepath.Ext(randomName))]+ext,
	)
}

// GenerateThumbnailPath generates a storage path for a thumbnail
func GenerateThumbnailPath(originalPath string) string {
	dir := filepath.Dir(originalPath)
	base := filepath.Base(originalPath)
	ext := filepath.Ext(base)
	name := base[:len(base)-len(ext)]
	return filepath.Join(dir, name+"_thumb"+ext)
}

// Uploader handles media uploads
type Uploader struct {
	storage Storage
}

// UploadOptions controls upload processing behavior.
type UploadOptions struct {
	SkipProcessing bool
}

// NewUploader creates a new uploader
func NewUploader(storage Storage) *Uploader {
	return &Uploader{storage: storage}
}

// Upload processes and stores an uploaded file
func (u *Uploader) Upload(ctx context.Context, accountID int64, filename string, data []byte) (*UploadResult, error) {
	return u.UploadWithOptions(ctx, accountID, filename, data, UploadOptions{})
}

// UploadWithOptions processes and stores an uploaded file with options.
func (u *Uploader) UploadWithOptions(ctx context.Context, accountID int64, filename string, data []byte, opts UploadOptions) (*UploadResult, error) {
	// Detect content type
	contentType := DetectContentType(data)

	// Validate type
	if err := ValidateType(contentType); err != nil {
		return nil, err
	}

	mediaType := DetectType(contentType)

	// Validate size
	if err := ValidateSize(int64(len(data)), mediaType); err != nil {
		return nil, err
	}

	// Process based on type
	var meta *FileMeta
	var blurhash string
	var err error

	if !opts.SkipProcessing && (mediaType == MediaTypeImage || mediaType == MediaTypeGifv) {
		meta, err = ProcessImage(data)
		if err != nil {
			return nil, err
		}

		// Generate blurhash
		var errHash error
		blurhash, errHash = BlurHashFromBytes(data, 4, 3)
		if errHash != nil {
			fmt.Printf("Failed to generate blurhash: %v\n", errHash)
		}
	}

	// Generate storage path
	storagePath := GenerateStoragePath(accountID, filename)

	// Save file
	if err := u.storage.Save(ctx, storagePath, data); err != nil {
		return nil, fmt.Errorf("saving file: %w", err)
	}

	return &UploadResult{
		Path:        storagePath,
		URL:         u.storage.URL(storagePath),
		ContentType: contentType,
		Size:        int64(len(data)),
		MediaType:   mediaType,
		Meta:        meta,
		Blurhash:    blurhash,
	}, nil
}

// UploadResult contains the result of an upload
type UploadResult struct {
	Path        string
	URL         string
	ContentType string
	Size        int64
	MediaType   MediaType
	Meta        *FileMeta
	Blurhash    string
}

// CopyToStorage copies data from a reader to storage
func (u *Uploader) CopyToStorage(ctx context.Context, accountID int64, filename string, r io.Reader, maxSize int64) (*UploadResult, error) {
	// Read with size limit
	limited := io.LimitReader(r, maxSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("reading upload: %w", err)
	}

	if int64(len(data)) > maxSize {
		return nil, ErrFileTooLarge
	}

	return u.Upload(ctx, accountID, filename, data)
}
