package v1

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

const (
	MediaTypeImage   = 1 // Assuming 1 based on previous usage "image" fallback
	MediaTypeVideo   = 2
	MediaTypeUnknown = 0 // Just in case
)

type statusEmojiQuerier interface {
	ListCustomEmojisByShortcodesAndDomains(ctx context.Context, arg db.ListCustomEmojisByShortcodesAndDomainsParams) ([]db.CustomEmoji, error)
}

type statusSerializeOptions struct {
	includeReblogs bool
	includeQuotes  bool
}

type statusSerializerQuerier interface {
	AccountDataQuerier
	statusRelationshipQuerier
	statusEmojiQuerier
	GetAccountByID(ctx context.Context, id int64) (db.Account, error)
	GetMediaAttachmentsByStatusIDs(ctx context.Context, statusIds []int64) ([]db.MediaAttachment, error)
	GetMentionsByStatusIDs(ctx context.Context, statusIds []int64) ([]db.Mention, error)
	GetStatusStatsByIDs(ctx context.Context, statusIds []int64) ([]db.StatusStat, error)
	GetTagsByStatusIDs(ctx context.Context, statusIds []int64) ([]db.GetTagsByStatusIDsRow, error)
	ListConversationMutes(ctx context.Context, arg db.ListConversationMutesParams) ([]int64, error)
	GetPreviewCardsByStatusIDs(ctx context.Context, statusIds []int64) ([]db.GetPreviewCardsByStatusIDsRow, error)
	GetQuotesByStatusIDs(ctx context.Context, statusIds []int64) ([]db.Quote, error)
	GetStatusesByIDs(ctx context.Context, ids []int64) ([]db.Status, error)
}

type statusRelationshipQuerier interface {
	ListFavouritedStatusIDs(ctx context.Context, arg db.ListFavouritedStatusIDsParams) ([]int64, error)
	ListBookmarkedStatusIDs(ctx context.Context, arg db.ListBookmarkedStatusIDsParams) ([]int64, error)
	ListRebloggedStatusIDs(ctx context.Context, arg db.ListRebloggedStatusIDsParams) ([]pgtype.Int8, error)
	ListPinnedStatusIDs(ctx context.Context, arg db.ListPinnedStatusIDsParams) ([]int64, error)
}

type statusRelationships struct {
	favourited map[int64]bool
	reblogged  map[int64]bool
	bookmarked map[int64]bool
	pinned     map[int64]bool
}

func isPinnableStatus(accountID int64, status db.Status) bool {
	if accountID == 0 || status.AccountID != accountID || status.ReblogOfID.Valid {
		return false
	}
	switch status.Visibility {
	case 0, 1, 2:
		return true
	default:
		return false
	}
}

