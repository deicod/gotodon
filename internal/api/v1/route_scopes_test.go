package v1_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/deicod/gotodon/internal/api/middleware"
	v1 "github.com/deicod/gotodon/internal/api/v1"
	"github.com/deicod/gotodon/internal/api/v1alpha"
	v2 "github.com/deicod/gotodon/internal/api/v2"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/oauth"
)

// scopeMode is how a route checks the token's scopes.
type scopeMode int

const (
	// unscoped routes check no scopes: their Mastodon controllers call
	// neither doorkeeper_authorize! nor authorize_if_got_token!.
	unscoped scopeMode = iota
	// required routes need a token holding one of the scopes:
	// doorkeeper_authorize!.
	required
	// ifToken routes serve anonymous clients and check the scopes of a
	// token when there is one: authorize_if_got_token!.
	ifToken
)

func (m scopeMode) String() string {
	switch m {
	case required:
		return "doorkeeper_authorize!"
	case ifToken:
		return "authorize_if_got_token!"
	default:
		return "unscoped"
	}
}

type userRoute struct {
	pattern string
	mode    scopeMode
	// scopes are the scopes Mastodon's controller passes, any of which
	// lets a token through.
	scopes []string
}

func need(pattern string, scopes ...string) userRoute {
	return userRoute{pattern: pattern, mode: required, scopes: scopes}
}

func ifTokenHas(pattern string, scopes ...string) userRoute {
	return userRoute{pattern: pattern, mode: ifToken, scopes: scopes}
}

func open(pattern string) userRoute {
	return userRoute{pattern: pattern, mode: unscoped}
}

