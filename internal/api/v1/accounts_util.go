package v1

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/idna"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

type accountSerializeOptions struct {
	Stats        *db.AccountStat
	Fields       []Field
	Emojis       []CustomEmoji
	Mentions     []Mention
	NoIndex      *bool
	Roles        *[]AccountRoleSummary
	Moved        *Account
	IncludeMoved bool
}

// AccountDataQuerier exposes the account data needed for full serialization.
type AccountDataQuerier interface {
	ListAccountStatsByAccountIDs(ctx context.Context, accountIds []int64) ([]db.AccountStat, error)
	ListUserSettingsByAccountIDs(ctx context.Context, accountIds []int64) ([]db.ListUserSettingsByAccountIDsRow, error)
	ListUserRolesByAccountIDs(ctx context.Context, accountIds []int64) ([]db.ListUserRolesByAccountIDsRow, error)
	ListCustomEmojisByShortcodesAndDomain(ctx context.Context, arg db.ListCustomEmojisByShortcodesAndDomainParams) ([]db.CustomEmoji, error)
	GetAccountsByIDs(ctx context.Context, ids []int64) ([]db.Account, error)
	GetAccountsByUsernames(ctx context.Context, usernames []string) ([]db.Account, error)
	GetAccountsByUsernamesAndDomains(ctx context.Context, arg db.GetAccountsByUsernamesAndDomainsParams) ([]db.Account, error)
}

type accountCredentialQueries interface {
	AccountDataQuerier
	GetUserByAccountID(ctx context.Context, accountID int64) (db.User, error)
	CountFollowRequestsForAccount(ctx context.Context, targetAccountID int64) (int64, error)
	GetUserRole(ctx context.Context, userID int64) (db.UserRole, error)
}

// SerializeAccount converts a db.Account to an API Account.
func SerializeAccount(cfg *config.Config, dbAcc db.Account) Account {
	return serializeAccount(cfg, dbAcc, accountSerializeOptions{})
}

// SerializeAccounts converts a slice of db.Account records to API accounts with full stats/fields/emojis.
func SerializeAccounts(ctx context.Context, q AccountDataQuerier, cfg *config.Config, dbAccounts []db.Account, includeMoved bool) map[int64]Account {
	return serializeAccounts(ctx, q, cfg, dbAccounts, includeMoved)
}

