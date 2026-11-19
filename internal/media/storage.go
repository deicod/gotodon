// Package media provides file storage abstraction.
package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/deicod/gotodon/internal/config"
)

// Storage is the interface for media file storage
type Storage interface {
	Save(ctx context.Context, path string, data []byte) error
	Load(ctx context.Context, path string) ([]byte, error)
	Delete(ctx context.Context, path string) error
	// URL returns the absolute public URL of a stored path. Callers use
	// FileURL, which also passes absolute values through.
	URL(path string) string
	Exists(ctx context.Context, path string) (bool, error)
}

// FilesRoute is the path gotodon serves the local storage root at, and the
// path of the default base URL.
const FilesRoute = "/files"

// Paths of the images accounts without an avatar or header show. gotodon
// serves them at these paths on the web domain, as Mastodon does.
const (
	MissingAvatarPath = "/avatars/original/missing.png"
	MissingHeaderPath = "/headers/original/missing.png"
)

// NewStorage returns the storage cfg configures: the local storage path,
// served at media.base_url or, by default, at https://<web domain>/files.
// A nil cfg means the default storage path and no domain.
func NewStorage(cfg *config.Config) *LocalStorage {
	if cfg == nil {
		cfg = &config.Config{}
	}
	root := strings.TrimSpace(cfg.Media.StoragePath)
	if root == "" {
		root = config.DefaultMediaStoragePath
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.Media.BaseURL), "/")
	if baseURL == "" {
		baseURL = SiteURL(cfg) + FilesRoute
	}
	return NewLocalStorage(root, baseURL)
}

// SiteURL returns the root URL of the web domain, where gotodon serves its
// own routes. There is no fallback domain: without a configured one the URL
// has no host, and serve refuses to start.
func SiteURL(cfg *config.Config) string {
	return "https://" + cfg.PublicWebDomain()
}

// MissingAvatarURL returns the absolute URL of the default avatar.
func MissingAvatarURL(cfg *config.Config) string {
	return SiteURL(cfg) + MissingAvatarPath
}

// MissingHeaderURL returns the absolute URL of the default header.
func MissingHeaderURL(cfg *config.Config) string {
	return SiteURL(cfg) + MissingHeaderPath
}

// FileURL turns a stored file reference into the URL clients fetch it at. It
// is the only way to build such a URL. A value that is already an absolute
// http or https URL, such as a remote avatar or image_remote_url, is returned
// unchanged. Any other non-empty value is a path inside storage and becomes
// storage.URL of it. Empty values, and paths without a storage, give "".
func FileURL(storage Storage, stored string) string {
	stored = strings.TrimSpace(stored)
	if stored == "" {
		return ""
	}
	if IsAbsoluteURL(stored) {
		return stored
	}
	if storage == nil {
		return ""
	}
	stored = strings.TrimLeft(stored, "/")
	if stored == "" {
		return ""
	}
	return storage.URL(stored)
}

// IsAbsoluteURL reports whether value starts with an http or https scheme.
func IsAbsoluteURL(value string) bool {
	for _, scheme := range []string{"http://", "https://"} {
		if len(value) >= len(scheme) && strings.EqualFold(value[:len(scheme)], scheme) {
			return true
		}
	}
	return false
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

// openRoot validates the file name before accessing the storage directory.
func (s *LocalStorage) openRoot(name string, create bool) (*os.Root, string, error) {
	// Stored media paths can use slash separators on any platform.
	local := filepath.FromSlash(name)
	if local == "." || !filepath.IsLocal(local) || filepath.Clean(local) != local || strings.IndexByte(local, 0) >= 0 {
		return nil, "", fmt.Errorf("invalid storage path %q: %w", name, os.ErrInvalid)
	}

	base := filepath.Clean(s.BasePath)
	root, err := os.OpenRoot(base)
	if create && errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(base, 0o755); err != nil {
			return nil, "", fmt.Errorf("creating storage directory %q: %w", base, err)
		}
		root, err = os.OpenRoot(base)
	}
	return root, local, err
}

// Save saves data to a file
func (s *LocalStorage) Save(_ context.Context, name string, data []byte) error {
	root, name, err := s.openRoot(name, true)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()

	dir := filepath.Dir(name)
	if err := root.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating directory %q: %w", dir, err)
	}

	if err := root.WriteFile(name, data, 0o644); err != nil {
		return fmt.Errorf("writing file %q: %w", name, err)
	}

	return nil
}

// Load loads data from a file
func (s *LocalStorage) Load(_ context.Context, name string) ([]byte, error) {
	root, name, err := s.openRoot(name, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()

	data, err := root.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("reading file %q: %w", name, err)
	}
	return data, nil
}

// Delete deletes a file
func (s *LocalStorage) Delete(_ context.Context, name string) error {
	root, name, err := s.openRoot(name, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()

	// Remove operates on a final symlink itself. Stat checks its target so
	// Delete rejects paths that resolve outside storage as the other methods do.
	if _, err := root.Stat(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("resolving file %q: %w", name, err)
	}
	if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("deleting file %q: %w", name, err)
	}
	return nil
}