// userRoutes is every non-admin route of the v1, v2 and v1alpha handlers with
// the scopes of the matching controller in Mastodon's app/controllers/api.
// The scopes are written as Mastodon lists them, not with the oauth
// constants the handlers use, so that a wrong constant shows here.
var userRoutes = []userRoute{
	// Api::V1::AccountsController and Api::V1::Accounts::*
	need("GET /api/v1/accounts/verify_credentials", "profile", "read", "read:accounts"),
	need("PATCH /api/v1/accounts/update_credentials", "write", "write:accounts"),
	need("GET /api/v1/accounts/search", "read", "read:accounts"),
	need("GET /api/v1/accounts/relationships", "read", "read:follows"),
	need("GET /api/v1/accounts/familiar_followers", "read", "read:follows"),
	need("POST /api/v1/accounts", "write", "write:accounts"),
	ifTokenHas("GET /api/v1/accounts", "read", "read:accounts"),
	ifTokenHas("GET /api/v1/accounts/{id}", "read", "read:accounts"),
	ifTokenHas("GET /api/v1/accounts/{id}/statuses", "read", "read:statuses"),
	ifTokenHas("GET /api/v1/accounts/{id}/followers", "read", "read:accounts"),
	ifTokenHas("GET /api/v1/accounts/{id}/following", "read", "read:accounts"),
	open("GET /api/v1/accounts/{id}/featured_tags"),
	ifTokenHas("GET /api/v1/accounts/{id}/endorsements", "read", "read:accounts"),
	open("GET /api/v1/accounts/{id}/identity_proofs"),
	need("POST /api/v1/accounts/{id}/follow", "follow", "write", "write:follows"),
	need("POST /api/v1/accounts/{id}/unfollow", "follow", "write", "write:follows"),
	need("POST /api/v1/accounts/{id}/remove_from_followers", "follow", "write", "write:follows"),
	need("POST /api/v1/accounts/{id}/block", "follow", "write", "write:blocks"),
	need("POST /api/v1/accounts/{id}/unblock", "follow", "write", "write:blocks"),
	need("POST /api/v1/accounts/{id}/mute", "follow", "write", "write:mutes"),
	need("POST /api/v1/accounts/{id}/unmute", "follow", "write", "write:mutes"),
	need("POST /api/v1/accounts/{id}/note", "write", "write:accounts"),
	ifTokenHas("GET /api/v1/accounts/lookup", "read", "read:accounts"),
	need("GET /api/v1/accounts/{id}/lists", "read", "read:lists"),
	need("POST /api/v1/accounts/{id}/pin", "write", "write:accounts"),
	need("POST /api/v1/accounts/{id}/endorse", "write", "write:accounts"),
	need("POST /api/v1/accounts/{id}/unpin", "write", "write:accounts"),
	need("POST /api/v1/accounts/{id}/unendorse", "write", "write:accounts"),

	// Api::V1::StatusesController and Api::V1::Statuses::*
	ifTokenHas("GET /api/v1/statuses", "read", "read:statuses"),
	need("POST /api/v1/statuses", "write", "write:statuses"),
	ifTokenHas("GET /api/v1/statuses/{id}", "read", "read:statuses"),
	need("PUT /api/v1/statuses/{id}", "write", "write:statuses"),
	need("PATCH /api/v1/statuses/{id}", "write", "write:statuses"),
	need("DELETE /api/v1/statuses/{id}", "write", "write:statuses"),
	ifTokenHas("GET /api/v1/statuses/{id}/context", "read", "read:statuses"),
	ifTokenHas("GET /api/v1/statuses/{id}/history", "read", "read:statuses"),
	need("GET /api/v1/statuses/{id}/source", "read", "read:statuses"),
	need("PUT /api/v1/statuses/{id}/interaction_policy", "write", "write:statuses"),
	need("PATCH /api/v1/statuses/{id}/interaction_policy", "write", "write:statuses"),
	need("POST /api/v1/statuses/{id}/translate", "read", "read:statuses"),
	need("GET /api/v1/statuses/{id}/quotes", "read", "read:statuses"),
	need("POST /api/v1/statuses/{id}/quotes/{quote_id}/revoke", "write", "write:statuses"),
	need("POST /api/v1/statuses/{id}/favourite", "write", "write:favourites"),
	need("POST /api/v1/statuses/{id}/unfavourite", "write", "write:favourites"),
	need("POST /api/v1/statuses/{id}/reblog", "write", "write:statuses"),
	need("POST /api/v1/statuses/{id}/unreblog", "write", "write:statuses"),
	need("POST /api/v1/statuses/{id}/bookmark", "write", "write:bookmarks"),
	need("POST /api/v1/statuses/{id}/unbookmark", "write", "write:bookmarks"),
	need("POST /api/v1/statuses/{id}/mute", "write", "write:mutes"),
	need("POST /api/v1/statuses/{id}/unmute", "write", "write:mutes"),
	need("POST /api/v1/statuses/{id}/pin", "write", "write:accounts"),
	need("POST /api/v1/statuses/{id}/unpin", "write", "write:accounts"),
	ifTokenHas("GET /api/v1/statuses/{id}/favourited_by", "read", "read:accounts"),
	ifTokenHas("GET /api/v1/statuses/{id}/reblogged_by", "read", "read:accounts"),

	// Api::V1::Timelines::*
	need("GET /api/v1/timelines/home", "read", "read:statuses"),
	ifTokenHas("GET /api/v1/timelines/public", "read", "read:statuses"),
	ifTokenHas("GET /api/v1/timelines/link", "read", "read:statuses"),
	ifTokenHas("GET /api/v1/timelines/tag/{hashtag}", "read", "read:statuses"),
	need("GET /api/v1/timelines/list/{list_id}", "read", "read:lists"),

	// Api::V1::ListsController, Api::V1::Lists::AccountsController
	need("GET /api/v1/lists", "read", "read:lists"),
	need("POST /api/v1/lists", "write", "write:lists"),
	need("GET /api/v1/lists/{id}", "read", "read:lists"),
	need("PUT /api/v1/lists/{id}", "write", "write:lists"),
	need("PATCH /api/v1/lists/{id}", "write", "write:lists"),
	need("DELETE /api/v1/lists/{id}", "write", "write:lists"),
	need("GET /api/v1/lists/{id}/accounts", "read", "read:lists"),
	need("POST /api/v1/lists/{id}/accounts", "write", "write:lists"),
	need("DELETE /api/v1/lists/{id}/accounts", "write", "write:lists"),

	// Api::V1::BookmarksController, FavouritesController, MutesController,
	// BlocksController, CustomEmojisController, ConversationsController
	need("GET /api/v1/bookmarks", "read", "read:bookmarks"),
	need("GET /api/v1/favourites", "read", "read:favourites"),
	need("GET /api/v1/mutes", "follow", "read", "read:mutes"),
	need("GET /api/v1/blocks", "follow", "read", "read:blocks"),
	open("GET /api/v1/custom_emojis"),
	need("GET /api/v1/conversations", "read", "read:statuses"),
	need("DELETE /api/v1/conversations/{id}", "write", "write:conversations"),
	need("POST /api/v1/conversations/{id}/read", "write", "write:conversations"),
	need("POST /api/v1/conversations/{id}/unread", "write", "write:conversations"),

	// Api::V1::Emails::ConfirmationsController
	need("POST /api/v1/emails/confirmations", "write", "write:accounts"),
	ifTokenHas("GET /api/v1/emails/check_confirmation", "read", "read:accounts"),

	// Api::V1::AppsController, Api::V1::Apps::CredentialsController
	open("POST /api/v1/apps"),
	open("GET /api/v1/apps/verify_credentials"),

	// Api::V1::Trends::*
	open("GET /api/v1/trends"),
	open("GET /api/v1/trends/tags"),
	open("GET /api/v1/trends/statuses"),
	open("GET /api/v1/trends/links"),

	// Api::V1::NotificationsController and Api::V1::Notifications::*
	need("GET /api/v1/notifications", "read", "read:notifications"),
	need("GET /api/v1/notifications/unread_count", "read", "read:notifications"),
	need("GET /api/v1/notifications/{id}", "read", "read:notifications"),
	need("POST /api/v1/notifications/clear", "write", "write:notifications"),
	need("POST /api/v1/notifications/{id}/dismiss", "write", "write:notifications"),
	need("GET /api/v1/notifications/policy", "read", "read:notifications"),
	need("PUT /api/v1/notifications/policy", "write", "write:notifications"),
	need("PATCH /api/v1/notifications/policy", "write", "write:notifications"),
	need("GET /api/v1/notifications/requests", "read", "read:notifications"),
	need("GET /api/v1/notifications/requests/{id}", "read", "read:notifications"),
	need("GET /api/v1/notifications/requests/merged", "read", "read:notifications"),
	need("POST /api/v1/notifications/requests/{id}/accept", "write", "write:notifications"),
	need("POST /api/v1/notifications/requests/{id}/dismiss", "write", "write:notifications"),
	need("POST /api/v1/notifications/requests/accept", "write", "write:notifications"),
	need("POST /api/v1/notifications/requests/dismiss", "write", "write:notifications"),

	// Api::V1::MarkersController
	need("GET /api/v1/markers", "read", "read:statuses"),
	need("POST /api/v1/markers", "write", "write:statuses"),

	// Api::V1::FeaturedTagsController, Api::V1::FeaturedTags::SuggestionsController
	need("GET /api/v1/featured_tags", "read", "read:accounts"),
	need("GET /api/v1/featured_tags/suggestions", "read", "read:accounts"),
	need("POST /api/v1/featured_tags", "write", "write:accounts"),
	need("DELETE /api/v1/featured_tags/{id}", "write", "write:accounts"),

	// Api::V1::FiltersController
	need("GET /api/v1/filters", "read", "read:filters"),
	need("POST /api/v1/filters", "write", "write:filters"),
	need("GET /api/v1/filters/{id}", "read", "read:filters"),
	need("PUT /api/v1/filters/{id}", "write", "write:filters"),
	need("PATCH /api/v1/filters/{id}", "write", "write:filters"),
	need("DELETE /api/v1/filters/{id}", "write", "write:filters"),

	// Api::V1::PreferencesController, DirectoriesController
	need("GET /api/v1/preferences", "read", "read:accounts"),
	open("GET /api/v1/directory"),

	// Api::V1::AnnualReportsController
	need("GET /api/v1/annual_reports", "read", "read:accounts"),
	need("GET /api/v1/annual_reports/{id}", "read", "read:accounts"),
	need("POST /api/v1/annual_reports/{id}/read", "write", "write:accounts"),

	// Api::V1::InstancesController and Api::V1::Instances::*
	open("GET /api/v1/instance"),
	open("GET /api/v1/instance/rules"),
	open("GET /api/v1/instance/peers"),
	open("GET /api/v1/instance/activity"),
	open("GET /api/v1/instance/extended_description"),
	open("GET /api/v1/instance/privacy_policy"),
	open("GET /api/v1/instance/terms_of_service"),
	open("GET /api/v1/instance/terms_of_service/{date}"),
	open("GET /api/v1/instance/translation_languages"),
	open("GET /api/v1/instance/languages"),
	open("GET /api/v1/instance/domain_blocks"),

	// Api::V1::SuggestionsController
	need("GET /api/v1/suggestions", "read", "read:accounts"),
	need("DELETE /api/v1/suggestions/{account_id}", "write", "write:accounts"),

	// Api::V1::FollowRequestsController
	need("GET /api/v1/follow_requests", "follow", "read", "read:follows"),
	need("POST /api/v1/follow_requests/{id}/authorize", "follow", "write", "write:follows"),
	need("POST /api/v1/follow_requests/{id}/reject", "follow", "write", "write:follows"),

	// Api::V1::DomainBlocksController, Api::V1::DomainBlocks::PreviewsController
	need("GET /api/v1/domain_blocks", "follow", "read", "read:blocks"),
	need("POST /api/v1/domain_blocks", "follow", "write", "write:blocks"),
	need("DELETE /api/v1/domain_blocks", "follow", "write", "write:blocks"),
	need("GET /api/v1/domain_blocks/preview", "follow", "write", "write:blocks"),

	// Api::V1::ScheduledStatusesController
	need("GET /api/v1/scheduled_statuses", "read", "read:statuses"),
	need("GET /api/v1/scheduled_statuses/{id}", "read", "read:statuses"),
	need("PUT /api/v1/scheduled_statuses/{id}", "write", "write:statuses"),
	need("PATCH /api/v1/scheduled_statuses/{id}", "write", "write:statuses"),
	need("DELETE /api/v1/scheduled_statuses/{id}", "write", "write:statuses"),

	// Api::V1::Push::SubscriptionsController
	need("GET /api/v1/push/subscription", "push"),
	need("POST /api/v1/push/subscription", "push"),
	need("PUT /api/v1/push/subscription", "push"),
	need("PATCH /api/v1/push/subscription", "push"),
	need("DELETE /api/v1/push/subscription", "push"),

	// Api::V1::EndorsementsController
	need("GET /api/v1/endorsements", "read", "read:accounts"),

	// Api::V1::Peers::SearchController, ReportsController
	open("GET /api/v1/peers/search"),
	need("POST /api/v1/reports", "write", "write:reports"),

	// Api::V1::PollsController, Api::V1::Polls::VotesController
	ifTokenHas("GET /api/v1/polls/{id}", "read", "read:statuses"),
	need("POST /api/v1/polls/{id}/votes", "write", "write:statuses"),

	// Api::V1::MediaController, Api::V2::MediaController
	need("POST /api/v1/media", "write", "write:media"),
	need("GET /api/v1/media/{id}", "write", "write:media"),
	need("PUT /api/v1/media/{id}", "write", "write:media"),
	need("PATCH /api/v1/media/{id}", "write", "write:media"),
	need("DELETE /api/v1/media/{id}", "write", "write:media"),
	need("POST /api/v2/media", "write", "write:media"),

	// Api::V1::Profile::AvatarsController, HeadersController
	need("DELETE /api/v1/profile/avatar", "write", "write:accounts"),
	need("DELETE /api/v1/profile/header", "write", "write:accounts"),

	// Api::V1::TagsController, FollowedTagsController
	open("GET /api/v1/tags/{id}"),
	need("POST /api/v1/tags/{id}/follow", "follow", "write", "write:follows"),
	need("POST /api/v1/tags/{id}/unfollow", "follow", "write", "write:follows"),
	need("POST /api/v1/tags/{id}/feature", "write", "write:accounts"),
	need("POST /api/v1/tags/{id}/unfeature", "write", "write:accounts"),
	need("GET /api/v1/followed_tags", "follow", "read", "read:follows"),

	// Api::V1::AnnouncementsController, Api::V1::Announcements::ReactionsController
	open("GET /api/v1/announcements"),
	need("POST /api/v1/announcements/{id}/dismiss", "write", "write:accounts"),
	need("PUT /api/v1/announcements/{id}/reactions/{name}", "write", "write:favourites"),
	need("PATCH /api/v1/announcements/{id}/reactions/{name}", "write", "write:favourites"),
	need("DELETE /api/v1/announcements/{id}/reactions/{name}", "write", "write:favourites"),

	// Api::V2::SearchController, InstancesController, SuggestionsController
	ifTokenHas("GET /api/v2/search", "read", "read:search"),
	open("GET /api/v2/instance"),
	need("GET /api/v2/suggestions", "read", "read:accounts"),
	need("DELETE /api/v2/suggestions/{account_id}", "write", "write:accounts"),

	// Api::V2::NotificationsController and Api::V2::Notifications::*
	need("GET /api/v2/notifications", "read", "read:notifications"),
	need("GET /api/v2/notifications/unread_count", "read", "read:notifications"),
	need("GET /api/v2/notifications/{group_key}", "read", "read:notifications"),
	need("GET /api/v2/notifications/{group_key}/accounts", "read", "read:notifications"),
	need("POST /api/v2/notifications/clear", "write", "write:notifications"),
	need("POST /api/v2/notifications/{group_key}/dismiss", "write", "write:notifications"),
	need("GET /api/v2/notifications/policy", "read", "read:notifications"),
	need("PUT /api/v2/notifications/policy", "write", "write:notifications"),
	need("PATCH /api/v2/notifications/policy", "write", "write:notifications"),

	// Api::V2::FiltersController, Api::V2::Filters::KeywordsController and
	// StatusesController. The trailing-slash patterns serve the keyword and
	// status routes.
	need("GET /api/v2/filters", "read", "read:filters"),
	need("POST /api/v2/filters", "write", "write:filters"),
	need("GET /api/v2/filters/", "read", "read:filters"),
	need("POST /api/v2/filters/", "write", "write:filters"),
	need("PUT /api/v2/filters/{id}", "write", "write:filters"),
	need("PATCH /api/v2/filters/{id}", "write", "write:filters"),
	need("PUT /api/v2/filters/keywords/{id}", "write", "write:filters"),
	need("PATCH /api/v2/filters/keywords/{id}", "write", "write:filters"),
	need("PUT /api/v2/filters/", "write", "write:filters"),
	need("PATCH /api/v2/filters/", "write", "write:filters"),
	need("DELETE /api/v2/filters/", "write", "write:filters"),

	// Api::V1Alpha::AsyncRefreshesController
	need("GET /api/v1_alpha/async_refreshes/{id}", "read"),
}