func serializeAccounts(ctx context.Context, q AccountDataQuerier, cfg *config.Config, dbAccounts []db.Account, includeMoved bool) map[int64]Account {
	accounts := make(map[int64]Account, len(dbAccounts))
	if len(dbAccounts) == 0 {
		return accounts
	}

	accountIDs := make([]int64, 0, len(dbAccounts))
	fieldsByID := make(map[int64][]Field, len(dbAccounts))
	shortcodesByID := make(map[int64][]string, len(dbAccounts))
	domainShortcodes := make(map[string]map[string]struct{})
	noteMentionsByID := make(map[int64][]mentionToken, len(dbAccounts))
	localMentionUsernames := make(map[string]struct{})
	remoteMentionTokens := make(map[string]mentionToken)

	for _, acc := range dbAccounts {
		accountIDs = append(accountIDs, acc.ID)
		fields := parseAccountFields(acc.Fields)
		fieldsByID[acc.ID] = fields

		if acc.SuspendedAt.Valid {
			continue
		}

		if accountIsLocal(acc) {
			tokens := extractMentionTokens(acc.Note, cfg)
			if len(tokens) > 0 {
				noteMentionsByID[acc.ID] = tokens
				for _, token := range tokens {
					if token.Domain == "" {
						localMentionUsernames[token.Username] = struct{}{}
					} else {
						remoteMentionTokens[statusMentionKey(token.Username, token.Domain)] = token
					}
				}
			}
		}

		shortcodes := accountShortcodes(acc, fields)
		shortcodesByID[acc.ID] = shortcodes
		if len(shortcodes) == 0 {
			continue
		}

		domain := accountEmojiDomain(acc)
		if _, ok := domainShortcodes[domain]; !ok {
			domainShortcodes[domain] = map[string]struct{}{}
		}
		for _, shortcode := range shortcodes {
			domainShortcodes[domain][shortcode] = struct{}{}
		}
	}

	statsByID := make(map[int64]db.AccountStat)
	if q != nil {
		if stats, err := q.ListAccountStatsByAccountIDs(ctx, accountIDs); err == nil {
			for _, stat := range stats {
				statsByID[stat.AccountID] = stat
			}
		}
	}

	noindexByID := make(map[int64]*bool)
	if q != nil {
		if rows, err := q.ListUserSettingsByAccountIDs(ctx, accountIDs); err == nil {
			for _, row := range rows {
				settings := settingsFromText(row.Settings)
				if value, ok := settingsBool(settings, "noindex"); ok {
					v := value
					noindexByID[row.AccountID] = &v
				}
			}
		}
	}

	rolesByID := make(map[int64][]AccountRoleSummary)
	if q != nil {
		if rows, err := q.ListUserRolesByAccountIDs(ctx, accountIDs); err == nil {
			for _, row := range rows {
				if !row.Highlighted {
					continue
				}
				rolesByID[row.AccountID] = append(rolesByID[row.AccountID], AccountRoleSummary{
					ID:    strconv.FormatInt(row.ID, 10),
					Name:  row.Name,
					Color: row.Color,
				})
			}
		}
	}

	emojisByDomain := make(map[string]map[string]CustomEmoji)
	if q != nil {
		for domain, shortcodes := range domainShortcodes {
			if len(shortcodes) == 0 {
				continue
			}

			list := make([]string, 0, len(shortcodes))
			for shortcode := range shortcodes {
				list = append(list, shortcode)
			}

			rows, err := q.ListCustomEmojisByShortcodesAndDomain(ctx, db.ListCustomEmojisByShortcodesAndDomainParams{
				Shortcodes: list,
				Domain:     domain,
			})
			if err != nil {
				continue
			}

			emojiMap := make(map[string]CustomEmoji, len(rows))
			for _, emoji := range rows {
				emojiMap[emoji.Shortcode] = CustomEmoji{
					Shortcode:       emoji.Shortcode,
					URL:             GetEmojiURL(emoji),
					StaticURL:       GetEmojiURL(emoji),
					VisibleInPicker: emoji.VisibleInPicker,
				}
			}
			emojisByDomain[domain] = emojiMap
		}
	}

	emojisByID := make(map[int64][]CustomEmoji, len(dbAccounts))
	for _, acc := range dbAccounts {
		shortcodes := shortcodesByID[acc.ID]
		if len(shortcodes) == 0 {
			emojisByID[acc.ID] = []CustomEmoji{}
			continue
		}
		domain := accountEmojiDomain(acc)
		lookup := emojisByDomain[domain]
		resolved := make([]CustomEmoji, 0, len(shortcodes))
		for _, shortcode := range shortcodes {
			if emoji, ok := lookup[shortcode]; ok {
				resolved = append(resolved, emoji)
			}
		}
		emojisByID[acc.ID] = resolved
	}

	mentionsByKey := make(map[string]Mention)
	if q != nil {
		if len(localMentionUsernames) > 0 {
			usernames := make([]string, 0, len(localMentionUsernames))
			for username := range localMentionUsernames {
				usernames = append(usernames, username)
			}
			if accounts, err := q.GetAccountsByUsernames(ctx, usernames); err == nil {
				addMentionAccounts(cfg, mentionsByKey, accounts)
			}
		}
		if len(remoteMentionTokens) > 0 {
			usernames := make([]string, 0, len(remoteMentionTokens))
			domains := make([]string, 0, len(remoteMentionTokens))
			for _, token := range remoteMentionTokens {
				usernames = append(usernames, token.Username)
				domains = append(domains, token.Domain)
			}
			if accounts, err := q.GetAccountsByUsernamesAndDomains(ctx, db.GetAccountsByUsernamesAndDomainsParams{
				Usernames: usernames,
				Domains:   domains,
			}); err == nil {
				addMentionAccounts(cfg, mentionsByKey, accounts)
			}
		}
	}

	movedByID := make(map[int64]*Account)
	if includeMoved && q != nil {
		movedIDs := make(map[int64]struct{})
		for _, acc := range dbAccounts {
			if acc.MovedToAccountID.Valid {
				movedIDs[acc.MovedToAccountID.Int64] = struct{}{}
			}
		}

		if len(movedIDs) > 0 {
			ids := make([]int64, 0, len(movedIDs))
			for id := range movedIDs {
				ids = append(ids, id)
			}

			movedAccounts, err := q.GetAccountsByIDs(ctx, ids)
			if err == nil {
				movedSerialized := serializeAccounts(ctx, q, cfg, movedAccounts, false)
				for _, acc := range dbAccounts {
					if acc.MovedToAccountID.Valid {
						if moved, ok := movedSerialized[acc.MovedToAccountID.Int64]; ok {
							copy := moved
							movedByID[acc.ID] = &copy
						}
					}
				}
			}
		}
	}

	for _, acc := range dbAccounts {
		opts := accountSerializeOptions{IncludeMoved: includeMoved}
		if stat, ok := statsByID[acc.ID]; ok {
			opts.Stats = &stat
		}
		opts.Fields = fieldsByID[acc.ID]
		opts.Emojis = emojisByID[acc.ID]
		if tokens, ok := noteMentionsByID[acc.ID]; ok {
			opts.Mentions = mentionsFromTokens(tokens, mentionsByKey)
		}
		if noindex, ok := noindexByID[acc.ID]; ok {
			opts.NoIndex = noindex
		}
		if roles, ok := rolesByID[acc.ID]; ok {
			roleCopy := roles
			opts.Roles = &roleCopy
		}
		if moved, ok := movedByID[acc.ID]; ok {
			opts.Moved = moved
		}

		accounts[acc.ID] = serializeAccount(cfg, acc, opts)
	}

	return accounts
}

