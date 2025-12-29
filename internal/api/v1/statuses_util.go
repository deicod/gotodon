package v1

import (
	"context"
	"strconv"
	"strings"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

const (
	MediaTypeImage   = 1 // Assuming 1 based on previous usage "image" fallback
	MediaTypeVideo   = 2
	MediaTypeUnknown = 0 // Just in case
)

// SerializeStatuses converts a slice of database Status records to API Statuses efficiently
func SerializeStatuses(ctx context.Context, q db.Querier, cfg *config.Config, dbStatuses []db.Status) []Status {
	if len(dbStatuses) == 0 {
		return []Status{}
	}

	statusIDs := make([]int64, len(dbStatuses))
	accountIDs := make([]int64, 0, len(dbStatuses))
	accountIDMap := make(map[int64]bool)

	for i, s := range dbStatuses {
		statusIDs[i] = s.ID
		if !accountIDMap[s.AccountID] {
			accountIDs = append(accountIDs, s.AccountID)
			accountIDMap[s.AccountID] = true
		}
	}

	// Bulk fetch related data
	dbAccounts, _ := q.GetAccountsByIDs(ctx, accountIDs)
	dbMedias, _ := q.GetMediaAttachmentsByStatusIDs(ctx, statusIDs)
	dbMentions, _ := q.GetMentionsByStatusIDs(ctx, statusIDs)
	dbStats, _ := q.GetStatusStatsByIDs(ctx, statusIDs)

	// Map data for easy lookup
	accountsMap := make(map[int64]Account)
	for _, a := range dbAccounts {
		accountsMap[a.ID] = SerializeAccount(cfg, a)
	}

	mediaMap := make(map[int64][]MediaAttach)
	for _, m := range dbMedias {
		if !m.StatusID.Valid {
			continue
		}
		sid := m.StatusID.Int64
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

		// Use configured domain if media is local and URL is relative (though model usually stores full or relative)
		// For robustness, assumes URL in DB is correct for now, or we could prefix if needed.
		// Existing code used raw m.RemoteUrl or m.ThumbnailRemoteUrl.
		// For local files, we might need to prepend valid URLs but let's stick to DB values for now unless empty.

		mediaMap[sid] = append(mediaMap[sid], MediaAttach{
			ID:          strconv.FormatInt(m.ID, 10),
			Type:        mediaType,
			URL:         m.RemoteUrl,
			PreviewURL:  m.ThumbnailRemoteUrl.String,
			RemoteURL:   remoteURL,
			Description: description,
			Blurhash:    blurhash,
		})
	}

	mentionsMap := make(map[int64][]Mention)
	// For mentions, we need the mentioned account usernames.
	// The bulk query GetMentionsByStatusIDs gives us the Mention records.
	// We need to fetch the Accounts for these mentions to populate Username/Acct/URL.
	// This is valid: [P1] N+1 fix.

	// Collect mentioned account IDs
	mentionedAccountIDs := make([]int64, 0)
	mentionedAccountIDSeen := make(map[int64]bool)
	for _, m := range dbMentions {
		if !mentionedAccountIDSeen[m.AccountID] {
			mentionedAccountIDs = append(mentionedAccountIDs, m.AccountID)
			mentionedAccountIDSeen[m.AccountID] = true
		}
	}

	// Fetch mentioned accounts
	if len(mentionedAccountIDs) > 0 {
		mentionedDbAccounts, _ := q.GetAccountsByIDs(ctx, mentionedAccountIDs)
		mentionedAccountsMap := make(map[int64]Account)
		for _, a := range mentionedDbAccounts {
			mentionedAccountsMap[a.ID] = SerializeAccount(cfg, a)
		}

		for _, m := range dbMentions {
			acc, ok := mentionedAccountsMap[m.AccountID]
			if ok {
				mentionsMap[m.StatusID] = append(mentionsMap[m.StatusID], Mention{
					ID:       acc.ID,
					Username: acc.Username,
					Acct:     acc.Acct,
					URL:      acc.URI,
				})
			}
		}
	}

	// Fetch tags
	dbTags, _ := q.GetTagsByStatusIDs(ctx, statusIDs)
	tagsMap := make(map[int64][]Tag)
	for _, t := range dbTags {
		tagsMap[t.StatusID] = append(tagsMap[t.StatusID], Tag{
			Name: t.Name,
			URL:  "https://" + cfg.Instance.LocalDomain + "/tags/" + t.Name, // Construct local tag URL
		})
	}

	statsMap := make(map[int64]db.StatusStat)
	for _, s := range dbStats {
		statsMap[s.StatusID] = s
	}

	previewCardsMap := make(map[int64]*PreviewCard)
	if rows, err := q.GetPreviewCardsByStatusIDs(ctx, statusIDs); err == nil {
		for _, row := range rows {
			if _, exists := previewCardsMap[row.StatusID]; exists {
				continue
			}
			card := db.PreviewCard{
				ID:                        row.ID,
				Url:                       row.Url,
				Title:                     row.Title,
				Description:               row.Description,
				ImageFileName:             row.ImageFileName,
				ImageContentType:          row.ImageContentType,
				ImageFileSize:             row.ImageFileSize,
				ImageUpdatedAt:            row.ImageUpdatedAt,
				Type:                      row.Type,
				Html:                      row.Html,
				AuthorName:                row.AuthorName,
				AuthorUrl:                 row.AuthorUrl,
				ProviderName:              row.ProviderName,
				ProviderUrl:               row.ProviderUrl,
				Width:                     row.Width,
				Height:                    row.Height,
				CreatedAt:                 row.CreatedAt,
				UpdatedAt:                 row.UpdatedAt,
				EmbedUrl:                  row.EmbedUrl,
				ImageStorageSchemaVersion: row.ImageStorageSchemaVersion,
				Blurhash:                  row.Blurhash,
				Language:                  row.Language,
				MaxScore:                  row.MaxScore,
				MaxScoreAt:                row.MaxScoreAt,
				Trendable:                 row.Trendable,
				LinkType:                  row.LinkType,
				PublishedAt:               row.PublishedAt,
				ImageDescription:          row.ImageDescription,
				AuthorAccountID:           row.AuthorAccountID,
			}
			previewCardsMap[row.StatusID] = serializePreviewCard(card)
		}
	}

	// Fetch quotes for statuses (quotes where these statuses are the quoting status)
	dbQuotes, _ := q.GetQuotesByStatusIDs(ctx, statusIDs)
	quotesMap := make(map[int64]db.Quote) // status_id -> quote
	quotedStatusIDs := make([]int64, 0)
	for _, qr := range dbQuotes {
		quotesMap[qr.StatusID] = qr
		if qr.QuotedStatusID.Valid {
			quotedStatusIDs = append(quotedStatusIDs, qr.QuotedStatusID.Int64)
		}
	}

	// Fetch quoted statuses
	quotedStatusesMap := make(map[int64]Status)
	if len(quotedStatusIDs) > 0 {
		quotedDbStatuses, _ := q.GetStatusesByIDs(ctx, quotedStatusIDs)

		// Bulk fetch accounts for quoted statuses to avoid N+1 queries
		quotedAccountIDs := make([]int64, 0, len(quotedDbStatuses))
		quotedAccountIDSeen := make(map[int64]bool)
		for _, qs := range quotedDbStatuses {
			if !quotedAccountIDSeen[qs.AccountID] {
				quotedAccountIDs = append(quotedAccountIDs, qs.AccountID)
				quotedAccountIDSeen[qs.AccountID] = true
			}
		}

		quotedAccountsMap := make(map[int64]Account)
		if len(quotedAccountIDs) > 0 {
			quotedDbAccounts, _ := q.GetAccountsByIDs(ctx, quotedAccountIDs)
			for _, a := range quotedDbAccounts {
				quotedAccountsMap[a.ID] = SerializeAccount(cfg, a)
			}
		}

		// Serialize quoted statuses (without recursing into their quotes to avoid infinite loops)
		for _, qs := range quotedDbStatuses {
			var accPtr *Account
			if acc, ok := quotedAccountsMap[qs.AccountID]; ok {
				accPtr = &acc
			}
			quotedStatusesMap[qs.ID] = SerializeStatusSimple(ctx, q, cfg, qs, accPtr)
		}
	}

	results := make([]Status, len(dbStatuses))
	for i, dbStatus := range dbStatuses {
		// Defaults
		visibility := "public"
		switch dbStatus.Visibility {
		case 1:
			visibility = "unlisted"
		case 2:
			visibility = "private"
		case 3:
			visibility = "direct"
		}

		account, ok := accountsMap[dbStatus.AccountID]
		if !ok {
			// Fallback empty account to avoid crash
			account = Account{ID: strconv.FormatInt(dbStatus.AccountID, 10), Username: "unknown"}
		}

		mediaAttachments := mediaMap[dbStatus.ID]
		if mediaAttachments == nil {
			mediaAttachments = []MediaAttach{}
		}

		mentions := mentionsMap[dbStatus.ID]
		if mentions == nil {
			mentions = []Mention{}
		}

		statusTags := tagsMap[dbStatus.ID]
		if statusTags == nil {
			statusTags = []Tag{}
		}

		// Stats
		repliesCount := int64(0)
		reblogsCount := int64(0)
		favouritesCount := int64(0)
		if stat, ok := statsMap[dbStatus.ID]; ok {
			repliesCount = stat.RepliesCount
			reblogsCount = stat.ReblogsCount
			favouritesCount = stat.FavouritesCount
		}

		var language *string
		if dbStatus.Language.Valid {
			s := dbStatus.Language.String
			language = &s
		}

		s := Status{
			ID:                 strconv.FormatInt(dbStatus.ID, 10),
			CreatedAt:          dbStatus.CreatedAt.Time,
			InReplyToID:        nil,
			InReplyToAccountID: nil,
			Sensitive:          dbStatus.Sensitive,
			SpoilerText:        dbStatus.SpoilerText,
			Visibility:         visibility,
			Language:           language,
			URI:                dbStatus.Uri.String,
			URL:                &dbStatus.Url.String,
			RepliesCount:       repliesCount,
			ReblogsCount:       reblogsCount,
			FavouritesCount:    favouritesCount,
			Favourited:         false,
			Reblogged:          false,
			Muted:              false,
			Bookmarked:         false,
			Content:            dbStatus.Text,
			Reblog:             nil,
			Account:            account,
			MediaAttachments:   mediaAttachments,
			Mentions:           mentions,
			Tags:               statusTags,
			Emojis:             []CustomEmoji{},
			Pinned:             false,
		}
		if card, ok := previewCardsMap[dbStatus.ID]; ok {
			s.Card = card
		}

		if dbStatus.InReplyToID.Valid {
			idStr := strconv.FormatInt(dbStatus.InReplyToID.Int64, 10)
			s.InReplyToID = &idStr
		}
		if dbStatus.InReplyToAccountID.Valid {
			idStr := strconv.FormatInt(dbStatus.InReplyToAccountID.Int64, 10)
			s.InReplyToAccountID = &idStr
		}

		// Add quote fields if the status quotes another
		if qr, hasQuote := quotesMap[dbStatus.ID]; hasQuote && qr.QuotedStatusID.Valid {
			quoteIDStr := strconv.FormatInt(qr.QuotedStatusID.Int64, 10)
			s.QuoteID = &quoteIDStr
			if quotedStatus, ok := quotedStatusesMap[qr.QuotedStatusID.Int64]; ok {
				s.Quote = &quotedStatus
			}
		}

		results[i] = s
	}

	return results
}

// SerializeStatus converts a database Status record to API Status
func SerializeStatus(ctx context.Context, q db.Querier, cfg *config.Config, dbStatus db.Status) Status {
	results := SerializeStatuses(ctx, q, cfg, []db.Status{dbStatus})
	if len(results) > 0 {
		return results[0]
	}
	return Status{}
}

// SerializeStatusSimple converts a status without recursing into quotes (to avoid infinite loops)
func SerializeStatusSimple(ctx context.Context, q db.Querier, cfg *config.Config, dbStatus db.Status, preFetchedAccount *Account) Status {
	// Get account
	var account Account
	if preFetchedAccount != nil {
		account = *preFetchedAccount
	} else {
		account = Account{ID: strconv.FormatInt(dbStatus.AccountID, 10), Username: "unknown"}
		if acc, err := q.GetAccountByID(ctx, dbStatus.AccountID); err == nil {
			account = SerializeAccount(cfg, acc)
		}
	}

	visibility := "public"
	switch dbStatus.Visibility {
	case 1:
		visibility = "unlisted"
	case 2:
		visibility = "private"
	case 3:
		visibility = "direct"
	}

	var language *string
	if dbStatus.Language.Valid {
		s := dbStatus.Language.String
		language = &s
	}

	return Status{
		ID:               strconv.FormatInt(dbStatus.ID, 10),
		CreatedAt:        dbStatus.CreatedAt.Time,
		Sensitive:        dbStatus.Sensitive,
		SpoilerText:      dbStatus.SpoilerText,
		Visibility:       visibility,
		Language:         language,
		URI:              dbStatus.Uri.String,
		URL:              &dbStatus.Url.String,
		Content:          dbStatus.Text,
		Account:          account,
		MediaAttachments: []MediaAttach{},
		Mentions:         []Mention{},
		Tags:             []Tag{},
		Emojis:           []CustomEmoji{},
	}
}

func serializePreviewCard(card db.PreviewCard) *PreviewCard {
	if card.ID == 0 {
		return nil
	}

	var imageURL *string
	if card.ImageFileName.Valid {
		image := strings.TrimSpace(card.ImageFileName.String)
		if image != "" {
			if strings.HasPrefix(image, "http://") || strings.HasPrefix(image, "https://") {
				imageURL = &image
			} else {
				local := "/files/" + image
				imageURL = &local
			}
		}
	}

	var blurhash *string
	if card.Blurhash.Valid {
		value := card.Blurhash.String
		blurhash = &value
	}

	return &PreviewCard{
		URL:          card.Url,
		Title:        card.Title,
		Description:  card.Description,
		Type:         previewCardType(card.Type),
		AuthorName:   card.AuthorName,
		AuthorURL:    card.AuthorUrl,
		ProviderName: card.ProviderName,
		ProviderURL:  card.ProviderUrl,
		HTML:         card.Html,
		Width:        int(card.Width),
		Height:       int(card.Height),
		Image:        imageURL,
		Blurhash:     blurhash,
	}
}

func previewCardType(cardType int32) string {
	switch cardType {
	case 1:
		return "photo"
	case 2:
		return "video"
	case 3:
		return "rich"
	default:
		return "link"
	}
}