// readPOSTs are the non-GET routes that only read, which Mastodon guards with
// read scopes.
var readPOSTs = []string{
	"POST /api/v1/statuses/{id}/translate",
}

// writeGETs are the GET routes Mastodon guards with write scopes: the domain
// block preview precedes a block, and an attachment is readable only by the
// uploader.
var writeGETs = []string{
	"GET /api/v1/domain_blocks/preview",
	"GET /api/v1/media/{id}",
}

// anonymousRefused are the unscoped and authorize_if_got_token! routes whose
// handlers refuse a request without a token themselves, as Mastodon's do with
// require_user!, require_authenticated_user! or, for an application's
// credentials, a token check. Their 401 doesn't come from the scope check.
var anonymousRefused = []string{
	"GET /api/v1/accounts/{id}/identity_proofs",
	"GET /api/v1/emails/check_confirmation",
	"GET /api/v1/apps/verify_credentials",
}

// recordingMux records the patterns RegisterRoutes registers and serves them.
type recordingMux struct {
	*http.ServeMux
	patterns []string
}

func (m *recordingMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	m.patterns = append(m.patterns, pattern)
	m.ServeMux.HandleFunc(pattern, handler)
}

var errDatabase = errors.New("database unavailable")

// failingDB is a db.DBTX on which every query fails, so that handlers which
// pass the scope check answer with their own errors instead of needing
// Postgres.
type failingDB struct{}