func buildStatusRelationships(ctx context.Context, q statusRelationshipQuerier, accountID int64, statuses []db.Status) statusRelationships {
	relationships := statusRelationships{}
	if q == nil || accountID == 0 || len(statuses) == 0 {
		return relationships
	}

	statusIDs := make([]int64, 0, len(statuses))
	statusIDSeen := make(map[int64]struct{})
	pinnableIDs := make([]int64, 0, len(statuses))
	pinnableSeen := make(map[int64]struct{})

	for _, status := range statuses {
		if _, ok := statusIDSeen[status.ID]; !ok {
			statusIDs = append(statusIDs, status.ID)
			statusIDSeen[status.ID] = struct{}{}
		}
		if status.ReblogOfID.Valid {
			rebloggedID := status.ReblogOfID.Int64
			if _, ok := statusIDSeen[rebloggedID]; !ok {
				statusIDs = append(statusIDs, rebloggedID)
				statusIDSeen[rebloggedID] = struct{}{}
			}
		}

		if isPinnableStatus(accountID, status) {
			if _, ok := pinnableSeen[status.ID]; !ok {
				pinnableIDs = append(pinnableIDs, status.ID)
				pinnableSeen[status.ID] = struct{}{}
			}
		}
	}

	if len(statusIDs) == 0 {
		return relationships
	}

	if ids, err := q.ListFavouritedStatusIDs(ctx, db.ListFavouritedStatusIDsParams{
		AccountID: accountID,
		StatusIds: statusIDs,
	}); err == nil && len(ids) > 0 {
		relationships.favourited = make(map[int64]bool)
		for _, id := range ids {
			relationships.favourited[id] = true
		}
	}

	if ids, err := q.ListBookmarkedStatusIDs(ctx, db.ListBookmarkedStatusIDsParams{
		AccountID: accountID,
		StatusIds: statusIDs,
	}); err == nil && len(ids) > 0 {
		relationships.bookmarked = make(map[int64]bool)
		for _, id := range ids {
			relationships.bookmarked[id] = true
		}
	}

	if ids, err := q.ListRebloggedStatusIDs(ctx, db.ListRebloggedStatusIDsParams{
		AccountID: accountID,
		StatusIds: statusIDs,
	}); err == nil && len(ids) > 0 {
		relationships.reblogged = make(map[int64]bool)
		for _, id := range ids {
			if id.Valid {
				relationships.reblogged[id.Int64] = true
			}
		}
	}

	if len(pinnableIDs) > 0 {
		if ids, err := q.ListPinnedStatusIDs(ctx, db.ListPinnedStatusIDsParams{
			AccountID: accountID,
			StatusIds: pinnableIDs,
		}); err == nil && len(ids) > 0 {
			relationships.pinned = make(map[int64]bool)
			for _, id := range ids {
				relationships.pinned[id] = true
			}
		}
	}

	return relationships
}

func applyStatusRelationships(status *Status, dbStatus db.Status, relationships statusRelationships, accountID int64, hasAccount bool) {
	if status == nil {
		return
	}

	status.Favourited = relationships.favourited[dbStatus.ID]
	status.Reblogged = relationships.reblogged[dbStatus.ID]
	status.Bookmarked = relationships.bookmarked[dbStatus.ID]

	if hasAccount && isPinnableStatus(accountID, dbStatus) {
		pinned := relationships.pinned[dbStatus.ID]
		status.Pinned = &pinned
	}
}

func statusEmojiDomain(acc Account) string {
	acct := strings.TrimSpace(acc.Acct)
	if acct == "" {
		return ""
	}
	parts := strings.SplitN(acct, "@", 2)
	if len(parts) == 2 {
		return strings.ToLower(strings.TrimSpace(parts[1]))
	}
	return ""
}

