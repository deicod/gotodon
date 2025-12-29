package workers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/media"
)

const (
	TaskTypeMediaProcessing = "media:process"

	defaultMediaProcessingMaxRetry = 2
	defaultMediaProcessingTimeout  = 2 * time.Minute
)

var (
	ErrMediaProcessingPayloadInvalid     = errors.New("media processing payload invalid")
	ErrMediaProcessingEnqueuerMissing    = errors.New("media processing enqueuer missing")
	ErrMediaProcessingHandlerMissingDeps = errors.New("media processing handler dependencies missing")
)

// MediaProcessingPayload is the payload for media processing jobs.
type MediaProcessingPayload struct {
	MediaID int64 `json:"media_id"`
}

// MediaProcessingStore loads and updates media attachments.
type MediaProcessingStore interface {
	GetMediaAttachment(ctx context.Context, id int64) (db.MediaAttachment, error)
	UpdateMediaProcessing(ctx context.Context, arg db.UpdateMediaProcessingParams) (db.MediaAttachment, error)
}

// MediaProcessingRegistrar registers the media processing job handler.
type MediaProcessingRegistrar struct {
	Store   MediaProcessingStore
	Storage media.Storage
}

// Register wires the media processing job handler into the worker manager.
func (r *MediaProcessingRegistrar) Register(m *Manager) error {
	if r == nil || r.Store == nil || r.Storage == nil {
		return ErrMediaProcessingHandlerMissingDeps
	}
	if m == nil {
		return ErrRegistrarNil
	}
	handler := &MediaProcessingHandler{
		store:   r.Store,
		storage: r.Storage,
	}
	return m.Handle(TaskTypeMediaProcessing, handler)
}

// NewMediaProcessingTask builds a task to process a media attachment.
func NewMediaProcessingTask(mediaID int64) (*asynq.Task, error) {
	if mediaID <= 0 {
		return nil, ErrMediaProcessingPayloadInvalid
	}
	payload := MediaProcessingPayload{MediaID: mediaID}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return asynq.NewTask(TaskTypeMediaProcessing, body), nil
}

// MediaProcessingEnqueuer enqueues media processing jobs.
type MediaProcessingEnqueuer struct {
	client   *asynq.Client
	queue    string
	maxRetry int
	timeout  time.Duration
}

// NewMediaProcessingEnqueuer creates an enqueuer with defaults.
func NewMediaProcessingEnqueuer(client *asynq.Client) *MediaProcessingEnqueuer {
	return &MediaProcessingEnqueuer{
		client:   client,
		queue:    QueueDefault,
		maxRetry: defaultMediaProcessingMaxRetry,
		timeout:  defaultMediaProcessingTimeout,
	}
}

// Enqueue schedules a media processing job.
func (e *MediaProcessingEnqueuer) Enqueue(ctx context.Context, mediaID int64) error {
	if e == nil || e.client == nil {
		return ErrMediaProcessingEnqueuerMissing
	}
	task, err := NewMediaProcessingTask(mediaID)
	if err != nil {
		return err
	}
	_, err = e.client.Enqueue(task,
		asynq.Queue(e.queue),
		asynq.MaxRetry(e.maxRetry),
		asynq.Timeout(e.timeout),
	)
	return err
}

// MediaProcessingHandler processes media attachments.
type MediaProcessingHandler struct {
	store   MediaProcessingStore
	storage media.Storage
}