func (failingDB) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errDatabase
}

func (failingDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, errDatabase
}

func (failingDB) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	return failingRow{}
}

type failingRow struct{}

func (failingRow) Scan(...any) error { return errDatabase }

// newUserMux registers the routes of every non-admin handler in the v1, v2
// and v1alpha packages, as cmd/serve.go does, on queries that fail every
// call.
func newUserMux() *recordingMux {
	queries := db.New(failingDB{})
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	mux := &recordingMux{ServeMux: http.NewServeMux()}

	v1.NewAccountsHandler(queries, nil, cfg, nil).RegisterRoutes(mux)
	v1.NewStatusesHandler(queries, nil, nil, cfg, nil).RegisterRoutes(mux)
	v1.NewTimelinesHandler(queries, cfg).RegisterRoutes(mux)
	v1.NewListsHandler(queries, cfg).RegisterRoutes(mux)
	v1.NewPollsHandler(queries).RegisterRoutes(mux)
	v1.NewAppsHandler(queries, cfg).RegisterRoutes(mux)
	v1.NewNotificationsHandler(queries, nil, cfg, nil).RegisterRoutes(mux)
	v1.NewFiltersHandler(queries, nil).RegisterRoutes(mux)
	v1.NewMediaHandler(queries, nil).RegisterRoutes(mux)
	v1.NewAnnouncementsHandler(queries, cfg).RegisterRoutes(mux)
	v1.NewMarkersHandler(queries).RegisterRoutes(mux)
	v1alpha.NewAsyncRefreshHandler(cfg, nil).RegisterRoutes(mux)
	v1.NewPreferencesHandler(queries).RegisterRoutes(mux)
	v1.NewSuggestionsHandler(queries, cfg).RegisterRoutes(mux)
	v1.NewTrendsHandler(queries, cfg).RegisterRoutes(mux)
	v1.NewDirectoryHandler(queries, cfg).RegisterRoutes(mux)
	v1.NewBookmarksHandler(queries, cfg).RegisterRoutes(mux)
	v1.NewFavouritesHandler(queries, cfg).RegisterRoutes(mux)
	v1.NewMutesHandler(queries, cfg).RegisterRoutes(mux)
	v1.NewBlocksHandler(queries, cfg).RegisterRoutes(mux)
	v1.NewCustomEmojisHandler(queries, nil).RegisterRoutes(mux)
	v1.NewConversationsHandler(queries, cfg).RegisterRoutes(mux)
	v1.NewFollowRequestsHandler(queries, cfg, nil).RegisterRoutes(mux)
	v1.NewDomainBlocksHandler(queries, cfg).RegisterRoutes(mux)
	v1.NewScheduledStatusesHandler(queries, nil).RegisterRoutes(mux)
	v1.NewPushHandler(queries, cfg).RegisterRoutes(mux)
	v1.NewEndorsementsHandler(queries, cfg).RegisterRoutes(mux)
	v1.NewFeaturedTagsHandler(queries, cfg).RegisterRoutes(mux)
	v1.NewTagsHandler(queries, cfg).RegisterRoutes(mux)
	v1.NewInstanceHandler(queries, cfg).RegisterRoutes(mux)
	v1.NewInstanceInfoHandler(queries, cfg).RegisterRoutes(mux)
	v1.NewProfileHandler(queries, cfg).RegisterRoutes(mux)
	v1.NewAnnualReportsHandler(queries).RegisterRoutes(mux)
	v1.NewEmailsHandler(queries).RegisterRoutes(mux)
	v1.NewPeersHandler(queries).RegisterRoutes(mux)
	v1.NewReportsHandler(queries).RegisterRoutes(mux)

	v2.NewInstanceHandler(queries, cfg).RegisterRoutes(mux)
	v2.NewFiltersHandler(queries).RegisterRoutes(mux)
	v2.NewSearchHandler(queries, cfg).RegisterRoutes(mux)
	v2.NewSuggestionsHandler(queries, cfg).RegisterRoutes(mux)
	v2.NewNotificationsHandler(queries, cfg).RegisterRoutes(mux)

	return mux
}

