package workers

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/services"
)

type stubLinkCrawlerStore struct {
	status    db.Status
	statusErr error
	cards     map[string]db.PreviewCard
	attached  []db.AttachPreviewCardToStatusParams
}

func (s *stubLinkCrawlerStore) GetStatusByID(ctx context.Context, id int64) (db.Status, error) {
	if s.statusErr != nil {
		return db.Status{}, s.statusErr
	}
	return s.status, nil
}

func (s *stubLinkCrawlerStore) GetPreviewCardByURL(ctx context.Context, cardURL string) (db.PreviewCard, error) {
	if s.cards == nil {
		return db.PreviewCard{}, pgx.ErrNoRows
	}
	if card, ok := s.cards[cardURL]; ok {
		return card, nil
	}
	return db.PreviewCard{}, pgx.ErrNoRows
}

func (s *stubLinkCrawlerStore) CreatePreviewCard(ctx context.Context, arg db.CreatePreviewCardParams) (db.PreviewCard, error) {
	return db.PreviewCard{ID: 1, Url: arg.Url}, nil
}

func (s *stubLinkCrawlerStore) AttachPreviewCardToStatus(ctx context.Context, arg db.AttachPreviewCardToStatusParams) error {
	s.attached = append(s.attached, arg)
	return nil
}

func (s *stubLinkCrawlerStore) GetPreviewCardProviderByDomain(ctx context.Context, domain string) (db.PreviewCardProvider, error) {
	return db.PreviewCardProvider{}, pgx.ErrNoRows
}

func (s *stubLinkCrawlerStore) CreatePreviewCardProvider(ctx context.Context, arg db.CreatePreviewCardProviderParams) (db.PreviewCardProvider, error) {
	return db.PreviewCardProvider{ID: 1, Domain: arg.Domain}, nil
}

type noopLinkStorage struct{}

func (n *noopLinkStorage) Save(ctx context.Context, path string, data []byte) error {
	return nil
}

func (n *noopLinkStorage) Load(ctx context.Context, path string) ([]byte, error) {
	return nil, os.ErrNotExist
}

func (n *noopLinkStorage) Delete(ctx context.Context, path string) error {
	return nil
}

func (n *noopLinkStorage) URL(path string) string {
	return "https://example.com/" + path
}

func (n *noopLinkStorage) Exists(ctx context.Context, path string) (bool, error) {
	return false, nil
}

func TestNewLinkCrawlerTask(t *testing.T) {
	if _, err := NewLinkCrawlerTask(0, "https://example.com"); !errors.Is(err, ErrLinkCrawlerPayloadInvalid) {
		t.Fatalf("missing status id error = %v", err)
	}
	if _, err := NewLinkCrawlerTask(1, ""); !errors.Is(err, ErrLinkCrawlerPayloadInvalid) {
		t.Fatalf("missing url error = %v", err)
	}

	task, err := NewLinkCrawlerTask(10, "https://example.com")
	if err != nil {
		t.Fatalf("NewLinkCrawlerTask error = %v", err)
	}
	var payload LinkCrawlerPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		t.Fatalf("payload unmarshal error = %v", err)
	}
	if payload.StatusID != 10 || payload.URL != "https://example.com" {
		t.Fatalf("payload = %#v, want status_id 10", payload)
	}
}

func TestLinkCrawlerEnqueuerValidatesClient(t *testing.T) {
	enqueuer := NewLinkCrawlerEnqueuer(nil)
	if err := enqueuer.Enqueue(context.Background(), 1, "https://example.com"); !errors.Is(err, ErrLinkCrawlerEnqueuerMissing) {
		t.Fatalf("missing client error = %v", err)
	}
}

func TestLinkCrawlerHandlerCallsCrawler(t *testing.T) {
	link := "https://news.example/example"
	store := &stubLinkCrawlerStore{
		status: db.Status{ID: 5, Local: pgtype.Bool{Bool: true, Valid: true}},
		cards: map[string]db.PreviewCard{
			link: {ID: 33, Url: link},
		},
	}
	crawler := services.NewLinkCrawler(store, &noopLinkStorage{})
	handler := &LinkCrawlerHandler{crawler: crawler}
	task, err := NewLinkCrawlerTask(5, link)
	if err != nil {
		t.Fatalf("NewLinkCrawlerTask error = %v", err)
	}
	if err := handler.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("ProcessTask error = %v", err)
	}
	if len(store.attached) != 1 {
		t.Fatalf("attached = %d, want 1", len(store.attached))
	}
	if store.attached[0].PreviewCardID != 33 {
		t.Fatalf("PreviewCardID = %d, want 33", store.attached[0].PreviewCardID)
	}
	if !store.attached[0].Url.Valid || store.attached[0].Url.String != link {
		t.Fatalf("Url = %#v, want %s", store.attached[0].Url, link)
	}
}

func TestLinkCrawlerHandlerSkipsInvalidPayload(t *testing.T) {
	store := &stubLinkCrawlerStore{status: db.Status{ID: 1, Local: pgtype.Bool{Bool: true, Valid: true}}}
	crawler := services.NewLinkCrawler(store, &noopLinkStorage{})
	handler := &LinkCrawlerHandler{crawler: crawler}
	task := asynq.NewTask(TaskTypeLinkCrawler, []byte("bad"))
	err := handler.ProcessTask(context.Background(), task)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("ProcessTask error = %v, want SkipRetry", err)
	}
}

func TestLinkCrawlerHandlerSkipsRetryOnCrawlerSkip(t *testing.T) {
	store := &stubLinkCrawlerStore{
		status: db.Status{ID: 2, Local: pgtype.Bool{Bool: false, Valid: true}},
	}
	crawler := services.NewLinkCrawler(store, &noopLinkStorage{})
	handler := &LinkCrawlerHandler{crawler: crawler}
	task, err := NewLinkCrawlerTask(2, "http://127.0.0.1/example")
	if err != nil {
		t.Fatalf("NewLinkCrawlerTask error = %v", err)
	}
	err = handler.ProcessTask(context.Background(), task)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("ProcessTask error = %v, want SkipRetry", err)
	}
}

func TestLinkCrawlerHandlerSkipsRetryForInternalLink(t *testing.T) {
	store := &stubLinkCrawlerStore{
		status: db.Status{ID: 3, Local: pgtype.Bool{Bool: true, Valid: true}},
	}
	handler := &LinkCrawlerHandler{crawler: services.NewLinkCrawler(store, &noopLinkStorage{})}
	task, err := NewLinkCrawlerTask(3, "http://169.254.169.254/latest/meta-data/")
	if err != nil {
		t.Fatalf("NewLinkCrawlerTask error = %v", err)
	}
	err = handler.ProcessTask(context.Background(), task)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("ProcessTask error = %v, want SkipRetry", err)
	}
	if len(store.attached) != 0 {
		t.Fatalf("attached = %d, want 0", len(store.attached))
	}
}
