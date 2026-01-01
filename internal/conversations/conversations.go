package conversations

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
)

// ConversationStore provides the persistence needed for conversation tracking.
type ConversationStore interface {
	CreateConversation(ctx context.Context, arg db.CreateConversationParams) (db.Conversation, error)
	GetConversationByParentStatusID(ctx context.Context, parentStatusID pgtype.Int8) (db.Conversation, error)
	UpdateStatusConversationID(ctx context.Context, arg db.UpdateStatusConversationIDParams) error
	UpdateStatusConversationIDIfNull(ctx context.Context, arg db.UpdateStatusConversationIDIfNullParams) (int64, error)
	GetAccountConversationByParticipants(ctx context.Context, arg db.GetAccountConversationByParticipantsParams) (db.AccountConversation, error)
	CreateAccountConversation(ctx context.Context, arg db.CreateAccountConversationParams) (db.AccountConversation, error)
	UpdateAccountConversation(ctx context.Context, arg db.UpdateAccountConversationParams) (int64, error)
	DeleteAccountConversation(ctx context.Context, arg db.DeleteAccountConversationParams) (int64, error)
}

// EnsureConversationForStatus ensures the status has a conversation_id, creating one if needed.
func EnsureConversationForStatus(ctx context.Context, store ConversationStore, status db.Status, parent *db.Status) (db.Status, error) {
	if store == nil {
		return status, errors.New("conversation store missing")
	}
	if status.ConversationID.Valid {
		return status, nil
	}

	if parent != nil && parent.ConversationID.Valid {
		convID := parent.ConversationID.Int64
		if err := store.UpdateStatusConversationID(ctx, db.UpdateStatusConversationIDParams{
			ID:             status.ID,
			ConversationID: pgtype.Int8{Int64: convID, Valid: true},
		}); err != nil {
			return status, err
		}
		status.ConversationID = pgtype.Int8{Int64: convID, Valid: true}
		return status, nil
	}

	parentStatusID := status.ID
	parentAccountID := status.AccountID
	if parent != nil {
		parentStatusID = parent.ID
		parentAccountID = parent.AccountID
	}

	conv, err := store.CreateConversation(ctx, db.CreateConversationParams{
		Uri:             pgtype.Text{Valid: false},
		ParentStatusID:  pgtype.Int8{Int64: parentStatusID, Valid: true},
		ParentAccountID: pgtype.Int8{Int64: parentAccountID, Valid: true},
	})
	if err != nil {
		if !isUniqueViolation(err) {
			return status, err
		}
		conv, err = store.GetConversationByParentStatusID(ctx, pgtype.Int8{Int64: parentStatusID, Valid: true})
		if err != nil {
			return status, err
		}
	}

	convID := conv.ID
	if err := store.UpdateStatusConversationID(ctx, db.UpdateStatusConversationIDParams{
		ID:             status.ID,
		ConversationID: pgtype.Int8{Int64: convID, Valid: true},
	}); err != nil {
		return status, err
	}
	status.ConversationID = pgtype.Int8{Int64: convID, Valid: true}

	if parent != nil && !parent.ConversationID.Valid {
		_, _ = store.UpdateStatusConversationIDIfNull(ctx, db.UpdateStatusConversationIDIfNullParams{
			ID:             parent.ID,
			ConversationID: pgtype.Int8{Int64: convID, Valid: true},
		})
	}

	return status, nil
}