func serializeAccount(cfg *config.Config, dbAcc db.Account, opts accountSerializeOptions) Account {
	createdAt := time.Now().UTC()
	if dbAcc.CreatedAt.Valid {
		createdAt = midnightUTC(dbAcc.CreatedAt.Time)
	}

	isLocal := accountIsLocal(dbAcc)
	publicDomain := accountPublicDomain(cfg)
	federationDomain := accountFederationDomain(cfg, publicDomain)

	acct := dbAcc.Username
	if !isLocal {
		domain := strings.TrimSpace(dbAcc.Domain.String)
		if domain != "" {
			if unicodeDomain, err := idna.Lookup.ToUnicode(domain); err == nil && unicodeDomain != "" {
				domain = unicodeDomain
			}
			acct = dbAcc.Username + "@" + domain
		}
	}

	url := dbAcc.Uri
	if dbAcc.Url.Valid && strings.TrimSpace(dbAcc.Url.String) != "" {
		url = strings.TrimSpace(dbAcc.Url.String)
	}
	if url == "" && isLocal && publicDomain != "" {
		url = "https://" + publicDomain + "/@" + dbAcc.Username
	}

	author := Account{
		Username: dbAcc.Username,
		Acct:     acct,
		URL:      url,
	}

	uri := dbAcc.Uri
	if uri == "" && isLocal && federationDomain != "" {
		uri = "https://" + federationDomain + "/users/" + dbAcc.Username
	}
	if uri == "" {
		uri = url
	}

	suspended := dbAcc.SuspendedAt.Valid
	locked := dbAcc.Locked
	actorType := strings.TrimSpace(dbAcc.ActorType.String)
	isService := dbAcc.ActorType.Valid && (strings.EqualFold(actorType, "Service") || strings.EqualFold(actorType, "Application"))
	isGroup := dbAcc.ActorType.Valid && strings.EqualFold(actorType, "Group")
	bot := isService
	displayName := dbAcc.DisplayName
	indexable := dbAcc.Indexable
	var note string

	discoverableValue := false
	if dbAcc.Discoverable.Valid {
		discoverableValue = dbAcc.Discoverable.Bool
	}
	discoverable := &discoverableValue
	if suspended {
		discoverableValue = false
		locked = false
		bot = false
		note = ""
		displayName = ""
		indexable = false
	} else {
		note = formatAccountNote(cfg, dbAcc, author, opts.Mentions)
	}

	avatar := accountImageURL(publicDomain, "", "/avatars/original/missing.png")
	header := accountImageURL(publicDomain, "", "/headers/original/missing.png")
	if !suspended {
		avatarRemote := ""
		if dbAcc.AvatarRemoteUrl.Valid {
			avatarRemote = strings.TrimSpace(dbAcc.AvatarRemoteUrl.String)
		}
		avatar = accountImageURL(publicDomain, avatarRemote, "")
		if avatar == "" {
			avatar = accountImageURL(publicDomain, strings.TrimSpace(dbAcc.AvatarFileName.String), "/avatars/original/missing.png")
		}

		headerRemote := strings.TrimSpace(dbAcc.HeaderRemoteUrl)
		header = accountImageURL(publicDomain, headerRemote, "")
		if header == "" {
			header = accountImageURL(publicDomain, strings.TrimSpace(dbAcc.HeaderFileName.String), "/headers/original/missing.png")
		}
	}

	hideCollections := dbAcc.HideCollections.Valid && dbAcc.HideCollections.Bool

	fields := opts.Fields
	if fields == nil {
		fields = parseAccountFields(dbAcc.Fields)
	}
	if suspended {
		fields = []Field{}
	} else {
		fields = formatAccountFields(cfg, dbAcc, fields, author)
	}

	emojis := opts.Emojis
	if emojis == nil || suspended {
		emojis = []CustomEmoji{}
	}

	acc := Account{
		ID:              strconv.FormatInt(dbAcc.ID, 10),
		Username:        dbAcc.Username,
		Acct:            acct,
		DisplayName:     displayName,
		Locked:          locked,
		Bot:             bot,
		Discoverable:    discoverable,
		Indexable:       indexable,
		Group:           isGroup,
		CreatedAt:       createdAt,
		Note:            note,
		URL:             url,
		URI:             uri,
		Avatar:          avatar,
		AvatarStatic:    avatar,
		Header:          header,
		HeaderStatic:    header,
		FollowersCount:  0,
		FollowingCount:  0,
		StatusesCount:   0,
		LastStatusAt:    nil,
		HideCollections: hideCollections,
		Emojis:          emojis,
		Fields:          fields,
	}

	if opts.Stats != nil {
		acc.FollowersCount = opts.Stats.FollowersCount
		acc.FollowingCount = opts.Stats.FollowingCount
		acc.StatusesCount = opts.Stats.StatusesCount
		if opts.Stats.LastStatusAt.Valid {
			lastStatus := opts.Stats.LastStatusAt.Time.Format("2006-01-02")
			acc.LastStatusAt = &lastStatus
		}
	}

	if suspended {
		value := true
		acc.Suspended = &value
	}
	if dbAcc.SilencedAt.Valid {
		value := true
		acc.Limited = &value
	}
	if dbAcc.Memorial {
		value := true
		acc.Memorial = &value
	}

	if isLocal {
		roles := []AccountRoleSummary{}
		if opts.Roles != nil {
			roles = *opts.Roles
		}
		acc.Roles = &roles
		noindex := false
		if opts.NoIndex != nil {
			noindex = *opts.NoIndex
		}
		acc.NoIndex = &noindex
	}

	if opts.IncludeMoved && opts.Moved != nil && !suspended {
		acc.Moved = opts.Moved
	}

	return acc
}