func buildStatusEmojis(ctx context.Context, q statusEmojiQuerier, statuses []db.Status, accounts map[int64]Account) map[int64][]CustomEmoji {
	if q == nil || len(statuses) == 0 {
		return nil
	}

	type shortcodeRange struct {
		start, end int
	}

	var statusShortcodeRanges map[int64]shortcodeRange
	var allShortcodes []string
	var statusDomains map[int64]string
	var domainShortcodes map[string]map[string]struct{}

	// Reuse buffer for shortcode extraction to avoid allocations
	var shortcodeBuf []string

	for _, status := range statuses {
		shortcodeBuf = shortcodeBuf[:0]
		appendShortcodes(status.SpoilerText, &shortcodeBuf)
		appendShortcodes(status.Text, &shortcodeBuf)

		if len(shortcodeBuf) == 0 {
			continue
		}

		if statusShortcodeRanges == nil {
			statusShortcodeRanges = make(map[int64]shortcodeRange, len(statuses))
			statusDomains = make(map[int64]string, len(statuses))
			domainShortcodes = make(map[string]map[string]struct{})
		}

		// Append to flat buffer instead of allocating new slice
		start := len(allShortcodes)
		allShortcodes = append(allShortcodes, shortcodeBuf...)
		end := len(allShortcodes)

		statusShortcodeRanges[status.ID] = shortcodeRange{start: start, end: end}

		domain := ""
		if acc, ok := accounts[status.AccountID]; ok {
			domain = statusEmojiDomain(acc)
		}
		statusDomains[status.ID] = domain

		if _, ok := domainShortcodes[domain]; !ok {
			domainShortcodes[domain] = make(map[string]struct{})
		}
		for _, shortcode := range shortcodeBuf {
			domainShortcodes[domain][shortcode] = struct{}{}
		}
	}

	if len(domainShortcodes) == 0 {
		return nil
	}

	// Flatten domains and shortcodes for bulk query
	allDomains := make([]string, 0, len(domainShortcodes))
	queryShortcodes := make([]string, 0)
	shortcodeSeen := make(map[string]struct{})
	includeLocal := false

	for domain, shortcodes := range domainShortcodes {
		if domain == "" {
			includeLocal = true
		} else {
			allDomains = append(allDomains, domain)
		}
		for shortcode := range shortcodes {
			if _, ok := shortcodeSeen[shortcode]; !ok {
				queryShortcodes = append(queryShortcodes, shortcode)
				shortcodeSeen[shortcode] = struct{}{}
			}
		}
	}

	emojis, err := q.ListCustomEmojisByShortcodesAndDomains(ctx, db.ListCustomEmojisByShortcodesAndDomainsParams{
		Shortcodes:   queryShortcodes,
		Domains:      allDomains,
		IncludeLocal: includeLocal,
	})
	if err != nil {
		// Log error? For now just return empty as per original behavior which ignored errors inside loop
		return nil
	}

	emojisByDomain := make(map[string]map[string]db.CustomEmoji)
	for _, emoji := range emojis {
		domain := emoji.Domain.String
		if !emoji.Domain.Valid {
			domain = ""
		}
		if _, ok := emojisByDomain[domain]; !ok {
			emojisByDomain[domain] = make(map[string]db.CustomEmoji)
		}
		emojisByDomain[domain][emoji.Shortcode] = emoji
	}

	emojisByStatus := make(map[int64][]CustomEmoji, len(statusShortcodeRanges))
	for statusID, rng := range statusShortcodeRanges {
		shortcodes := allShortcodes[rng.start:rng.end]
		domain := statusDomains[statusID]
		emojiMap := emojisByDomain[domain]
		if len(emojiMap) == 0 {
			emojisByStatus[statusID] = []CustomEmoji{}
			continue
		}
		emojis := make([]CustomEmoji, 0, len(shortcodes))
		for _, shortcode := range shortcodes {
			emoji, ok := emojiMap[shortcode]
			if !ok {
				continue
			}
			emojis = append(emojis, CustomEmoji{
				Shortcode:       emoji.Shortcode,
				URL:             GetEmojiURL(emoji),
				StaticURL:       GetEmojiURL(emoji),
				VisibleInPicker: emoji.VisibleInPicker,
			})
		}
		emojisByStatus[statusID] = emojis
	}

	return emojisByStatus
}

// SerializeStatuses converts a slice of database Status records to API Statuses efficiently
func SerializeStatuses(ctx context.Context, q db.Querier, cfg *config.Config, dbStatuses []db.Status) []Status {
	return serializeStatusesInternal(ctx, q, cfg, dbStatuses, statusSerializeOptions{
		includeReblogs: true,
		includeQuotes:  true,
	})
}

