package workers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/media"
)

type stubMediaProcessingStore struct {
	attachment db.MediaAttachment
	getErr     error
	updateErr  error
	updates    []db.UpdateMediaProcessingParams
}

func (s *stubMediaProcessingStore) GetMediaAttachment(ctx context.Context, id int64) (db.MediaAttachment, error) {
	return s.attachment, s.getErr
}

func (s *stubMediaProcessingStore) UpdateMediaProcessing(ctx context.Context, arg db.UpdateMediaProcessingParams) (db.MediaAttachment, error) {
	s.updates = append(s.updates, arg)
	return s.attachment, s.updateErr
}

type memStorage struct {
	files map[string][]byte
}

func (m *memStorage) Save(ctx context.Context, path string, data []byte) error {
	if m.files == nil {
		m.files = make(map[string][]byte)
	}
	copied := make([]byte, len(data))
	copy(copied, data)
	m.files[path] = copied
	return nil
}

func (m *memStorage) Load(ctx context.Context, path string) ([]byte, error) {
	if m.files == nil {
		return nil, os.ErrNotExist
	}
	data, ok := m.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	copied := make([]byte, len(data))
	copy(copied, data)
	return copied, nil
}

func (m *memStorage) Delete(ctx context.Context, path string) error {
	if m.files != nil {
		delete(m.files, path)
	}
	return nil
}

func (m *memStorage) URL(path string) string {
	return "https://example.com/" + path
}

func (m *memStorage) Exists(ctx context.Context, path string) (bool, error) {
	_, ok := m.files[path]
	return ok, nil
}

func createTestImage(width, height int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 120, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func TestNewMediaProcessingTask(t *testing.T) {
	if _, err := NewMediaProcessingTask(0); !errors.Is(err, ErrMediaProcessingPayloadInvalid) {
		t.Fatalf("expected payload invalid error, got %v", err)
	}
	task, err := NewMediaProcessingTask(10)
	if err != nil {
		t.Fatalf("NewMediaProcessingTask error = %v", err)
	}
	var payload MediaProcessingPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		t.Fatalf("payload unmarshal error = %v", err)
	}
	if payload.MediaID != 10 {
		t.Fatalf("payload.MediaID = %d, want 10", payload.MediaID)
	}
}

func TestMediaProcessingHandlerProcessesImage(t *testing.T) {
	createdAt := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	attachment := db.MediaAttachment{
		ID:              10,
		RemoteUrl:       "",
		AccountID:       pgtype.Int8{Int64: 42, Valid: true},
		CreatedAt:       pgtype.Timestamptz{Time: createdAt, Valid: true},
		FileFileName:    pgtype.Text{String: "image.png", Valid: true},
		FileContentType: pgtype.Text{String: "image/png", Valid: true},
		FileMeta:        []byte(`{"focus":{"x":0.5,"y":-0.25}}`),
	}
	store := &stubMediaProcessingStore{attachment: attachment}
	storage := &memStorage{}

	originalPath := media.MediaStoragePath(attachment.AccountID.Int64, attachment.CreatedAt.Time, attachment.FileFileName.String)
	imageData := createTestImage(800, 600)
	if err := storage.Save(context.Background(), originalPath, imageData); err != nil {
		t.Fatalf("Save original error = %v", err)
	}

	handler := &MediaProcessingHandler{store: store, storage: storage}
	task, err := NewMediaProcessingTask(attachment.ID)
	if err != nil {
		t.Fatalf("NewMediaProcessingTask error = %v", err)
	}
	if err := handler.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("ProcessTask error = %v", err)
	}

	if len(store.updates) < 2 {
		t.Fatalf("updates = %d, want >= 2", len(store.updates))
	}
	last := store.updates[len(store.updates)-1]
	if !last.Processing.Valid || last.Processing.Int32 != media.ProcessingComplete {
		t.Fatalf("processing = %#v, want complete", last.Processing)
	}
	if len(last.FileMeta) == 0 {
		t.Fatal("FileMeta should be populated")
	}
	var meta media.FileMeta
	if err := json.Unmarshal(last.FileMeta, &meta); err != nil {
		t.Fatalf("unmarshal FileMeta error = %v", err)
	}
	if meta.Focus == nil || meta.Focus.X != 0.5 || meta.Focus.Y != -0.25 {
		t.Fatalf("focus = %#v, want x=0.5 y=-0.25", meta.Focus)
	}
	if !last.ThumbnailFileName.Valid || last.ThumbnailFileName.String == "" {
		t.Fatal("ThumbnailFileName should be set")
	}
	thumbPath := media.GenerateThumbnailPath(originalPath)
	exists, _ := storage.Exists(context.Background(), thumbPath)
	if !exists {
		t.Fatalf("thumbnail not saved at %s", thumbPath)
	}
}

func TestMediaProcessingHandlerMissingFile(t *testing.T) {
	attachment := db.MediaAttachment{
		ID:              11,
		RemoteUrl:       "",
		AccountID:       pgtype.Int8{Int64: 1, Valid: true},
		CreatedAt:       pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		FileFileName:    pgtype.Text{String: "missing.png", Valid: true},
		FileContentType: pgtype.Text{String: "image/png", Valid: true},
	}
	store := &stubMediaProcessingStore{attachment: attachment}
	handler := &MediaProcessingHandler{store: store, storage: &memStorage{}}
	task, _ := NewMediaProcessingTask(attachment.ID)

	err := handler.ProcessTask(context.Background(), task)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("ProcessTask error = %v, want SkipRetry", err)
	}
	last := store.updates[len(store.updates)-1]
	if !last.Processing.Valid || last.Processing.Int32 != media.ProcessingFailed {
		t.Fatalf("processing = %#v, want failed", last.Processing)
	}
}

func TestMediaProcessingHandlerSkipsRemote(t *testing.T) {
	attachment := db.MediaAttachment{
		ID:        12,
		RemoteUrl: "https://remote.example/media.png",
	}
	store := &stubMediaProcessingStore{attachment: attachment}
	handler := &MediaProcessingHandler{store: store, storage: &memStorage{}}
	task, _ := NewMediaProcessingTask(attachment.ID)

	if err := handler.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("ProcessTask error = %v", err)
	}
	last := store.updates[len(store.updates)-1]
	if !last.Processing.Valid || last.Processing.Int32 != media.ProcessingComplete {
		t.Fatalf("processing = %#v, want complete", last.Processing)
	}
}
