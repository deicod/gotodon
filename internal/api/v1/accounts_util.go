package v1

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

var emojiScanRe = regexp.MustCompile(`:([a-zA-Z0-9_]{2,}):`)

type accountSerializeOptions struct {
	Stats        *db.AccountStat
	Fields       []Field
	Emojis       []CustomEmoji
	NoIndex      *bool
	Roles        *[]AccountRoleSummary
	Moved        *Account
	IncludeMoved bool
}

type accountDataQueries interface {
	ListAccountStatsByAccountIDs(ctx context.Context, accountIds []int64) ([]db.AccountStat, error)
	ListUserSettingsByAccountIDs(ctx context.Context, accountIds []int64) ([]db.ListUserSettingsByAccountIDsRow, error)
	ListUserRolesByAccountIDs(ctx context.Context, accountIds []int64) ([]db.ListUserRolesByAccountIDsRow, error)
	ListCustomEmojisByShortcodesAndDomain(ctx context.Context, arg db.ListCustomEmojisByShortcodesAndDomainParams) ([]db.CustomEmoji, error)
	GetAccountsByIDs(ctx context.Context, ids []int64) ([]db.Account, error)
}

type accountCredentialQueries interface {
	accountDataQueries
	GetUserByAccountID(ctx context.Context, accountID int64) (db.User, error)
	CountFollowRequestsForAccount(ctx context.Context, targetAccountID int64) (int64, error)
	GetUserRole(ctx context.Context, userID int64) (db.UserRole, error)
}

// SerializeAccount converts a db.Account to an API Account.
func SerializeAccount(cfg *config.Config, dbAcc db.Account) Account {
	return serializeAccount(cfg, dbAcc, accountSerializeOptions{})
}

func serializeAccounts(ctx context.Context, q accountDataQueries, cfg *config.Config, dbAccounts []db.Account, includeMoved bool) map[int64]Account {
	accounts := make(map[int64]Account, len(dbAccounts))
	if len(dbAccounts) == 0 {
		return accounts
	}

	accountIDs := make([]int64, 0, len(dbAccounts))
	fieldsByID := make(map[int64][]Field, len(dbAccounts))
	shortcodesByID := make(map[int64][]string, len(dbAccounts))
	domainShortcodes := make(map[string]map[string]struct{})

	for _, acc := range dbAccounts {
		accountIDs = append(accountIDs, acc.ID)
		fields := parseAccountFields(acc.Fields)
		fieldsByID[acc.ID] = fields

		if acc.SuspendedAt.Valid {
			continue
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
		acct = dbAcc.Username + "@" + strings.TrimSpace(dbAcc.Domain.String)
	}

	url := dbAcc.Uri
	if dbAcc.Url.Valid && strings.TrimSpace(dbAcc.Url.String) != "" {
		url = strings.TrimSpace(dbAcc.Url.String)
	}
	if url == "" && isLocal && publicDomain != "" {
		url = "https://" + publicDomain + "/@" + dbAcc.Username
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
	bot := dbAcc.ActorType.Valid && dbAcc.ActorType.String == "Service"
	note := dbAcc.Note
	displayName := dbAcc.DisplayName
	indexable := dbAcc.Indexable

	var discoverable *bool
	if suspended {
		value := false
		discoverable = &value
		locked = false
		bot = false
		note = ""
		displayName = ""
		indexable = false
	} else if dbAcc.Discoverable.Valid {
		value := dbAcc.Discoverable.Bool
		discoverable = &value
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
		Group:           false,
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

	source := AccountSource{
		Privacy:             prefs.PostingDefaultVisibility,
		Sensitive:           prefs.PostingDefaultSensitive,
		Language:            prefs.PostingDefaultLanguage,
		Note:                apiAcc.Note,
		Fields:              apiAcc.Fields,
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
	seen := make(map[string]struct{})
	appendShortcodes(acc.DisplayName, seen, &shortcodes)
	appendShortcodes(acc.Note, seen, &shortcodes)
	for _, field := range fields {
		appendShortcodes(field.Value, seen, &shortcodes)
	}
	return shortcodes
}

func appendShortcodes(text string, seen map[string]struct{}, out *[]string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	matches := emojiScanRe.FindAllStringSubmatchIndex(text, -1)
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		if match[0] > 0 && isASCIIAlnum(text[match[0]-1]) {
			continue
		}
		if match[1] < len(text) && isASCIIAlnum(text[match[1]]) {
			continue
		}
		shortcode := text[match[2]:match[3]]
		if shortcode == "" {
			continue
		}
		if _, ok := seen[shortcode]; ok {
			continue
		}
		seen[shortcode] = struct{}{}
		*out = append(*out, shortcode)
	}
}

func isASCIIAlnum(b byte) bool {
	return (b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
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