func formatAccountFields(cfg *config.Config, dbAcc db.Account, fields []Field, author Account) []Field {
	if len(fields) == 0 {
		return []Field{}
	}
	local := accountIsLocal(dbAcc)
	formatted := make([]Field, 0, len(fields))
	opts := statusContentOptions{author: &author}
	for _, field := range fields {
		value := field.Value
		if field.VerifiedAt != nil && !local {
			value = shortenedLink(value, false)
		} else if local {
			value = linkifyAccountFieldText(cfg, value, opts)
		} else {
			value = sanitizeStatusHTML(value)
		}
		formatted = append(formatted, Field{
			Name:       field.Name,
			Value:      value,
			VerifiedAt: field.VerifiedAt,
		})
	}
	return formatted
}

func formatAccountNote(cfg *config.Config, dbAcc db.Account, author Account, mentions []Mention) string {
	note := strings.TrimSpace(dbAcc.Note)
	if note == "" {
		return ""
	}
	if !accountIsLocal(dbAcc) {
		return sanitizeStatusHTML(note)
	}
	opts := statusContentOptions{author: &author, mentions: mentions}
	html := linkifyTextWithOptions(cfg, note, opts, false, false)
	return simpleFormatHTML(html)
}

func buildCredentialAccount(ctx context.Context, q accountCredentialQueries, cfg *config.Config, dbAcc db.Account) CredentialAccount {
	apiAcc := SerializeAccount(cfg, dbAcc)
	if q != nil {
		if serialized := serializeAccounts(ctx, q, cfg, []db.Account{dbAcc}, true); serialized != nil {
			if acc, ok := serialized[dbAcc.ID]; ok {
				apiAcc = acc
			}
		}
	}

	settings := map[string]any{}
	locale := ""
	var user db.User
	if q != nil {
		if u, err := q.GetUserByAccountID(ctx, dbAcc.ID); err == nil {
			user = u
			if user.Locale.Valid {
				locale = strings.TrimSpace(user.Locale.String)
			}
			settings = settingsFromText(user.Settings)
		}
	}

	prefs := preferencesFromSettings(settings, locale)
	quotePolicy := settingsString(settings, "default_quote_policy")
	if quotePolicy == "" {
		quotePolicy = "public"
	}

	followRequests := 0
	if q != nil {
		if count, err := q.CountFollowRequestsForAccount(ctx, dbAcc.ID); err == nil {
			followRequests = int(count)
		}
	}

	attributionDomains := dbAcc.AttributionDomains
	if attributionDomains == nil {
		attributionDomains = []string{}
	}

	discoverable := false
	if dbAcc.Discoverable.Valid {
		discoverable = dbAcc.Discoverable.Bool
	}

	sourceFields := parseAccountFields(dbAcc.Fields)
	sourceNote := dbAcc.Note
	source := AccountSource{
		Privacy:             prefs.PostingDefaultVisibility,
		Sensitive:           prefs.PostingDefaultSensitive,
		Language:            prefs.PostingDefaultLanguage,
		Note:                sourceNote,
		Fields:              sourceFields,
		FollowRequestsCount: followRequests,
		HideCollections:     apiAcc.HideCollections,
		Discoverable:        discoverable,
		Indexable:           apiAcc.Indexable,
		AttributionDomains:  attributionDomains,
		QuotePolicy:         quotePolicy,
	}

	var role *AccountRole
	if q != nil && user.ID != 0 {
		if userRole, err := q.GetUserRole(ctx, user.ID); err == nil {
			role = &AccountRole{
				ID:          strconv.FormatInt(userRole.ID, 10),
				Name:        userRole.Name,
				Color:       userRole.Color,
				Permissions: strconv.FormatInt(userRole.Permissions, 10),
				Highlighted: userRole.Highlighted,
			}
		}
	}

	return CredentialAccount{
		Account: apiAcc,
		Source:  source,
		Role:    role,
	}
}