// ProcessTask loads a media attachment, generates metadata, blurhash, and thumbnail.
func (h *MediaProcessingHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	if task == nil || h == nil || h.store == nil || h.storage == nil {
		return ErrMediaProcessingHandlerMissingDeps
	}

	var payload MediaProcessingPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("decode payload: %w", asynq.SkipRetry)
	}
	if payload.MediaID <= 0 {
		return fmt.Errorf("invalid media id: %w", asynq.SkipRetry)
	}

	attachment, err := h.store.GetMediaAttachment(ctx, payload.MediaID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("media missing: %w", asynq.SkipRetry)
		}
		return fmt.Errorf("load media: %w", err)
	}

	if attachment.RemoteUrl != "" {
		return h.markProcessingComplete(ctx, attachment.ID)
	}

	if !attachment.AccountID.Valid || !attachment.CreatedAt.Valid || !attachment.FileFileName.Valid {
		return h.markProcessingFailed(ctx, attachment.ID, "missing storage fields")
	}

	contentType := ""
	if attachment.FileContentType.Valid {
		contentType = strings.TrimSpace(attachment.FileContentType.String)
	}
	mediaType := media.DetectType(contentType)
	if contentType != "" && mediaType != media.MediaTypeImage && mediaType != media.MediaTypeGifv {
		return h.markProcessingComplete(ctx, attachment.ID)
	}

	if err := h.setProcessing(ctx, attachment.ID, media.ProcessingInProgress); err != nil {
		return fmt.Errorf("mark in progress: %w", err)
	}

	storagePath := mediaStoragePath(attachment.AccountID.Int64, attachment.CreatedAt.Time, attachment.FileFileName.String)
	if storagePath == "" {
		return h.markProcessingFailed(ctx, attachment.ID, "storage path missing")
	}

	data, err := h.storage.Load(ctx, storagePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			altPath := mediaStoragePath(attachment.AccountID.Int64, attachment.CreatedAt.Time.In(time.Local), attachment.FileFileName.String)
			if altPath != "" && altPath != storagePath {
				if altData, altErr := h.storage.Load(ctx, altPath); altErr == nil {
					data = altData
					storagePath = altPath
				} else if !errors.Is(altErr, os.ErrNotExist) {
					return fmt.Errorf("load media file: %w", altErr)
				}
			}
			if data == nil {
				return h.markProcessingFailed(ctx, attachment.ID, "media file missing")
			}
		} else {
			return fmt.Errorf("load media file: %w", err)
		}
	}

	if contentType == "" {
		contentType = media.DetectContentType(data)
		mediaType = media.DetectType(contentType)
		if mediaType != media.MediaTypeImage && mediaType != media.MediaTypeGifv {
			return h.markProcessingComplete(ctx, attachment.ID)
		}
	}

	meta, err := media.ProcessImage(data)
	if err != nil {
		return h.markProcessingFailed(ctx, attachment.ID, "invalid image")
	}

	meta = mergeFocus(meta, attachment.FileMeta)

	thumb, err := media.GenerateThumbnail(data, contentType)
	if err != nil {
		return h.markProcessingFailed(ctx, attachment.ID, "thumbnail generation failed")
	}

	if meta != nil {
		meta.Small.Width = thumb.Width
		meta.Small.Height = thumb.Height
		meta.Small.Size = fmt.Sprintf("%dx%d", thumb.Width, thumb.Height)
		meta.Small.Aspect = meta.Original.Aspect
	}

	thumbPath := media.GenerateThumbnailPath(storagePath)
	if err := h.storage.Save(ctx, thumbPath, thumb.Data); err != nil {
		return fmt.Errorf("save thumbnail: %w", err)
	}

	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}

	var blurhash pgtype.Text
	if hash, err := media.BlurHashFromBytes(data, 4, 3); err == nil && hash != "" {
		blurhash = pgtype.Text{String: hash, Valid: true}
	}

	_, err = h.store.UpdateMediaProcessing(ctx, db.UpdateMediaProcessingParams{
		ID:                   attachment.ID,
		FileMeta:             metaBytes,
		Blurhash:             blurhash,
		Processing:           pgtype.Int4{Int32: media.ProcessingComplete, Valid: true},
		ThumbnailFileName:    pgtype.Text{String: filepath.Base(thumbPath), Valid: true},
		ThumbnailContentType: pgtype.Text{String: thumb.ContentType, Valid: true},
		ThumbnailFileSize:    pgtype.Int4{Int32: int32(len(thumb.Data)), Valid: true},
	})
	if err != nil {
		return fmt.Errorf("update media: %w", err)
	}

	return nil
}

func (h *MediaProcessingHandler) setProcessing(ctx context.Context, mediaID int64, status int32) error {
	_, err := h.store.UpdateMediaProcessing(ctx, db.UpdateMediaProcessingParams{
		ID:         mediaID,
		Processing: pgtype.Int4{Int32: status, Valid: true},
	})
	return err
}

func (h *MediaProcessingHandler) markProcessingComplete(ctx context.Context, mediaID int64) error {
	if err := h.setProcessing(ctx, mediaID, media.ProcessingComplete); err != nil {
		return fmt.Errorf("mark complete: %w", err)
	}
	return nil
}

func (h *MediaProcessingHandler) markProcessingFailed(ctx context.Context, mediaID int64, message string) error {
	if err := h.setProcessing(ctx, mediaID, media.ProcessingFailed); err != nil {
		return fmt.Errorf("mark failed: %w", err)
	}
	return fmt.Errorf("%s: %w", message, asynq.SkipRetry)
}

func mergeFocus(meta *media.FileMeta, existing []byte) *media.FileMeta {
	if meta == nil || len(existing) == 0 {
		return meta
	}
	var prior media.FileMeta
	if err := json.Unmarshal(existing, &prior); err != nil {
		return meta
	}
	if prior.Focus != nil {
		meta.Focus = prior.Focus
	}
	return meta
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
