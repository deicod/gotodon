package v1

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/deicod/gotodon/internal/db"
)

type scheduledStatusMediaQuerier interface {
	GetMediaAttachmentsByScheduledStatusIDs(ctx context.Context, ids []int64) ([]db.MediaAttachment, error)
}

func scheduledStatusParamsJSON(raw []byte) json.RawMessage {
	if len(raw) == 0 || !json.Valid(raw) {
		return json.RawMessage([]byte("{}"))
	}
	return json.RawMessage(raw)
}

func scheduledStatusMediaAttachments(ctx context.Context, q scheduledStatusMediaQuerier, ids []int64) map[int64][]MediaAttach {
	mediaMap := make(map[int64][]MediaAttach)
	if q == nil || len(ids) == 0 {
		return mediaMap
	}
	dbMedias, err := q.GetMediaAttachmentsByScheduledStatusIDs(ctx, ids)
	if err != nil {
		return mediaMap
	}
	for _, m := range dbMedias {
		if !m.ScheduledStatusID.Valid {
			continue
		}
		scheduledID := m.ScheduledStatusID.Int64
		mediaType := "image"
		if m.Type == MediaTypeVideo {
			mediaType = "video"
		}

		var remoteURL *string
		if m.RemoteUrl != "" {
			s := m.RemoteUrl
			remoteURL = &s
		}

		var description *string
		if m.Description.Valid {
			s := m.Description.String
			description = &s
		}

		var blurhash *string
		if m.Blurhash.Valid {
			s := m.Blurhash.String
			blurhash = &s
		}

		mediaMap[scheduledID] = append(mediaMap[scheduledID], MediaAttach{
			ID:          strconv.FormatInt(m.ID, 10),
			Type:        mediaType,
			URL:         m.RemoteUrl,
			PreviewURL:  m.ThumbnailRemoteUrl.String,
			RemoteURL:   remoteURL,
			Description: description,
			Blurhash:    blurhash,
		})
	}
	return mediaMap
}

func serializeScheduledStatus(scheduled db.ScheduledStatus, media []MediaAttach) ScheduledStatus {
	var scheduledAt interface{}
	if scheduled.ScheduledAt.Valid {
		scheduledAt = scheduled.ScheduledAt.Time
	}
	return ScheduledStatus{
		ID:               strconv.FormatInt(scheduled.ID, 10),
		ScheduledAt:      scheduledAt,
		Params:           scheduledStatusParamsJSON(scheduled.Params),
		MediaAttachments: media,
	}
}