// AddStatusToAccountConversations updates per-account conversation state for direct statuses.
func AddStatusToAccountConversations(ctx context.Context, store ConversationStore, status db.Status, author db.Account, mentions []db.Account) error {
	if store == nil {
		return errors.New("conversation store missing")
	}
	if !status.ConversationID.Valid || status.ID == 0 {
		return nil
	}

	participants := participantAccountIDs(author, mentions)
	recipients := localRecipients(author, mentions)
	if len(recipients) == 0 {
		return nil
	}

	for _, recipient := range recipients {
		participantIDs := removeAccountID(participants, recipient.ID)
		unread := status.AccountID != recipient.ID

		conv, err := store.GetAccountConversationByParticipants(ctx, db.GetAccountConversationByParticipantsParams{
			AccountID:             recipient.ID,
			ConversationID:        status.ConversationID.Int64,
			ParticipantAccountIds: participantIDs,
		})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if errors.Is(err, pgx.ErrNoRows) {
			_, err := store.CreateAccountConversation(ctx, db.CreateAccountConversationParams{
				AccountID:             recipient.ID,
				ConversationID:        status.ConversationID.Int64,
				ParticipantAccountIds: participantIDs,
				StatusIds:             []int64{status.ID},
				LastStatusID:          pgtype.Int8{Int64: status.ID, Valid: true},
				Unread:                unread,
			})
			if err != nil {
				return err
			}
			continue
		}

		statusIDs := mergeStatusIDs(conv.StatusIds, status.ID)
		if len(statusIDs) == 0 {
			continue
		}
		lastID := statusIDs[len(statusIDs)-1]
		if _, err := store.UpdateAccountConversation(ctx, db.UpdateAccountConversationParams{
			ID:           conv.ID,
			StatusIds:    statusIDs,
			LastStatusID: pgtype.Int8{Int64: lastID, Valid: true},
			Unread:       unread,
		}); err != nil {
			return err
		}
	}

	return nil
}

// RemoveStatusFromAccountConversations removes a status from per-account conversation state.
func RemoveStatusFromAccountConversations(ctx context.Context, store ConversationStore, status db.Status, author db.Account, mentions []db.Account) error {
	if store == nil {
		return errors.New("conversation store missing")
	}
	if !status.ConversationID.Valid || status.ID == 0 {
		return nil
	}

	participants := participantAccountIDs(author, mentions)
	recipients := localRecipients(author, mentions)
	if len(recipients) == 0 {
		return nil
	}

	for _, recipient := range recipients {
		participantIDs := removeAccountID(participants, recipient.ID)
		conv, err := store.GetAccountConversationByParticipants(ctx, db.GetAccountConversationByParticipantsParams{
			AccountID:             recipient.ID,
			ConversationID:        status.ConversationID.Int64,
			ParticipantAccountIds: participantIDs,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return err
		}

		statusIDs := removeStatusID(conv.StatusIds, status.ID)
		if len(statusIDs) == 0 {
			if _, err := store.DeleteAccountConversation(ctx, db.DeleteAccountConversationParams{
				AccountID: recipient.ID,
				ID:        conv.ID,
			}); err != nil {
				return err
			}
			continue
		}

		lastID := statusIDs[len(statusIDs)-1]
		if _, err := store.UpdateAccountConversation(ctx, db.UpdateAccountConversationParams{
			ID:           conv.ID,
			StatusIds:    statusIDs,
			LastStatusID: pgtype.Int8{Int64: lastID, Valid: true},
			Unread:       conv.Unread,
		}); err != nil {
			return err
		}
	}

	return nil
}

func participantAccountIDs(author db.Account, mentions []db.Account) []int64 {
	ids := make([]int64, 0, len(mentions)+1)
	if author.ID != 0 {
		ids = append(ids, author.ID)
	}
	for _, acc := range mentions {
		if acc.ID != 0 {
			ids = append(ids, acc.ID)
		}
	}
	return uniqueSortedIDs(ids)
}

func localRecipients(author db.Account, mentions []db.Account) []db.Account {
	recipients := make(map[int64]db.Account)
	if isLocalAccount(author) {
		recipients[author.ID] = author
	}
	for _, acc := range mentions {
		if isLocalAccount(acc) {
			recipients[acc.ID] = acc
		}
	}
	if len(recipients) == 0 {
		return nil
	}
	out := make([]db.Account, 0, len(recipients))
	for _, acc := range recipients {
		out = append(out, acc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func isLocalAccount(acc db.Account) bool {
	return !acc.Domain.Valid || strings.TrimSpace(acc.Domain.String) == ""
}

func uniqueSortedIDs(ids []int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := ids[:0]
	var prev int64
	for i, id := range ids {
		if i == 0 || id != prev {
			out = append(out, id)
			prev = id
		}
	}
	return out
}

func removeAccountID(ids []int64, target int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id != target {
			out = append(out, id)
		}
	}
	return out
}

func mergeStatusIDs(ids []int64, addID int64) []int64 {
	out := make([]int64, 0, len(ids)+1)
	out = append(out, ids...)
	out = append(out, addID)
	return uniqueSortedIDs(out)
}

func removeStatusID(ids []int64, removeID int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id != removeID {
			out = append(out, id)
		}
	}
	return uniqueSortedIDs(out)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