func accountIsLocal(dbAcc db.Account) bool {
	return !dbAcc.Domain.Valid || strings.TrimSpace(dbAcc.Domain.String) == ""
}

func accountPublicDomain(cfg *config.Config) string {
	publicDomain := instancePublicDomain(cfg)
	if publicDomain != "" {
		return publicDomain
	}
	return "localhost:3000"
}

func accountFederationDomain(cfg *config.Config, publicDomain string) string {
	federationDomain := instanceFederationDomain(cfg)
	if federationDomain != "" {
		return federationDomain
	}
	return publicDomain
}

func accountImageURL(domain, value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
			return value
		}
		if !strings.HasPrefix(value, "/") {
			value = "/files/" + value
		}
		if domain == "" {
			return value
		}
		return "https://" + domain + value
	}

	if fallback == "" {
		return ""
	}
	if domain == "" {
		return fallback
	}
	return "https://" + domain + fallback
}

func accountEmojiDomain(dbAcc db.Account) string {
	if dbAcc.Domain.Valid {
		return strings.ToLower(strings.TrimSpace(dbAcc.Domain.String))
	}
	return ""
}

func accountShortcodes(acc db.Account, fields []Field) []string {
	shortcodes := make([]string, 0)
	appendShortcodes(acc.DisplayName, &shortcodes)
	appendShortcodes(acc.Note, &shortcodes)
	for _, field := range fields {
		appendShortcodes(field.Value, &shortcodes)
	}
	return shortcodes
}

