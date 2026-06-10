package mailer

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type captureSender struct {
	mu       sync.Mutex
	to       string
	message  []byte
	sendErr  error
	sendDone chan struct{}
}

func newCaptureSender() *captureSender {
	return &captureSender{sendDone: make(chan struct{}, 8)}
}

func (s *captureSender) Send(ctx context.Context, to string, message []byte) error {
	s.mu.Lock()
	s.to = to
	s.message = message
	err := s.sendErr
	s.mu.Unlock()
	s.sendDone <- struct{}{}
	return err
}

func staticInstance(ctx context.Context) InstanceInfo {
	return InstanceInfo{Title: "Example Social", Domain: "social.example.com"}
}

func TestServiceRenderAndSend(t *testing.T) {
	sender := newCaptureSender()
	service := NewService(sender, "notifications@example.com", staticInstance)

	mail := Mail{Kind: KindPasswordChanged, To: "user@example.org"}
	if err := service.RenderAndSend(context.Background(), mail); err != nil {
		t.Fatalf("RenderAndSend() error = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if sender.to != "user@example.org" {
		t.Errorf("to = %q", sender.to)
	}
	message := string(sender.message)
	if !strings.Contains(message, "Subject: Example Social: Password changed") {
		t.Errorf("message missing subject:\n%s", message)
	}
	if !strings.Contains(message, "From: <notifications@example.com>") {
		t.Errorf("message missing from:\n%s", message)
	}
}

func TestServiceRenderAndSendValidates(t *testing.T) {
	service := NewService(newCaptureSender(), "n@example.com", staticInstance)
	if err := service.RenderAndSend(context.Background(), Mail{Kind: "bogus", To: "a@b.c"}); !errors.Is(err, ErrUnknownKind) {
		t.Errorf("error = %v, want ErrUnknownKind", err)
	}
	if err := service.RenderAndSend(context.Background(), Mail{Kind: KindPasswordChanged}); !errors.Is(err, ErrRecipientMiss) {
		t.Errorf("error = %v, want ErrRecipientMiss", err)
	}
}

func TestServiceRenderAndSendPropagatesSendError(t *testing.T) {
	sender := newCaptureSender()
	sender.sendErr = errors.New("boom")
	service := NewService(sender, "n@example.com", staticInstance)
	err := service.RenderAndSend(context.Background(), Mail{Kind: KindPasswordChanged, To: "a@b.c"})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %v, want send failure", err)
	}
}

func TestDirectDelivererSendsAsync(t *testing.T) {
	sender := newCaptureSender()
	service := NewService(sender, "n@example.com", staticInstance)
	deliverer := NewDirectDeliverer(service, nil)

	if err := deliverer.Deliver(context.Background(), Mail{Kind: KindPasswordChanged, To: "user@example.org"}); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}

	select {
	case <-sender.sendDone:
	case <-time.After(5 * time.Second):
		t.Fatal("send never happened")
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if sender.to != "user@example.org" {
		t.Errorf("to = %q", sender.to)
	}
}

func TestDirectDelivererValidatesSynchronously(t *testing.T) {
	deliverer := NewDirectDeliverer(NewService(newCaptureSender(), "n@example.com", staticInstance), nil)
	if err := deliverer.Deliver(context.Background(), Mail{Kind: "bogus", To: "a@b.c"}); !errors.Is(err, ErrUnknownKind) {
		t.Errorf("error = %v, want ErrUnknownKind", err)
	}
}