// pathValues fills the wildcards of a route pattern.
var pathValues = strings.NewReplacer(
	"{id}", "1",
	"{quote_id}", "2",
	"{hashtag}", "gotodon",
	"{list_id}", "1",
	"{account_id}", "1",
	"{group_key}", "ungrouped-1",
	"{name}", "%F0%9F%91%8D",
	"{date}", "2025-01-01",
)

// routeRequest builds a request that pattern matches. A trailing-slash
// pattern gets a path below it that no more specific pattern matches.
func routeRequest(pattern string) *http.Request {
	method, path, _ := strings.Cut(pattern, " ")
	path = pathValues.Replace(path)
	if strings.HasSuffix(path, "/") {
		path += "1/keywords"
	}
	body := ""
	if method != http.MethodGet && method != http.MethodDelete {
		body = "{}"
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// token describes the token a request carries, as the auth middleware
// stores it in the context.
type token struct {
	// none sends no token.
	none bool
	// withoutScopes leaves the scopes out of the context, as for a token
	// whose scopes were never set.
	withoutScopes bool
	scopes        string
}

func withToken(req *http.Request, tok token) *http.Request {
	if tok.none {
		return req
	}
	ctx := req.Context()
	ctx = context.WithValue(ctx, middleware.ContextKeyTokenID, int64(30))
	ctx = context.WithValue(ctx, middleware.ContextKeyAppID, int64(40))
	ctx = context.WithValue(ctx, middleware.ContextKeyUserID, int64(20))
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, int64(10))
	if !tok.withoutScopes {
		ctx = context.WithValue(ctx, middleware.ContextKeyScopes, tok.scopes)
	}
	return req.WithContext(ctx)
}

// outcome is what the scope check did with a request.
type outcome int

const (
	passed outcome = iota
	refusedScopes
	refusedToken
)

func (o outcome) String() string {
	switch o {
	case refusedScopes:
		return "403 " + middleware.OutsideScopesMessage
	case refusedToken:
		return "401 " + middleware.InvalidTokenMessage
	default:
		return "passed to the handler"
	}
}

// serve sends a request for route with tok through mux and reports whether
// the scope check refused it. No handler answers with the scope check's
// 403, so any other answer, or a handler panicking on a dependency the test
// leaves out, means the request got past it. A 401 counts as the scope
// check's only for a request without a token, as handlers answer requests
// with a token by their own checks.
func serve(t *testing.T, mux *recordingMux, route userRoute, tok token) (outcome, string) {
	t.Helper()
	req := withToken(routeRequest(route.pattern), tok)
	if _, matched := mux.Handler(req); matched != route.pattern {
		t.Fatalf("request %s %s matched %q, want %q", req.Method, req.URL.Path, matched, route.pattern)
	}
	rec := httptest.NewRecorder()
	panicked := func() (panicked bool) {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		mux.ServeHTTP(rec, req)
		return false
	}()
	if panicked {
		return passed, "handler panicked"
	}
	summary := fmt.Sprintf("%d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	message := errorMessage(rec)
	switch {
	case rec.Code == http.StatusForbidden && message == middleware.OutsideScopesMessage:
		return refusedScopes, summary
	case tok.none && rec.Code == http.StatusUnauthorized && message == middleware.InvalidTokenMessage:
		return refusedToken, summary
	default:
		return passed, summary
	}
}

func errorMessage(rec *httptest.ResponseRecorder) string {
	var body struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Error
}

// grants reports whether Doorkeeper lets a token with tokenScopes through a
// route: some scope of the token is one of the route's, compared as exact
// strings, as AccessToken#includes_scope? compares them. It is the oracle the
// handlers' hierarchical matching is checked against.
func grants(route userRoute, tokenScopes string) bool {
	for _, scope := range strings.Fields(tokenScopes) {
		if slices.Contains(route.scopes, scope) {
			return true
		}
	}
	return false
}

// expected is the outcome Mastodon gives a request for route with tok.
func expected(route userRoute, tok token) outcome {
	switch {
	case tok.none && slices.Contains(anonymousRefused, route.pattern):
		return refusedToken
	case route.mode == unscoped, tok.none && route.mode == ifToken:
		return passed
	case tok.none:
		return refusedToken
	case !tok.withoutScopes && grants(route, tok.scopes):
		return passed
	default:
		return refusedScopes
	}
}

func sortedValidScopes() []string {
	scopes := make([]string, 0, len(oauth.ValidScopes))
	for scope := range oauth.ValidScopes {
		scopes = append(scopes, scope)
	}
	slices.Sort(scopes)
	return scopes
}

func TestUserRoutesAllDeclareScopes(t *testing.T) {
	registered := slices.Clone(newUserMux().patterns)
	slices.Sort(registered)
	expectedPatterns := make([]string, 0, len(userRoutes))
	for _, route := range userRoutes {
		expectedPatterns = append(expectedPatterns, route.pattern)
	}
	slices.Sort(expectedPatterns)

	if !slices.Equal(registered, expectedPatterns) {
		for _, pattern := range registered {
			if !slices.Contains(expectedPatterns, pattern) {
				t.Errorf("route %q is registered but has no expected scopes in userRoutes", pattern)
			}
		}
		for _, pattern := range expectedPatterns {
			if !slices.Contains(registered, pattern) {
				t.Errorf("route %q is in userRoutes but not registered", pattern)
			}
		}
		for i := 1; i < len(expectedPatterns); i++ {
			if expectedPatterns[i] == expectedPatterns[i-1] {
				t.Errorf("route %q is in userRoutes twice", expectedPatterns[i])
			}
		}
	}
}

// TestUserRoutesScopeColumn keeps the table consistent: every scope is a
// valid user scope, a route lists the parent of every child scope it lists,
// as Mastodon's controllers do, which makes hierarchical matching agree with
// Doorkeeper's exact matching, and GET routes need read scopes while other
// methods need write scopes, except where Mastodon decides otherwise.
func TestUserRoutesScopeColumn(t *testing.T) {
	for _, route := range userRoutes {
		if route.mode == unscoped {
			if len(route.scopes) != 0 {
				t.Errorf("%s: unscoped route lists scopes %v", route.pattern, route.scopes)
			}
			continue
		}
		if len(route.scopes) == 0 {
			t.Errorf("%s: %s without scopes", route.pattern, route.mode)
		}
		families := map[string]bool{}
		for _, scope := range route.scopes {
			if !oauth.ValidScopes[scope] || strings.HasPrefix(scope, "admin:") {
				t.Errorf("%s: %q is not a user scope", route.pattern, scope)
			}
			parent, _, isChild := strings.Cut(scope, ":")
			if isChild && !slices.Contains(route.scopes, parent) {
				t.Errorf("%s: lists %q without %q", route.pattern, scope, parent)
			}
			families[parent] = true
		}
		method, _, _ := strings.Cut(route.pattern, " ")
		switch {
		case families["push"]:
			if len(route.scopes) != 1 {
				t.Errorf("%s: push routes take push alone, got %v", route.pattern, route.scopes)
			}
		case families["read"] == families["write"]:
			t.Errorf("%s: scopes %v should be either read or write scopes", route.pattern, route.scopes)
		case families["read"]:
			if method != http.MethodGet && !slices.Contains(readPOSTs, route.pattern) {
				t.Errorf("%s: a %s route with read scopes %v", route.pattern, method, route.scopes)
			}
		default:
			if method == http.MethodGet && !slices.Contains(writeGETs, route.pattern) {
				t.Errorf("%s: a GET route with write scopes %v", route.pattern, route.scopes)
			}
		}
	}
}

// TestUserRoutesScopes sends every route the tokens below and compares what
// the scope check does with what Mastodon does.
func TestUserRoutesScopes(t *testing.T) {
	type tokenFor func(route userRoute) token
	fixed := func(tok token) tokenFor { return func(userRoute) token { return tok } }

	// everyOtherScope holds every valid scope that isn't one of the
	// route's, so no other scope, child or parent, reaches a route.
	everyOtherScope := func(route userRoute) token {
		var scopes []string
		for _, scope := range sortedValidScopes() {
			if !slices.Contains(route.scopes, scope) {
				scopes = append(scopes, scope)
			}
		}
		return token{scopes: strings.Join(scopes, " ")}
	}

	cases := []struct {
		name  string
		token tokenFor
	}{
		{"no token", fixed(token{none: true})},
		{"empty scope string", fixed(token{scopes: ""})},
		{"whitespace scope string", fixed(token{scopes: "  "})},
		{"scopes left out of the context", fixed(token{withoutScopes: true})},
		{"read write follow push", fixed(token{scopes: "read write follow push"})},
		{"admin scopes only", fixed(token{scopes: "admin:read admin:write"})},
		{"every scope but the route's", everyOtherScope},
	}
	for _, scope := range sortedValidScopes() {
		cases = append(cases, struct {
			name  string
			token tokenFor
		}{scope, fixed(token{scopes: scope})})
	}

	mux := newUserMux()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, route := range userRoutes {
				tok := c.token(route)
				want := expected(route, tok)
				got, response := serve(t, mux, route, tok)
				if got != want {
					t.Errorf("%s (%s %v): %s, want %s; response: %s", route.pattern, route.mode, route.scopes, got, want, response)
				}
			}
		})
	}
}

