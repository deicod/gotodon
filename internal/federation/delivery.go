package federation

import (
	"context"
	"fmt"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

// DeliveryService signs and sends outgoing ActivityPub activities.
type DeliveryService struct {
	newOutbox func(keyID, privateKeyPEM string) (*activitypub.OutboxHandler, error)
}

// NewDeliveryService creates a delivery service for signed ActivityPub requests.
func NewDeliveryService() *DeliveryService {
	return &DeliveryService{
		newOutbox: activitypub.NewOutboxHandler,
	}
}

// DeliverActivity signs and posts the activity to the remote inbox.
func (s *DeliveryService) DeliverActivity(ctx context.Context, localActor db.Account, inbox string, activity *activitypub.Activity) error {
	if inbox == "" {
		return fmt.Errorf("missing inbox")
	}
	if activity == nil {
		return fmt.Errorf("missing activity")
	}
	if localActor.Uri == "" {
		return fmt.Errorf("local actor uri missing")
	}
	if !localActor.PrivateKey.Valid || localActor.PrivateKey.String == "" {
		return fmt.Errorf("local actor signing key missing")
	}

	keyID := fmt.Sprintf("%s#main-key", localActor.Uri)
	outbox, err := s.newOutbox(keyID, localActor.PrivateKey.String)
	if err != nil {
		return err
	}

	return outbox.DeliverActivity(ctx, inbox, activity)
}