func serializeStatusesInternal(ctx context.Context, q statusSerializerQuerier, cfg *config.Config, dbStatuses []db.Status, opts statusSerializeOptions) []Status {
	if len(dbStatuses) == 0 {
		return []Status{}
	}

	// Optimization: Use lazy map initialization for reblogged statuses.
	var rebloggedStatuses map[int64]Status
	if opts.includeReblogs {
		reblogIDs := make([]int64, 0)
		reblogIDSeen := make(map[int64]struct{})
		for _, status := range dbStatuses {
			if !status.ReblogOfID.Valid {
				continue
			}
			reblogID := status.ReblogOfID.Int64
			if _, ok := reblogIDSeen[reblogID]; ok {
				continue
			}
			reblogIDSeen[reblogID] = struct{}{}
			reblogIDs = append(reblogIDs, reblogID)
		}
		if len(reblogIDs) > 0 {
			if rebloggedDbStatuses, err := q.GetStatusesByIDs(ctx, reblogIDs); err == nil {
				serialized := serializeStatusesInternal(ctx, q, cfg, rebloggedDbStatuses, statusSerializeOptions{
					includeReblogs: false,
					includeQuotes:  true,
				})
				if len(serialized) > 0 {
					rebloggedStatuses = make(map[int64]Status)
					for _, status := range serialized {
						if statusID, err := strconv.ParseInt(status.ID, 10, 64); err == nil {
							rebloggedStatuses[statusID] = status
						}
					}
				}
			}
		}
	}

	statusIDs := make([]int64, len(dbStatuses))
	accountIDs := make([]int64, 0, len(dbStatuses))
	accountIDMap := make(map[int64]bool)
	conversationIDs := make([]int64, 0, len(dbStatuses))
	conversationIDMap := make(map[int64]bool)

	for i, s := range dbStatuses {
		statusIDs[i] = s.ID
		if !accountIDMap[s.AccountID] {
			accountIDs = append(accountIDs, s.AccountID)
			accountIDMap[s.AccountID] = true
		}
		if s.ConversationID.Valid {
			cid := s.ConversationID.Int64
			if !conversationIDMap[cid] {
				conversationIDs = append(conversationIDs, cid)
				conversationIDMap[cid] = true
			}
		}
	}

	accountID, hasAccount := middleware.GetAccountID(ctx)

	// Bulk fetch related data
	dbAccounts, _ := q.GetAccountsByIDs(ctx, accountIDs)
	dbMedias, _ := q.GetMediaAttachmentsByStatusIDs(ctx, statusIDs)
	dbMentions, _ := q.GetMentionsByStatusIDs(ctx, statusIDs)
	dbStats, _ := q.GetStatusStatsByIDs(ctx, statusIDs)

	// Map data for easy lookup
	accountsMap := serializeAccounts(ctx, q, cfg, dbAccounts, true)
	statusEmojis := buildStatusEmojis(ctx, q, dbStatuses, accountsMap)

	type indexRange struct {
		start, end int
	}

	var allMedia []MediaAttach
	var mediaRanges map[int64]indexRange

	if len(dbMedias) > 0 {
		slices.SortFunc(dbMedias, func(a, b db.MediaAttachment) int {
			if !a.StatusID.Valid {
				if !b.StatusID.Valid {
					return 0
				}
				return 1
			}
			if !b.StatusID.Valid {
				return -1
			}
			if a.StatusID.Int64 < b.StatusID.Int64 {
				return -1
			}
			if a.StatusID.Int64 > b.StatusID.Int64 {
				return 1
			}
			return 0
		})

		allMedia = make([]MediaAttach, 0, len(dbMedias))
		mediaRanges = make(map[int64]indexRange)

		var currentSID int64 = -1
		start := 0

		for _, m := range dbMedias {
			if !m.StatusID.Valid {
				continue
			}
			sid := m.StatusID.Int64

			if sid != currentSID {
				if currentSID != -1 {
					mediaRanges[currentSID] = indexRange{start: start, end: len(allMedia)}
				}
				currentSID = sid
				start = len(allMedia)
			}

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

			allMedia = append(allMedia, MediaAttach{
				ID:          strconv.FormatInt(m.ID, 10),
				Type:        mediaType,
				URL:         m.RemoteUrl,
				PreviewURL:  m.ThumbnailRemoteUrl.String,
				RemoteURL:   remoteURL,
				Description: description,
				Blurhash:    blurhash,
			})
		}
		if currentSID != -1 {
			mediaRanges[currentSID] = indexRange{start: start, end: len(allMedia)}
		}
	}

	var allMentions []Mention
	var mentionRanges map[int64]indexRange

	// For mentions, we need the mentioned account usernames.
	// The bulk query GetMentionsByStatusIDs gives us the Mention records.
	// We need to fetch the Accounts for these mentions to populate Username/Acct/URL.
	// This is valid: [P1] N+1 fix.

	if len(dbMentions) > 0 {
		// Collect mentioned account IDs
		mentionedAccountIDs := make([]int64, 0, len(dbMentions))
		mentionedAccountIDSeen := make(map[int64]bool, len(dbMentions))
		for _, m := range dbMentions {
			if !mentionedAccountIDSeen[m.AccountID] {
				mentionedAccountIDs = append(mentionedAccountIDs, m.AccountID)
				mentionedAccountIDSeen[m.AccountID] = true
			}
		}

		// Fetch mentioned accounts
		var mentionedAccountsMap map[int64]Account
		if len(mentionedAccountIDs) > 0 {
			mentionedDbAccounts, _ := q.GetAccountsByIDs(ctx, mentionedAccountIDs)
			mentionedAccountsMap = serializeAccounts(ctx, q, cfg, mentionedDbAccounts, true)
		}

		slices.SortFunc(dbMentions, func(a, b db.Mention) int {
			if a.StatusID < b.StatusID {
				return -1
			}
			if a.StatusID > b.StatusID {
				return 1
			}
			return 0
		})

		allMentions = make([]Mention, 0, len(dbMentions))
		mentionRanges = make(map[int64]indexRange)

		var currentSID int64 = -1
		start := 0

		for _, m := range dbMentions {
			sid := m.StatusID

			if sid != currentSID {
				if currentSID != -1 {
					mentionRanges[currentSID] = indexRange{start: start, end: len(allMentions)}
				}
				currentSID = sid
				start = len(allMentions)
			}

			if mentionedAccountsMap != nil {
				acc, ok := mentionedAccountsMap[m.AccountID]
				if ok {
					url := acc.URL
					if strings.TrimSpace(url) == "" {
						url = acc.URI
					}
					allMentions = append(allMentions, Mention{
						ID:       acc.ID,
						Username: acc.Username,
						Acct:     acc.Acct,
						URL:      url,
					})
				}
			}
		}
		if currentSID != -1 {
			mentionRanges[currentSID] = indexRange{start: start, end: len(allMentions)}
		}
	}

	// Fetch tags
	dbTags, _ := q.GetTagsByStatusIDs(ctx, statusIDs)
	var allTags []Tag
	var tagRanges map[int64]indexRange

	if len(dbTags) > 0 {
		slices.SortFunc(dbTags, func(a, b db.GetTagsByStatusIDsRow) int {
			if a.StatusID < b.StatusID {
				return -1
			}
			if a.StatusID > b.StatusID {
				return 1
			}
			return 0
		})

		allTags = make([]Tag, 0, len(dbTags))
		tagRanges = make(map[int64]indexRange)

		var currentSID int64 = -1
		start := 0

		for _, t := range dbTags {
			sid := t.StatusID

			if sid != currentSID {
				if currentSID != -1 {
					tagRanges[currentSID] = indexRange{start: start, end: len(allTags)}
				}
				currentSID = sid
				start = len(allTags)
			}

			allTags = append(allTags, Tag{
				Name: t.Name,
				URL:  "https://" + cfg.Instance.LocalDomain + "/tags/" + t.Name, // Construct local tag URL
			})
		}
		if currentSID != -1 {
			tagRanges[currentSID] = indexRange{start: start, end: len(allTags)}
		}
	}

	var statsMap map[int64]db.StatusStat
	if len(dbStats) > 0 {
		statsMap = make(map[int64]db.StatusStat)
		for _, s := range dbStats {
			statsMap[s.StatusID] = s
		}
	}

	mutedConversations := make(map[int64]bool)
	if hasAccount && len(conversationIDs) > 0 {
		mutes, err := q.ListConversationMutes(ctx, db.ListConversationMutesParams{
			AccountID:       accountID,
			ConversationIds: conversationIDs,
		})
		if err == nil {
			for _, cid := range mutes {
				mutedConversations[cid] = true
			}
		}
	}

	var previewCardsMap map[int64]*PreviewCard
	if rows, err := q.GetPreviewCardsByStatusIDs(ctx, statusIDs); err == nil && len(rows) > 0 {
		previewCardsMap = make(map[int64]*PreviewCard)
		authorAccountIDs := make(map[int64]struct{})
		for _, row := range rows {
			if row.AuthorAccountID.Valid {
				authorAccountIDs[row.AuthorAccountID.Int64] = struct{}{}
			}
		}
		authorAccounts := map[int64]Account{}
		if len(authorAccountIDs) > 0 {
			ids := make([]int64, 0, len(authorAccountIDs))
			for id := range authorAccountIDs {
				ids = append(ids, id)
			}
			if dbAccounts, err := q.GetAccountsByIDs(ctx, ids); err == nil {
				authorAccounts = serializeAccounts(ctx, q, cfg, dbAccounts, true)
			}
		}

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
			previewCardsMap[row.StatusID] = serializePreviewCard(card, authorAccounts)
		}
	}

	// Optimization: Use lazy map initialization for quotes.
	var quotesMap map[int64]db.Quote
	var quotedStatusesMap map[int64]Status
	if opts.includeQuotes {
		// Fetch quotes for statuses (quotes where these statuses are the quoting status)
		dbQuotes, _ := q.GetQuotesByStatusIDs(ctx, statusIDs)
		if len(dbQuotes) > 0 {
			quotesMap = make(map[int64]db.Quote)
			quotedStatusIDs := make([]int64, 0)
			for _, qr := range dbQuotes {
				quotesMap[qr.StatusID] = qr
				if qr.QuotedStatusID.Valid {
					quotedStatusIDs = append(quotedStatusIDs, qr.QuotedStatusID.Int64)
				}
			}

			if len(quotedStatusIDs) > 0 {
				if quotedDbStatuses, err := q.GetStatusesByIDs(ctx, quotedStatusIDs); err == nil {
					serialized := serializeStatusesInternal(ctx, q, cfg, quotedDbStatuses, statusSerializeOptions{
						includeReblogs: true,
						includeQuotes:  false,
					})
					if len(serialized) > 0 {
						quotedStatusesMap = make(map[int64]Status)
						for _, status := range serialized {
							if statusID, err := strconv.ParseInt(status.ID, 10, 64); err == nil {
								quotedStatusesMap[statusID] = status
							}
						}
					}
				}
			}
		}
	}

	relationships := buildStatusRelationships(ctx, q, accountID, dbStatuses)

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

		var mediaAttachments []MediaAttach
		if rng, ok := mediaRanges[dbStatus.ID]; ok {
			mediaAttachments = allMedia[rng.start:rng.end]
		} else {
			mediaAttachments = []MediaAttach{}
		}

		var mentions []Mention
		if rng, ok := mentionRanges[dbStatus.ID]; ok {
			mentions = allMentions[rng.start:rng.end]
		} else {
			mentions = []Mention{}
		}

		var statusTags []Tag
		if rng, ok := tagRanges[dbStatus.ID]; ok {
			statusTags = allTags[rng.start:rng.end]
		} else {
			statusTags = []Tag{}
		}

		emojis := statusEmojis[dbStatus.ID]
		if emojis == nil {
			emojis = []CustomEmoji{}
		}

		var quoteStatus *Status
		var quoteIDStr *string
		if qr, hasQuote := quotesMap[dbStatus.ID]; hasQuote && qr.QuotedStatusID.Valid {
			idStr := strconv.FormatInt(qr.QuotedStatusID.Int64, 10)
			quoteIDStr = &idStr
			if quotedStatus, ok := quotedStatusesMap[qr.QuotedStatusID.Int64]; ok {
				quoteCopy := quotedStatus
				quoteStatus = &quoteCopy
			}
		}

		content := formatStatusContent(cfg, dbStatus, statusContentOptions{
			author:   &account,
			mentions: mentions,
			tags:     statusTags,
			quote:    quoteStatus,
		})

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

		var editedAt *time.Time
		if dbStatus.EditedAt.Valid {
			s := dbStatus.EditedAt.Time
			editedAt = &s
		}

		muted := false
		if dbStatus.ConversationID.Valid {
			muted = mutedConversations[dbStatus.ConversationID.Int64]
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
			EditedAt:           editedAt,
			Muted:              muted,
			Content:            content,
			Reblog:             nil,
			Account:            account,
			MediaAttachments:   mediaAttachments,
			Mentions:           mentions,
			Tags:               statusTags,
			Emojis:             emojis,
			Quote:              quoteStatus,
			QuoteID:            quoteIDStr,
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

		if opts.includeReblogs && dbStatus.ReblogOfID.Valid {
			if reblog, ok := rebloggedStatuses[dbStatus.ReblogOfID.Int64]; ok {
				s.Reblog = &reblog
			}
		}

		applyStatusRelationships(&s, dbStatus, relationships, accountID, hasAccount)

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
func SerializeStatusSimple(ctx context.Context, q statusSerializerQuerier, cfg *config.Config, dbStatus db.Status, preFetchedAccount *Account) Status {
	// Get account
	var account Account
	if preFetchedAccount != nil {
		account = *preFetchedAccount
	} else {
		account = Account{ID: strconv.FormatInt(dbStatus.AccountID, 10), Username: "unknown"}
		if acc, err := q.GetAccountByID(ctx, dbStatus.AccountID); err == nil {
			if serialized := serializeAccounts(ctx, q, cfg, []db.Account{acc}, true); serialized != nil {
				if apiAcc, ok := serialized[acc.ID]; ok {
					account = apiAcc
				}
			}
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

	var editedAt *time.Time
	if dbStatus.EditedAt.Valid {
		s := dbStatus.EditedAt.Time
		editedAt = &s
	}

	emojis := []CustomEmoji{}
	if q != nil {
		statusEmojis := buildStatusEmojis(ctx, q, []db.Status{dbStatus}, map[int64]Account{
			dbStatus.AccountID: account,
		})
		if list, ok := statusEmojis[dbStatus.ID]; ok {
			emojis = list
		}
	}

	content := formatStatusText(cfg, !dbStatus.Local.Valid || dbStatus.Local.Bool, dbStatus.Text, statusContentOptions{
		author: &account,
	})

	return Status{
		ID:               strconv.FormatInt(dbStatus.ID, 10),
		CreatedAt:        dbStatus.CreatedAt.Time,
		Sensitive:        dbStatus.Sensitive,
		SpoilerText:      dbStatus.SpoilerText,
		Visibility:       visibility,
		Language:         language,
		URI:              dbStatus.Uri.String,
		URL:              &dbStatus.Url.String,
		EditedAt:         editedAt,
		Content:          content,
		Account:          account,
		MediaAttachments: []MediaAttach{},
		Mentions:         []Mention{},
		Tags:             []Tag{},
		Emojis:           emojis,
	}
}

func serializePreviewCard(card db.PreviewCard, authorAccounts map[int64]Account) *PreviewCard {
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

	var language *string
	if card.Language.Valid {
		value := card.Language.String
		language = &value
	}

	var publishedAt *time.Time
	if card.PublishedAt.Valid {
		value := card.PublishedAt.Time
		publishedAt = &value
	}

	authors := []PreviewCardAuthor{}
	if strings.TrimSpace(card.AuthorName) != "" || strings.TrimSpace(card.AuthorUrl) != "" || card.AuthorAccountID.Valid {
		author := PreviewCardAuthor{
			Name: card.AuthorName,
			URL:  card.AuthorUrl,
		}
		if card.AuthorAccountID.Valid && authorAccounts != nil {
			if acc, ok := authorAccounts[card.AuthorAccountID.Int64]; ok {
				author.Account = &acc
			}
		}
		authors = append(authors, author)
	}

	return &PreviewCard{
		URL:              card.Url,
		Title:            card.Title,
		Description:      card.Description,
		Language:         language,
		Type:             previewCardType(card.Type),
		AuthorName:       card.AuthorName,
		AuthorURL:        card.AuthorUrl,
		ProviderName:     card.ProviderName,
		ProviderURL:      card.ProviderUrl,
		HTML:             card.Html,
		Width:            int(card.Width),
		Height:           int(card.Height),
		Image:            imageURL,
		ImageDescription: card.ImageDescription,
		EmbedURL:         card.EmbedUrl,
		Blurhash:         blurhash,
		PublishedAt:      publishedAt,
		Authors:          authors,
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
