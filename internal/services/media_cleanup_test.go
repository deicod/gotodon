package services

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
)

type stubMediaCleanupStore struct {
	attachments []db.MediaAttachment
	listCalls   int
	deleted     []int64
}

func (s *stubMediaCleanupStore) ListOrphanedMediaAttachments(ctx context.Context, arg db.ListOrphanedMediaAttachmentsParams) ([]db.MediaAttachment, error) {
	s.listCalls++
	if s.listCalls == 1 {
		return s.attachments, nil
	}
	return nil, nil
}

func (s *stubMediaCleanupStore) DeleteMediaAttachmentByID(ctx context.Context, id int64) error {
	s.deleted = append(s.deleted, id)
	return nil
}

type stubCleanupStorage struct {
	deleted []string
	errors  map[string]error
}

func (s *stubCleanupStorage) Save(ctx context.Context, path string, data []byte) error {
	return nil
}

func (s *stubCleanupStorage) Load(ctx context.Context, path string) ([]byte, error) {
	return nil, os.ErrNotExist
}

func (s *stubCleanupStorage) Delete(ctx context.Context, path string) error {
	s.deleted = append(s.deleted, path)
	if err, ok := s.errors[path]; ok {
		return err
	}
	return nil
}

func (s *stubCleanupStorage) URL(path string) string {
	return "https://example.com/" + path
}

func (s *stubCleanupStorage) Exists(ctx context.Context, path string) (bool, error) {
	return false, nil
}

func TestMediaCleanupServiceRemovesOrphanedAttachments(t *testing.T) {
	createdAt := time.Date(2024, 3, 2, 10, 11, 12, 0, time.UTC)
	attachment := db.MediaAttachment{
		ID:                10,
		AccountID:         pgtype.Int8{Int64: 4242, Valid: true},
		CreatedAt:         pgtype.Timestamptz{Time: createdAt, Valid: true},
		FileFileName:      pgtype.Text{String: "file.png", Valid: true},
		ThumbnailFileName: pgtype.Text{String: "file_thumb.png", Valid: true},
	}
	store := &stubMediaCleanupStore{attachments: []db.MediaAttachment{attachment}}
	storage := &stubCleanupStorage{}

	svc := NewMediaCleanupService(store, storage, nil)
	svc.now = func() time.Time { return time.Unix(1700000000, 0) }

	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("Run error = %v", err)
	}

	if len(store.deleted) != 1 || store.deleted[0] != 10 {
		t.Fatalf("deleted = %#v, want [10]", store.deleted)
	}
	if len(storage.deleted) != 2 {
		t.Fatalf("storage.deleted = %d, want 2", len(storage.deleted))
	}
	expectedFile := mediaStoragePath(attachment.AccountID.Int64, attachment.CreatedAt.Time, attachment.FileFileName.String)
	expectedThumb := mediaStoragePath(attachment.AccountID.Int64, attachment.CreatedAt.Time, attachment.ThumbnailFileName.String)
	if storage.deleted[0] != expectedFile || storage.deleted[1] != expectedThumb {
		t.Fatalf("storage.deleted = %#v, want [%s %s]", storage.deleted, expectedFile, expectedThumb)
	}
}

func TestMediaCleanupSkipsDeleteOnStorageError(t *testing.T) {
	createdAt := time.Date(2024, 3, 2, 10, 11, 12, 0, time.UTC)
	attachment := db.MediaAttachment{
		ID:           11,
		AccountID:    pgtype.Int8{Int64: 4242, Valid: true},
		CreatedAt:    pgtype.Timestamptz{Time: createdAt, Valid: true},
		FileFileName: pgtype.Text{String: "bad.png", Valid: true},
	}
	store := &stubMediaCleanupStore{attachments: []db.MediaAttachment{attachment}}
	badPath := mediaStoragePath(attachment.AccountID.Int64, attachment.CreatedAt.Time, attachment.FileFileName.String)
	storage := &stubCleanupStorage{errors: map[string]error{badPath: errors.New("boom")}}

	svc := NewMediaCleanupService(store, storage, nil)
	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if len(store.deleted) != 0 {
		t.Fatalf("deleted = %#v, want empty", store.deleted)
	}
}