// TestUserRoutesReadTokenRefusedOnWriteRoutes states the acceptance criterion
// directly: a read token gets 403 on every write route, and a write token
// passes them.
func TestUserRoutesReadTokenRefusedOnWriteRoutes(t *testing.T) {
	mux := newUserMux()
	writeRoutes := 0
	for _, route := range userRoutes {
		if !slices.Contains(route.scopes, "write") {
			continue
		}
		writeRoutes++
		if got, response := serve(t, mux, route, token{scopes: "read"}); got != refusedScopes {
			t.Errorf("%s with read: %s, want %s; response: %s", route.pattern, got, refusedScopes, response)
		}
		if got, response := serve(t, mux, route, token{scopes: "write"}); got != passed {
			t.Errorf("%s with write: %s, want %s; response: %s", route.pattern, got, passed, response)
		}
	}
	if writeRoutes == 0 {
		t.Fatal("no write routes in userRoutes")
	}
}

// TestUserRoutesFollowScope checks that a follow token reaches exactly the
// routes Mastodon opens to it: the account relationship actions the
// acceptance criteria list, follow requests, blocks, mutes, domain blocks and
// followed tags.
func TestUserRoutesFollowScope(t *testing.T) {
	want := []string{
		"POST /api/v1/accounts/{id}/follow",
		"POST /api/v1/accounts/{id}/unfollow",
		"POST /api/v1/accounts/{id}/remove_from_followers",
		"POST /api/v1/accounts/{id}/block",
		"POST /api/v1/accounts/{id}/unblock",
		"POST /api/v1/accounts/{id}/mute",
		"POST /api/v1/accounts/{id}/unmute",
		"GET /api/v1/follow_requests",
		"POST /api/v1/follow_requests/{id}/authorize",
		"POST /api/v1/follow_requests/{id}/reject",
		"GET /api/v1/blocks",
		"GET /api/v1/mutes",
		"GET /api/v1/domain_blocks",
		"POST /api/v1/domain_blocks",
		"DELETE /api/v1/domain_blocks",
		"GET /api/v1/domain_blocks/preview",
		"POST /api/v1/tags/{id}/follow",
		"POST /api/v1/tags/{id}/unfollow",
		"GET /api/v1/followed_tags",
	}
	slices.Sort(want)

	mux := newUserMux()
	var got []string
	for _, route := range userRoutes {
		if route.mode == unscoped {
			continue
		}
		outcome, response := serve(t, mux, route, token{scopes: "follow"})
		switch outcome {
		case passed:
			got = append(got, route.pattern)
		case refusedScopes:
		default:
			t.Errorf("%s with follow: %s; response: %s", route.pattern, outcome, response)
		}
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("follow reaches\n  %v\nwant\n  %v", got, want)
	}
}