type mentionToken struct {
	Username string
	Domain   string
}

func extractMentionTokens(text string, cfg *config.Config) []mentionToken {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	entities := extractTextEntities(text)
	if len(entities) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	tokens := make([]mentionToken, 0)
	for _, entity := range entities {
		if entity.kind != entityMention {
			continue
		}
		username, domain := splitAcct(entity.value, entity.value)
		username = strings.ToLower(strings.TrimSpace(username))
		domain = normalizeDomain(domain)
		if domain != "" && isLocalMentionDomain(domain, cfg) {
			domain = ""
		}
		if username == "" {
			continue
		}
		key := statusMentionKey(username, domain)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		tokens = append(tokens, mentionToken{Username: username, Domain: domain})
	}
	return tokens
}

func mentionsFromTokens(tokens []mentionToken, lookup map[string]Mention) []Mention {
	if len(tokens) == 0 || len(lookup) == 0 {
		return nil
	}
	mentions := make([]Mention, 0, len(tokens))
	seen := make(map[string]struct{})
	for _, token := range tokens {
		key := statusMentionKey(token.Username, token.Domain)
		mention, ok := lookup[key]
		if !ok {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		mentions = append(mentions, mention)
	}
	return mentions
}

func addMentionAccounts(cfg *config.Config, lookup map[string]Mention, accounts []db.Account) {
	if len(accounts) == 0 {
		return
	}
	for _, acc := range accounts {
		mention := mentionFromAccount(cfg, acc)
		username, domain := splitAcct(mention.Acct, mention.Username)
		key := statusMentionKey(username, domain)
		if _, ok := lookup[key]; ok {
			continue
		}
		lookup[key] = mention
	}
}

func mentionFromAccount(cfg *config.Config, dbAcc db.Account) Mention {
	acct := dbAcc.Username
	domain := ""
	if dbAcc.Domain.Valid {
		domain = strings.TrimSpace(dbAcc.Domain.String)
		if unicodeDomain, err := idna.Lookup.ToUnicode(domain); err == nil && unicodeDomain != "" {
			domain = unicodeDomain
		}
		if domain != "" {
			acct = dbAcc.Username + "@" + domain
		}
	}

	url := dbAcc.Uri
	if dbAcc.Url.Valid && strings.TrimSpace(dbAcc.Url.String) != "" {
		url = strings.TrimSpace(dbAcc.Url.String)
	}
	if url == "" && accountIsLocal(dbAcc) {
		publicDomain := accountPublicDomain(cfg)
		if publicDomain != "" {
			url = "https://" + publicDomain + "/@" + dbAcc.Username
		}
	}

	return Mention{
		ID:       strconv.FormatInt(dbAcc.ID, 10),
		Username: dbAcc.Username,
		URL:      url,
		Acct:     acct,
	}
}

// validShortcodeChars is a lookup table for valid shortcode characters [a-zA-Z0-9_].
// 1 = valid, 0 = invalid.
var validShortcodeChars = [256]uint8{
	// 0-9
	'0': 1, '1': 1, '2': 1, '3': 1, '4': 1, '5': 1, '6': 1, '7': 1, '8': 1, '9': 1,
	// A-Z
	'A': 1, 'B': 1, 'C': 1, 'D': 1, 'E': 1, 'F': 1, 'G': 1, 'H': 1, 'I': 1, 'J': 1,
	'K': 1, 'L': 1, 'M': 1, 'N': 1, 'O': 1, 'P': 1, 'Q': 1, 'R': 1, 'S': 1, 'T': 1,
	'U': 1, 'V': 1, 'W': 1, 'X': 1, 'Y': 1, 'Z': 1,
	// a-z
	'a': 1, 'b': 1, 'c': 1, 'd': 1, 'e': 1, 'f': 1, 'g': 1, 'h': 1, 'i': 1, 'j': 1,
	'k': 1, 'l': 1, 'm': 1, 'n': 1, 'o': 1, 'p': 1, 'q': 1, 'r': 1, 's': 1, 't': 1,
	'u': 1, 'v': 1, 'w': 1, 'x': 1, 'y': 1, 'z': 1,
	// _
	'_': 1,
}

func appendShortcodes(text string, out *[]string) {
	n := len(text)
	if n < 4 { // minimum :ab: is 4 chars
		return
	}

	// First search optimization: Use IndexByte unconditionally (fastest for no colons case)
	i := strings.IndexByte(text, ':')
	if i < 0 {
		return
	}

	for i < n {
		// If we advanced past a colon manually, scan for next
		if text[i] != ':' {
			// Speculative manual scan for nearby colon (avoid slice alloc)
			found := false
			limit := i + 16
			if limit > n {
				limit = n
			}
			for k := i; k < limit; k++ {
				if text[k] == ':' {
					i = k
					found = true
					break
				}
			}

			if !found {
				// Not found nearby, use SIMD scan for the rest
				if limit >= n {
					break
				}
				// Scan from limit onwards since i..limit-1 were already checked and contained no colons,
				// so using text[limit:] avoids rescanning the verified non-colon range.
				idx := strings.IndexByte(text[limit:], ':')
				if idx < 0 {
					break
				}
				i = limit + idx
			}
		}

		// Check previous char boundary (must NOT be alphanumeric)
		// We know text[i] == ':'
		if i > 0 && validShortcodeChars[text[i-1]] == 1 {
			i++
			continue
		}

		// Scan for end colon
		// Optimization: Shortcode must be at least 2 chars (e.g. :ab:)
		scanStart := i + 1
		if scanStart+1 >= n {
			break
		}

		end := -1
		invalidCharPos := -1

		// Manual inner scan is unavoidable as we must validate chars
		for j := scanStart; j < n; j++ {
			c := text[j]
			if c == ':' {
				end = j
				break
			}

			if validShortcodeChars[c] == 0 {
				// Invalid char found, stop this scan
				invalidCharPos = j
				break
			}
		}

		if invalidCharPos != -1 {
			// Found invalid char.
			// text[invalidCharPos] is not a colon.
			// We can jump to invalidCharPos.
			i = invalidCharPos
			continue
		}

		if end == -1 {
			break
		}

		// Check length: (end - start - 1) >= 2
		codeLen := end - i - 1
		if codeLen < 2 {
			// Too short, treat 'end' colon as potential new start
			// Since chars between i and end are valid shortcode chars (but too few),
			// and end is a colon, 'end' is a candidate start.
			i = end
			continue
		}

		// Check next char boundary (must NOT be alphanumeric)
		if end+1 < n && validShortcodeChars[text[end+1]] == 1 {
			// Boundary check failed: the closing colon at 'end' cannot start a new shortcode
			// because the following character is alphanumeric. Skip to the character after 'end'.
			i = end + 1
			continue
		}

		// Match found!
		shortcode := text[i+1 : end]

		// Linear scan to deduplicate
		exists := false
		for _, s := range *out {
			if s == shortcode {
				exists = true
				break
			}
		}

		if !exists {
			*out = append(*out, shortcode)
		}

		// Advance past this shortcode
		// Next candidate must be after 'end'.
		// 'end' was the closing colon. Can it be a start? No, it was consumed.
		i = end + 1
	}
}

func parseAccountFields(raw []byte) []Field {
	if len(raw) == 0 {
		return []Field{}
	}
	var parsed []struct {
		Name       string     `json:"name"`
		Value      string     `json:"value"`
		VerifiedAt *time.Time `json:"verified_at"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return []Field{}
	}
	fields := make([]Field, 0, len(parsed))
	for _, field := range parsed {
		fields = append(fields, Field{
			Name:       field.Name,
			Value:      field.Value,
			VerifiedAt: field.VerifiedAt,
		})
	}
	return fields
}

func midnightUTC(value time.Time) time.Time {
	utc := value.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}
