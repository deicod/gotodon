package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/deicod/gotodon/internal/db"
)

type statusCodeError interface {
	StatusCode() int
}

type scheduledStatusParams struct {
	Status      string              `json:"status"`
	Text        string              `json:"text"`
	InReplyToID idString            `json:"in_reply_to_id"`
	MediaIDs    flexibleStringSlice `json:"media_ids"`
	Sensitive   bool                `json:"sensitive"`
	SpoilerText string              `json:"spoiler_text"`
	Visibility  string              `json:"visibility"`
	Language    *string             `json:"language"`
	Poll        *PollReq            `json:"poll"`
}

type idString struct {
	Value string
	Valid bool
}

func (i *idString) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*i = idString{}
		return nil
	}
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		str = strings.TrimSpace(str)
		if str == "" {
			*i = idString{}
			return nil
		}
		*i = idString{Value: str, Valid: true}
		return nil
	}
	var num json.Number
	if err := json.Unmarshal(data, &num); err == nil {
		value := strings.TrimSpace(num.String())
		if value == "" {
			*i = idString{}
			return nil
		}
		*i = idString{Value: value, Valid: true}
		return nil
	}
	return fmt.Errorf("invalid id value")
}

type flexibleStringSlice []string

func (s *flexibleStringSlice) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err == nil {
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			var id idString
			if err := json.Unmarshal(item, &id); err != nil {
				return err
			}
			if id.Valid {
				out = append(out, id.Value)
			}
		}
		*s = out
		return nil
	}
	var single idString
	if err := json.Unmarshal(data, &single); err != nil {
		return err
	}
	if single.Valid {
		*s = []string{single.Value}
	}
	return nil
}

func parseScheduledStatusParams(data []byte) (CreateStatusRequest, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return CreateStatusRequest{}, statusCreateError{
			Status:  http.StatusBadRequest,
			Message: "Scheduled status params missing",
		}
	}
	var params scheduledStatusParams
	if err := json.Unmarshal(data, &params); err != nil {
		return CreateStatusRequest{}, statusCreateError{
			Status:  http.StatusBadRequest,
			Message: "Scheduled status params invalid",
		}
	}
	if params.Language != nil && strings.TrimSpace(*params.Language) == "" {
		params.Language = nil
	}
	statusText := params.Status
	if statusText == "" {
		statusText = params.Text
	}
	req := CreateStatusRequest{
		Status:      statusText,
		MediaIDs:    []string(params.MediaIDs),
		Sensitive:   params.Sensitive,
		SpoilerText: params.SpoilerText,
		Visibility:  params.Visibility,
		Language:    params.Language,
		Poll:        params.Poll,
	}
	if params.InReplyToID.Valid {
		replyID := params.InReplyToID.Value
		req.InReplyToID = &replyID
	}
	return req, nil
}

// PublishScheduledStatus creates and publishes a scheduled status payload.
func (h *StatusesHandler) PublishScheduledStatus(ctx context.Context, scheduled db.ScheduledStatus) error {
	if !h.useDB() || h.queries == nil {
		return statusCreateError{Status: http.StatusInternalServerError, Message: "Database not configured"}
	}
	if scheduled.ID <= 0 {
		return statusCreateError{Status: http.StatusBadRequest, Message: "Scheduled status ID missing"}
	}
	if scheduled.AccountID <= 0 {
		return statusCreateError{Status: http.StatusBadRequest, Message: "Scheduled status account missing"}
	}
	req, err := parseScheduledStatusParams(scheduled.Params)
	if err != nil {
		_ = h.queries.DeleteScheduledStatus(ctx, db.DeleteScheduledStatusParams{
			ID:        scheduled.ID,
			AccountID: scheduled.AccountID,
		})
		return err
	}
	if h.pool == nil {
		return statusCreateError{Status: http.StatusInternalServerError, Message: "Failed to start transaction"}
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return statusCreateError{Status: http.StatusInternalServerError, Message: "Failed to start transaction"}
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := db.New(tx)
	result, err := h.createStatusTx(ctx, qtx, scheduled.AccountID, req)
	if err != nil {
		var statusErr statusCodeError
		if errors.As(err, &statusErr) && statusErr.StatusCode() >= 400 && statusErr.StatusCode() < 500 {
			_ = h.queries.DeleteScheduledStatus(ctx, db.DeleteScheduledStatusParams{
				ID:        scheduled.ID,
				AccountID: scheduled.AccountID,
			})
		}
		return err
	}

	if err := qtx.DeleteScheduledStatus(ctx, db.DeleteScheduledStatusParams{
		ID:        scheduled.ID,
		AccountID: scheduled.AccountID,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return statusCreateError{Status: http.StatusInternalServerError, Message: "Failed to commit transaction"}
	}

	_ = h.publishCreatedStatus(ctx, scheduled.AccountID, result)
	return nil
}