// URL returns the public URL of a stored path: BaseURL followed by the path,
// each segment percent-encoded.
func (s *LocalStorage) URL(stored string) string {
	segments := strings.Split(strings.TrimLeft(filepath.ToSlash(stored), "/"), "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.TrimRight(s.BaseURL, "/") + "/" + strings.Join(segments, "/")
}

// Exists checks if a file exists
func (s *LocalStorage) Exists(_ context.Context, name string) (bool, error) {
	root, name, err := s.openRoot(name, false)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = root.Close() }()

	_, err = root.Stat(name)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// GenerateStoragePath generates a storage path for a file uploaded at the
// given time, which MediaStoragePath dates in UTC. A zero time means now.
// Format: media_attachments/files/<account_id%1000>/<account_id>/<yyyy/mm/dd>/<random>.<ext>
func GenerateStoragePath(accountID int64, filename string, at time.Time) string {
	if at.IsZero() {
		at = time.Now()
	}
	ext := filepath.Ext(filename)
	randomName := GenerateFilename(filename)

	return MediaStoragePath(accountID, at, randomName[:len(randomName)-len(filepath.Ext(randomName))]+ext)
}

// GenerateThumbnailPath generates a storage path for a thumbnail
func GenerateThumbnailPath(originalPath string) string {
	dir := filepath.Dir(originalPath)
	base := filepath.Base(originalPath)
	ext := filepath.Ext(base)
	name := base[:len(base)-len(ext)]
	return filepath.Join(dir, name+"_thumb"+ext)
}

// MediaStoragePath builds the storage path for a persisted media attachment
// file. The date directory is the attachment's created_at in UTC, which the
// upload also uses, so the path does not depend on the server's time zone.
func MediaStoragePath(accountID int64, createdAt time.Time, filename string) string {
	return mediaStoragePath(accountID, createdAt.UTC(), filename)
}

// MediaStoragePaths returns the paths a persisted attachment file may be
// stored at: MediaStoragePath first, then the path dated in the server's
// local time zone, which uploads used before paths were dated in UTC, when
// it differs.
func MediaStoragePaths(accountID int64, createdAt time.Time, filename string) []string {
	primary := MediaStoragePath(accountID, createdAt, filename)
	if primary == "" {
		return nil
	}
	paths := []string{primary}
	if legacy := mediaStoragePath(accountID, createdAt.In(time.Local), filename); legacy != primary {
		paths = append(paths, legacy)
	}
	return paths
}

func mediaStoragePath(accountID int64, createdAt time.Time, filename string) string {
	if accountID <= 0 || filename == "" || createdAt.IsZero() {
		return ""
	}
	return path.Join(
		"media_attachments",
		"files",
		fmt.Sprintf("%d", accountID%1000),
		fmt.Sprintf("%d", accountID),
		createdAt.Format("2006/01/02"),
		filename,
	)
}

// Uploader handles media uploads
type Uploader struct {
	storage Storage
}

// UploadOptions controls upload processing behavior.
type UploadOptions struct {
	SkipProcessing bool
	// At is the upload time that dates the storage path. Callers that derive
	// the path from a persisted created_at later must store this same time.
	// Zero means now.
	At time.Time
}

// NewUploader creates a new uploader
func NewUploader(storage Storage) *Uploader {
	return &Uploader{storage: storage}
}

// Storage returns the storage the uploader writes to, or nil for a nil
// uploader.
func (u *Uploader) Storage() Storage {
	if u == nil {
		return nil
	}
	return u.storage
}

// Upload processes and stores an uploaded file
func (u *Uploader) Upload(ctx context.Context, accountID int64, filename string, data []byte) (*UploadResult, error) {
	return u.UploadWithOptions(ctx, accountID, filename, data, UploadOptions{})
}

// Delete removes a stored media file by path.
func (u *Uploader) Delete(ctx context.Context, path string) error {
	if u == nil || u.storage == nil {
		return fmt.Errorf("media storage not configured")
	}
	if path == "" {
		return nil
	}
	return u.storage.Delete(ctx, path)
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

	// Generate storage path. The extension comes from the detected content
	// type, not the client's file name, so servers that type files by
	// extension serve them as what they are.
	storagePath := GenerateStoragePath(accountID, storedFilename(filename, contentType), opts.At)

	// Save file
	if err := u.storage.Save(ctx, storagePath, data); err != nil {
		return nil, fmt.Errorf("saving file: %w", err)
	}

	return &UploadResult{
		Path:        storagePath,
		URL:         FileURL(u.storage, storagePath),
		ContentType: contentType,
		Size:        int64(len(data)),
		MediaType:   mediaType,
		Meta:        meta,
		Blurhash:    blurhash,
	}, nil
}

// storedFilename returns filename with its extension replaced by the one for
// contentType.
func storedFilename(filename, contentType string) string {
	base := filepath.Base(filename)
	return strings.TrimSuffix(base, filepath.Ext(base)) + GetExtensionFromMIME(contentType)
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
