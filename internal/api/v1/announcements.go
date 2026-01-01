// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/streaming"
	"github.com/deicod/gotodon/internal/web"
)

// AnnouncementsHandler handles announcement endpoints
type AnnouncementsHandler struct {
	queries   announcementQueries
	cfg       *config.Config
	publisher streaming.EventPublisher
}

// NewAnnouncementsHandler creates a new announcements handler
func NewAnnouncementsHandler(queries announcementQueries, cfg *config.Config) *AnnouncementsHandler {
	return &AnnouncementsHandler{queries: queries, cfg: cfg}
}

// SetPublisher sets the streaming publisher for announcement events.
func (h *AnnouncementsHandler) SetPublisher(publisher streaming.EventPublisher) {
	h.publisher = publisher
}

// Announcement represents a server announcement
type Announcement struct {
	ID          string                 `json:"id"`
	Content     string                 `json:"content"`
	StartsAt    *time.Time             `json:"starts_at"`
	EndsAt      *time.Time             `json:"ends_at"`
	AllDay      bool                   `json:"all_day"`
	PublishedAt time.Time              `json:"published_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
	Published   bool                   `json:"published"`
	Read        bool                   `json:"read"`
	Reactions   []AnnouncementReaction `json:"reactions"`
	Mentions    []Mention              `json:"mentions"`
	Statuses    []AnnouncementStatus   `json:"statuses"`
	Tags        []Tag                  `json:"tags"`
	Emojis      []CustomEmoji          `json:"emojis"`
}

// AnnouncementReaction represents a reaction to an announcement
type AnnouncementReaction struct {
	Name      string `json:"name"`
	Count     int    `json:"count"`
	Me        bool   `json:"me"`
	URL       string `json:"url,omitempty"`
	StaticURL string `json:"static_url,omitempty"`
}

// AnnouncementStatus represents a status referenced in an announcement
type AnnouncementStatus struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

type announcementQueries interface {
	ListAnnouncements(ctx context.Context, arg db.ListAnnouncementsParams) ([]db.Announcement, error)
	ListAnnouncementReactions(ctx context.Context, announcementID int64) ([]db.AnnouncementReaction, error)
	AddAnnouncementReaction(ctx context.Context, arg db.AddAnnouncementReactionParams) (db.AnnouncementReaction, error)
	RemoveAnnouncementReaction(ctx context.Context, arg db.RemoveAnnouncementReactionParams) error
	DismissAnnouncement(ctx context.Context, arg db.DismissAnnouncementParams) error
	GetStatusesByIDs(ctx context.Context, ids []int64) ([]db.Status, error)
	GetAccountsByIDs(ctx context.Context, ids []int64) ([]db.Account, error)
	GetAccountsByUsernames(ctx context.Context, usernames []string) ([]db.Account, error)
	GetAccountsByUsernamesAndDomains(ctx context.Context, arg db.GetAccountsByUsernamesAndDomainsParams) ([]db.Account, error)
	ListCustomEmojisByShortcodesAndDomain(ctx context.Context, arg db.ListCustomEmojisByShortcodesAndDomainParams) ([]db.CustomEmoji, error)
	ListCustomEmojisByIDs(ctx context.Context, ids []int64) ([]db.CustomEmoji, error)
}

type announcementMentionRef struct {
	username string
	domain   string
}

type announcementParsed struct {
	mentions   []announcementMentionRef
	tags       []string
	shortcodes []string
	statusIDs  []int64
}

// GetAll handles GET /api/v1/announcements
func (h *AnnouncementsHandler) GetAll(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		// Announcements can be public? Usually authenticated in Mastodon
		web.JSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	withDismissed := web.QueryParam(r, "with_dismissed") == "true"

	if h.queries == nil {
		web.JSON(w, http.StatusOK, []Announcement{})
		return
	}

	anns, err := h.queries.ListAnnouncements(r.Context(), db.ListAnnouncementsParams{
		AccountID:     accountID,
		WithDismissed: withDismissed,
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	web.JSON(w, http.StatusOK, buildAnnouncements(r.Context(), h.queries, h.cfg, accountID, anns))
}

// Dismiss handles POST /api/v1/announcements/:id/dismiss
func (h *AnnouncementsHandler) Dismiss(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	id, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid ID")
		return
	}

	if h.queries != nil {
		err := h.queries.DismissAnnouncement(r.Context(), db.DismissAnnouncementParams{
			AccountID:      accountID,
			AnnouncementID: id,
		})
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// AddReaction handles PUT /api/v1/announcements/:id/reactions/:name
func (h *AnnouncementsHandler) AddReaction(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	id, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid ID")
		return
	}
	name := web.Param(r, "name")

	if h.queries != nil {
		_, err := h.queries.AddAnnouncementReaction(r.Context(), db.AddAnnouncementReactionParams{
			AccountID:      pgtype.Int8{Int64: accountID, Valid: true},
			AnnouncementID: id,
			Name:           name,
		})
		if err != nil {
			// Ignore dup error? or just return OK
			_ = err
		}
	}

	web.JSON(w, http.StatusOK, map[string]any{})
	h.publishAnnouncementReaction(r.Context(), id, name)
}

// RemoveReaction handles DELETE /api/v1/announcements/:id/reactions/:name
func (h *AnnouncementsHandler) RemoveReaction(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	id, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid ID")
		return
	}
	name := web.Param(r, "name")

	if h.queries != nil {
		_ = h.queries.RemoveAnnouncementReaction(r.Context(), db.RemoveAnnouncementReactionParams{
			AccountID:      pgtype.Int8{Int64: accountID, Valid: true},
			AnnouncementID: id,
			Name:           name,
		})
	}

	web.JSON(w, http.StatusOK, map[string]any{})
	h.publishAnnouncementReaction(r.Context(), id, name)
}

func buildAnnouncements(ctx context.Context, q announcementQueries, cfg *config.Config, accountID int64, anns []db.Announcement) []Announcement {
	if len(anns) == 0 {
		return []Announcement{}
	}

	publicDomain := instancePublicDomain(cfg)
	parsed := make(map[int64]announcementParsed, len(anns))

	statusIDSet := make(map[int64]struct{})
	statusIDs := make([]int64, 0)
	localUsernames := make([]string, 0)
	remoteUsernames := make([]string, 0)
	remoteDomains := make([]string, 0)
	localSeen := make(map[string]struct{})
	remoteSeen := make(map[string]struct{})
	shortcodeSeen := make(map[string]struct{})
	shortcodes := make([]string, 0)

	for _, ann := range anns {
		mentions := parseAnnouncementMentions(ann.Text)
		tags := parseAnnouncementTags(ann.Text)
		announcementShortcodes := parseAnnouncementShortcodes(ann.Text)

		parsed[ann.ID] = announcementParsed{
			mentions:   mentions,
			tags:       tags,
			shortcodes: announcementShortcodes,
			statusIDs:  ann.StatusIds,
		}

		for _, id := range ann.StatusIds {
			if _, ok := statusIDSet[id]; ok {
				continue
			}
			statusIDSet[id] = struct{}{}
			statusIDs = append(statusIDs, id)
		}

		for _, mention := range mentions {
			if strings.TrimSpace(mention.username) == "" {
				continue
			}
			if strings.TrimSpace(mention.domain) == "" {
				key := strings.ToLower(mention.username)
				if _, ok := localSeen[key]; ok {
					continue
				}
				localSeen[key] = struct{}{}
				localUsernames = append(localUsernames, key)
				continue
			}
			key := mentionKey(mention.username, mention.domain)
			if _, ok := remoteSeen[key]; ok {
				continue
			}
			remoteSeen[key] = struct{}{}
			remoteUsernames = append(remoteUsernames, strings.ToLower(mention.username))
			remoteDomains = append(remoteDomains, strings.ToLower(mention.domain))
		}

		for _, shortcode := range announcementShortcodes {
			if _, ok := shortcodeSeen[shortcode]; ok {
				continue
			}
			shortcodeSeen[shortcode] = struct{}{}
			shortcodes = append(shortcodes, shortcode)
		}
	}

	mentionAccounts := map[string]Account{}
	if len(localUsernames) > 0 {
		if accounts, err := q.GetAccountsByUsernames(ctx, localUsernames); err == nil {
			for _, acc := range accounts {
				mentionAccounts[mentionKey(acc.Username, "")] = SerializeAccount(cfg, acc)
			}
		}
	}
	if len(remoteUsernames) > 0 {
		accounts, err := q.GetAccountsByUsernamesAndDomains(ctx, db.GetAccountsByUsernamesAndDomainsParams{
			Usernames: remoteUsernames,
			Domains:   remoteDomains,
		})
		if err == nil {
			for _, acc := range accounts {
				domain := ""
				if acc.Domain.Valid {
					domain = acc.Domain.String
				}
				mentionAccounts[mentionKey(acc.Username, domain)] = SerializeAccount(cfg, acc)
			}
		}
	}

	emojiByShortcode := map[string]db.CustomEmoji{}
	if len(shortcodes) > 0 {
		emojis, err := q.ListCustomEmojisByShortcodesAndDomain(ctx, db.ListCustomEmojisByShortcodesAndDomainParams{
			Shortcodes: shortcodes,
			Domain:     "",
		})
		if err == nil {
			for _, emoji := range emojis {
				emojiByShortcode[emoji.Shortcode] = emoji
			}
		}
	}

	statusesByID := map[int64]db.Status{}
	statusAccountIDs := make(map[int64]struct{})
	statusAccountList := make([]int64, 0)
	if len(statusIDs) > 0 {
		statuses, err := q.GetStatusesByIDs(ctx, statusIDs)
		if err == nil {
			for _, status := range statuses {
				statusesByID[status.ID] = status
				if _, ok := statusAccountIDs[status.AccountID]; ok {
					continue
				}
				statusAccountIDs[status.AccountID] = struct{}{}
				statusAccountList = append(statusAccountList, status.AccountID)
			}
		}
	}

	statusAccounts := map[int64]db.Account{}
	if len(statusAccountList) > 0 {
		accounts, err := q.GetAccountsByIDs(ctx, statusAccountList)
		if err == nil {
			for _, acc := range accounts {
				statusAccounts[acc.ID] = acc
			}
		}
	}

	reactionRows := make(map[int64][]db.AnnouncementReaction)
	reactionEmojiIDs := make(map[int64]struct{})
	reactionEmojiList := make([]int64, 0)
	for _, ann := range anns {
		reactions, err := q.ListAnnouncementReactions(ctx, ann.ID)
		if err != nil {
			continue
		}
		reactionRows[ann.ID] = reactions
		for _, reaction := range reactions {
			if !reaction.CustomEmojiID.Valid {
				continue
			}
			emojiID := reaction.CustomEmojiID.Int64
			if _, ok := reactionEmojiIDs[emojiID]; ok {
				continue
			}
			reactionEmojiIDs[emojiID] = struct{}{}
			reactionEmojiList = append(reactionEmojiList, emojiID)
		}
	}

	reactionEmojiByID := map[int64]db.CustomEmoji{}
	if len(reactionEmojiList) > 0 {
		emojis, err := q.ListCustomEmojisByIDs(ctx, reactionEmojiList)
		if err == nil {
			for _, emoji := range emojis {
				reactionEmojiByID[emoji.ID] = emoji
			}
		}
	}

	result := make([]Announcement, len(anns))
	for i, ann := range anns {
		var startsAt *time.Time
		if ann.StartsAt.Valid {
			t := ann.StartsAt.Time
			startsAt = &t
		}
		var endsAt *time.Time
		if ann.EndsAt.Valid {
			t := ann.EndsAt.Time
			endsAt = &t
		}
		publishedAt := ann.CreatedAt.Time
		if ann.PublishedAt.Valid {
			publishedAt = ann.PublishedAt.Time
		}

		parsedAnn := parsed[ann.ID]

		result[i] = Announcement{
			ID:          strconv.FormatInt(ann.ID, 10),
			Content:     ann.Text,
			Published:   ann.Published,
			AllDay:      ann.AllDay,
			PublishedAt: publishedAt,
			UpdatedAt:   ann.UpdatedAt.Time,
			StartsAt:    startsAt,
			EndsAt:      endsAt,
			Reactions:   buildAnnouncementReactions(reactionRows[ann.ID], accountID, reactionEmojiByID),
			Statuses:    buildAnnouncementStatuses(cfg, parsedAnn.statusIDs, statusesByID, statusAccounts),
			Tags:        buildAnnouncementTags(parsedAnn.tags, publicDomain),
			Emojis:      buildAnnouncementEmojis(parsedAnn.shortcodes, emojiByShortcode),
			Mentions:    buildAnnouncementMentions(parsedAnn.mentions, mentionAccounts),
		}
	}

	return result
}

func parseAnnouncementMentions(text string) []announcementMentionRef {
	matches := mentionRegex.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return []announcementMentionRef{}
	}

	mentions := make([]announcementMentionRef, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))

	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		username := match[1]
		if username == "" {
			continue
		}
		domain := ""
		if len(match) > 2 {
			domain = match[2]
		}
		key := mentionKey(username, domain)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		mentions = append(mentions, announcementMentionRef{
			username: username,
			domain:   domain,
		})
	}

	return mentions
}

func parseAnnouncementTags(text string) []string {
	matches := tagRegex.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return []string{}
	}

	tags := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		tag := strings.ToLower(match[1])
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		tags = append(tags, tag)
	}

	return tags
}

func parseAnnouncementShortcodes(text string) []string {
	shortcodes := make([]string, 0)
	seen := make(map[string]struct{})
	appendShortcodes(text, seen, &shortcodes)
	return shortcodes
}

func mentionKey(username, domain string) string {
	return strings.ToLower(username) + "|" + strings.ToLower(domain)
}

func buildAnnouncementMentions(mentions []announcementMentionRef, accounts map[string]Account) []Mention {
	if len(mentions) == 0 || len(accounts) == 0 {
		return []Mention{}
	}

	out := make([]Mention, 0, len(mentions))
	for _, mention := range mentions {
		acc, ok := accounts[mentionKey(mention.username, mention.domain)]
		if !ok {
			continue
		}
		out = append(out, Mention{
			ID:       acc.ID,
			Username: acc.Username,
			Acct:     acc.Acct,
			URL:      acc.URL,
		})
	}

	return out
}

func buildAnnouncementTags(tags []string, publicDomain string) []Tag {
	if len(tags) == 0 {
		return []Tag{}
	}

	out := make([]Tag, 0, len(tags))
	for _, tag := range tags {
		out = append(out, Tag{
			Name: tag,
			URL:  tagURL(publicDomain, tag),
		})
	}

	return out
}

func buildAnnouncementEmojis(shortcodes []string, emojiByShortcode map[string]db.CustomEmoji) []CustomEmoji {
	if len(shortcodes) == 0 || len(emojiByShortcode) == 0 {
		return []CustomEmoji{}
	}

	out := make([]CustomEmoji, 0, len(shortcodes))
	for _, shortcode := range shortcodes {
		emoji, ok := emojiByShortcode[shortcode]
		if !ok {
			continue
		}
		out = append(out, CustomEmoji{
			Shortcode:       emoji.Shortcode,
			URL:             GetEmojiURL(emoji),
			StaticURL:       GetEmojiURL(emoji),
			VisibleInPicker: emoji.VisibleInPicker,
		})
	}

	return out
}

func buildAnnouncementStatuses(cfg *config.Config, statusIDs []int64, statuses map[int64]db.Status, accounts map[int64]db.Account) []AnnouncementStatus {
	if len(statusIDs) == 0 || len(statuses) == 0 {
		return []AnnouncementStatus{}
	}

	out := make([]AnnouncementStatus, 0, len(statusIDs))
	for _, statusID := range statusIDs {
		status, ok := statuses[statusID]
		if !ok {
			continue
		}
		account := accounts[status.AccountID]
		out = append(out, AnnouncementStatus{
			ID:  strconv.FormatInt(status.ID, 10),
			URL: announcementStatusURL(cfg, status, account),
		})
	}

	return out
}

func announcementStatusURL(cfg *config.Config, status db.Status, account db.Account) string {
	if status.Url.Valid && strings.TrimSpace(status.Url.String) != "" {
		return strings.TrimSpace(status.Url.String)
	}

	publicDomain := instancePublicDomain(cfg)
	if publicDomain != "" && account.Username != "" {
		if !account.Domain.Valid || strings.TrimSpace(account.Domain.String) == "" {
			return "https://" + publicDomain + "/@" + account.Username + "/" + strconv.FormatInt(status.ID, 10)
		}
	}

	if status.Uri.Valid && strings.TrimSpace(status.Uri.String) != "" {
		return strings.TrimSpace(status.Uri.String)
	}

	return ""
}

func buildAnnouncementReactions(reactions []db.AnnouncementReaction, accountID int64, emojiByID map[int64]db.CustomEmoji) []AnnouncementReaction {
	if len(reactions) == 0 {
		return []AnnouncementReaction{}
	}

	type reactionState struct {
		count         int
		me            bool
		customEmojiID *int64
	}

	stateByName := make(map[string]*reactionState)
	for _, reaction := range reactions {
		state, ok := stateByName[reaction.Name]
		if !ok {
			state = &reactionState{}
			stateByName[reaction.Name] = state
		}
		state.count++
		if reaction.AccountID.Valid && reaction.AccountID.Int64 == accountID {
			state.me = true
		}
		if state.customEmojiID == nil && reaction.CustomEmojiID.Valid {
			id := reaction.CustomEmojiID.Int64
			state.customEmojiID = &id
		}
	}

	names := make([]string, 0, len(stateByName))
	for name := range stateByName {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]AnnouncementReaction, 0, len(names))
	for _, name := range names {
		state := stateByName[name]
		apiReaction := AnnouncementReaction{
			Name:  name,
			Count: state.count,
			Me:    state.me,
		}
		if state.customEmojiID != nil {
			if emoji, ok := emojiByID[*state.customEmojiID]; ok {
				apiReaction.URL = GetEmojiURL(emoji)
				apiReaction.StaticURL = GetEmojiURL(emoji)
			}
		}
		out = append(out, apiReaction)
	}

	return out
}

func tagURL(publicDomain, name string) string {
	if publicDomain == "" {
		return "/tags/" + name
	}
	return "https://" + publicDomain + "/tags/" + name
}

func (h *AnnouncementsHandler) publishAnnouncementReaction(ctx context.Context, announcementID int64, name string) {
	if h == nil || h.publisher == nil || h.queries == nil || announcementID <= 0 || strings.TrimSpace(name) == "" {
		return
	}

	reactions, err := h.queries.ListAnnouncementReactions(ctx, announcementID)
	if err != nil {
		return
	}

	count := 0
	var emojiID *int64
	for _, reaction := range reactions {
		if reaction.Name != name {
			continue
		}
		count++
		if emojiID == nil && reaction.CustomEmojiID.Valid {
			id := reaction.CustomEmojiID.Int64
			emojiID = &id
		}
	}

	payload := map[string]any{
		"name":            name,
		"count":           count,
		"me":              false,
		"announcement_id": strconv.FormatInt(announcementID, 10),
	}

	if emojiID != nil {
		if emojis, err := h.queries.ListCustomEmojisByIDs(ctx, []int64{*emojiID}); err == nil && len(emojis) > 0 {
			url := GetEmojiURL(emojis[0])
			payload["url"] = url
			payload["static_url"] = url
		}
	}

	_ = h.publisher.PublishEventToRedis(ctx, streaming.EventAnnouncementReaction, payload, []string{streaming.StreamUser})
}

// RegisterRoutes registers announcement routes
func (h *AnnouncementsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/announcements", h.GetAll)
	mux.HandleFunc("POST /api/v1/announcements/{id}/dismiss", h.Dismiss)
	mux.HandleFunc("PUT /api/v1/announcements/{id}/reactions/{name}", h.AddReaction)
	mux.HandleFunc("DELETE /api/v1/announcements/{id}/reactions/{name}", h.RemoveReaction)
}
