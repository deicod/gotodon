package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/media"
)

const (
	mediaCleanupOrphanTTL = 24 * time.Hour
	mediaCleanupBatchSize = 100
)

var ErrMediaCleanupMissingDeps = errors.New("media cleanup missing dependencies")

// MediaCleanupStore defines DB access for media cleanup.
type MediaCleanupStore interface {
	ListOrphanedMediaAttachments(ctx context.Context, arg db.ListOrphanedMediaAttachmentsParams) ([]db.MediaAttachment, error)
	DeleteMediaAttachmentByID(ctx context.Context, id int64) error
}

// MediaCleanupService removes orphaned media attachments.
type MediaCleanupService struct {
	store     MediaCleanupStore
	storage   media.Storage
	logger    *slog.Logger
	now       func() time.Time
	orphanTTL time.Duration
	batchSize int32
}

// NewMediaCleanupService constructs a media cleanup service with defaults.
func NewMediaCleanupService(store MediaCleanupStore, storage media.Storage, logger *slog.Logger) *MediaCleanupService {
	if logger == nil {
		logger = slog.Default()
	}
	return &MediaCleanupService{
		store:     store,
		storage:   storage,
		logger:    logger,
		now:       time.Now,
		orphanTTL: mediaCleanupOrphanTTL,
		batchSize: mediaCleanupBatchSize,
	}
}

// Run removes orphaned media attachments in batches.
func (s *MediaCleanupService) Run(ctx context.Context) error {
	if s == nil || s.store == nil {
		return ErrMediaCleanupMissingDeps
	}
	batchSize := s.batchSize
	if batchSize <= 0 {
		batchSize = mediaCleanupBatchSize
	}
	orphanTTL := s.orphanTTL
	if orphanTTL <= 0 {
		orphanTTL = mediaCleanupOrphanTTL
	}

	before := s.now().Add(-orphanTTL)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		attachments, err := s.store.ListOrphanedMediaAttachments(ctx, db.ListOrphanedMediaAttachmentsParams{
			Before: pgtype.Timestamptz{Time: before, Valid: true},
			Limit:  batchSize,
		})
		if err != nil {
			return err
		}
		if len(attachments) == 0 {
			return nil
		}
		for _, attachment := range attachments {
			if err := s.cleanupAttachment(ctx, attachment); err != nil {
				s.logger.Error("Failed to cleanup media attachment", "media_id", attachment.ID, "error", err)
			}
		}
	}
}

func (s *MediaCleanupService) cleanupAttachment(ctx context.Context, attachment db.MediaAttachment) error {
	if s.storage != nil {
		if err := s.deleteStoredFile(ctx, attachment, attachment.FileFileName); err != nil {
			return err
		}
		if err := s.deleteStoredFile(ctx, attachment, attachment.ThumbnailFileName); err != nil {
			return err
		}
	}
	return s.store.DeleteMediaAttachmentByID(ctx, attachment.ID)
}

func (s *MediaCleanupService) deleteStoredFile(ctx context.Context, attachment db.MediaAttachment, filename pgtype.Text) error {
	if !filename.Valid {
		return nil
	}
	paths := attachmentStoragePaths(attachment, filename.String)
	if len(paths) == 0 {
		return nil
	}
	if err := s.storage.Delete(ctx, paths[0]); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		for _, path := range paths[1:] {
			if path == "" || path == paths[0] {
				continue
			}
			if err := s.storage.Delete(ctx, path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func attachmentStoragePaths(attachment db.MediaAttachment, filename string) []string {
	name := strings.TrimSpace(filename)
	if name == "" {
		return nil
	}
	if hasPathSeparator(name) {
		return []string{name}
	}
	if attachment.AccountID.Valid && attachment.AccountID.Int64 > 0 && attachment.CreatedAt.Valid {
		primary := mediaStoragePath(attachment.AccountID.Int64, attachment.CreatedAt.Time, name)
		if primary == "" {
			return []string{name}
		}
		alt := mediaStoragePath(attachment.AccountID.Int64, attachment.CreatedAt.Time.In(time.Local), name)
		if alt == primary {
			return []string{primary}
		}
		return []string{primary, alt}
	}
	return []string{name}
}

func hasPathSeparator(value string) bool {
	return strings.ContainsRune(value, '/') || strings.ContainsRune(value, '\\')
}

func mediaStoragePath(accountID int64, createdAt time.Time, filename string) string {
	if accountID <= 0 || filename == "" {
		return ""
	}
	return filepath.Join(
		"media_attachments",
		"files",
		fmt.Sprintf("%d", accountID%1000),
		fmt.Sprintf("%d", accountID),
		createdAt.Format("2006/01/02"),
		filename,
	)
}
