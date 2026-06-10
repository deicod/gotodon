package workers

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hibiken/asynq"

	"github.com/deicod/gotodon/internal/mailer"
)

type fakeMailService struct {
	mails   []mailer.Mail
	sendErr error
}

func (f *fakeMailService) RenderAndSend(ctx context.Context, mail mailer.Mail) error {
	f.mails = append(f.mails, mail)
	return f.sendErr
}

func TestMailDeliveryHandlerProcessTask(t *testing.T) {
	service := &fakeMailService{}
	handler := &MailDeliveryHandler{service: service}

	mail := mailer.Mail{
		Kind:   mailer.KindPasswordChanged,
		To:     "user@example.org",
		Locale: "en",
		Params: map[string]string{"key": "value"},
	}
	payload, err := json.Marshal(mail)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if err := handler.ProcessTask(context.Background(), asynq.NewTask(TaskTypeMailDeliver, payload)); err != nil {
		t.Fatalf("ProcessTask() error = %v", err)
	}
	if len(service.mails) != 1 {
		t.Fatalf("mails sent = %d, want 1", len(service.mails))
	}
	if service.mails[0].To != mail.To || service.mails[0].Kind != mail.Kind {
		t.Errorf("sent mail = %+v", service.mails[0])
	}
}

func TestMailDeliveryHandlerInvalidPayloadSkipsRetry(t *testing.T) {
	handler := &MailDeliveryHandler{service: &fakeMailService{}}

	err := handler.ProcessTask(context.Background(), asynq.NewTask(TaskTypeMailDeliver, []byte("{not json")))
	if !errors.Is(err, asynq.SkipRetry) {
		t.Errorf("error = %v, want SkipRetry", err)
	}

	payload, _ := json.Marshal(mailer.Mail{Kind: "bogus", To: "a@b.c"})
	err = handler.ProcessTask(context.Background(), asynq.NewTask(TaskTypeMailDeliver, payload))
	if !errors.Is(err, asynq.SkipRetry) {
		t.Errorf("error = %v, want SkipRetry for unknown kind", err)
	}
}

func TestMailDeliveryHandlerPropagatesSendErrorForRetry(t *testing.T) {
	service := &fakeMailService{sendErr: errors.New("smtp down")}
	handler := &MailDeliveryHandler{service: service}

	payload, _ := json.Marshal(mailer.Mail{Kind: mailer.KindPasswordChanged, To: "a@b.c"})
	err := handler.ProcessTask(context.Background(), asynq.NewTask(TaskTypeMailDeliver, payload))
	if err == nil || errors.Is(err, asynq.SkipRetry) {
		t.Errorf("error = %v, want retryable failure", err)
	}
}

func TestMailEnqueuerRequiresClient(t *testing.T) {
	var enqueuer *MailEnqueuer
	err := enqueuer.Deliver(context.Background(), mailer.Mail{Kind: mailer.KindPasswordChanged, To: "a@b.c"})
	if !errors.Is(err, ErrMailEnqueuerMissing) {
		t.Errorf("error = %v, want ErrMailEnqueuerMissing", err)
	}
}

func TestMailEnqueuerValidatesMail(t *testing.T) {
	enqueuer := NewMailEnqueuer(&asynq.Client{})
	err := enqueuer.Deliver(context.Background(), mailer.Mail{Kind: "bogus", To: "a@b.c"})
	if !errors.Is(err, mailer.ErrUnknownKind) {
		t.Errorf("error = %v, want ErrUnknownKind", err)
	}
}

func TestMailRegistrarRequiresService(t *testing.T) {
	registrar := &MailRegistrar{}
	if err := registrar.Register(&Manager{}); !errors.Is(err, ErrMailHandlerMissingDeps) {
		t.Errorf("error = %v, want ErrMailHandlerMissingDeps", err)
	}
}
