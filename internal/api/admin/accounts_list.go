package admin

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/activitypub"
)

const (
	// accountsListDefaultLimit is Api::V1::Admin::AccountsController::LIMIT.
	accountsListDefaultLimit = 100
	// accountsListMaxLimit is twice the default, as Api::BaseController's
	// limit_param allows.
	accountsListMaxLimit = 2 * accountsListDefaultLimit
)

// AccountListStore is the part of *db.Queries that an admin account list
// reads. Tests use stubs.
type AccountListStore interface {
	AccountEntityStore
	AdminListAccounts(ctx context.Context, arg db.AdminListAccountsParams) ([]db.AdminListAccountsRow, error)
}

// AccountFilter holds the params of Mastodon's AccountFilter, which the v1
// and v2 index actions translate their query parameters into. Values are
// matched as AccountFilter does after stripping surrounding whitespace, and
// an empty value doesn't filter.
type AccountFilter struct {
	// Origin is "local", "remote" or "" for both.
	Origin string
	// Status is "active", "pending", "disabled", "suspended", "silenced",
	// "sensitized" or "" for any.
	Status      string
	ByDomain    string
	Username    string
	DisplayName string
	Email       string
	// IP matches users who used the address or an address in the range.
	// An invalid one matches nothing, as AccountFilter's Account.none.
	IP        string
	InvitedBy int64
	// Staff limits the list to users whose role can manage reports, which
	// Mastodon passes as role_ids: UserRole.that_can(:manage_reports).
	Staff   bool
	RoleIDs []int64
}

// AccountList serves an admin account list, as
// Api::V1::Admin::AccountsController#index does for v1 and, through its
// subclass, v2: the accounts that the filter selects, newest first and
// paginated by ID, with Link headers that keep the version's own query
// parameters.
type AccountList struct {
	store AccountListStore
	cfg   *config.Config
	// path is the list's path, which the Link headers point to.
	path string
	// linkParams are the query parameters that the Link headers keep,
	// Mastodon's PAGINATION_PARAMS.
	linkParams []string
	// instanceActorURI identifies the instance actor, which the list
	// leaves out. It is "" without a local domain.
	instanceActorURI string
}

// NewAccountList returns the admin account list at path, whose Link headers
// keep linkParams. store may be nil, and then the list answers 500.
func NewAccountList(store AccountListStore, cfg *config.Config, path string, linkParams []string) *AccountList {
	localDomain := ""
	if cfg != nil {
		localDomain = cfg.Instance.LocalDomain
	}
	return &AccountList{
		store:            store,
		cfg:              cfg,
		path:             path,
		linkParams:       linkParams,
		instanceActorURI: activitypub.InstanceActorURI(localDomain),
	}
}

// Serve answers r with the accounts that filter selects. Pagination
// parameters that can't select anything, an invalid IP filter among them,
// give an empty list.
func (l *AccountList) Serve(w http.ResponseWriter, r *http.Request, filter AccountFilter) {
	query := r.URL.Query()
	limit := parseAccountsLimit(query.Get("limit"))
	maxRaw := query.Get("max_id")
	maxID, maxValid := parseOptionalID(maxRaw)
	sinceID, sinceValid := parseOptionalID(query.Get("since_id"))
	minID, minValid := parseOptionalID(query.Get("min_id"))
	// No account has an ID below 1. A since_id or min_id below 1 bounds
	// nothing, and the query ignores it.
	if maxRaw != "" && maxID <= 0 {
		maxValid = false
	}

	params := db.AdminListAccountsParams{
		Limit:            limit,
		Origin:           strings.TrimSpace(filter.Origin),
		Status:           strings.TrimSpace(filter.Status),
		ByDomain:         strings.TrimSpace(filter.ByDomain),
		Username:         strings.TrimPrefix(strings.TrimSpace(filter.Username), "@"),
		DisplayName:      strings.TrimSpace(filter.DisplayName),
		Email:            strings.TrimSpace(filter.Email),
		Ip:               strings.TrimSpace(filter.IP),
		InvitedBy:        filter.InvitedBy,
		RoleIds:          filter.RoleIDs,
		InstanceActorUri: l.instanceActorURI,
		MaxID:            maxID,
		SinceID:          sinceID,
		MinID:            minID,
	}
	if filter.Staff {
		params.Permissions = "staff"
		params.RoleIds = nil
	}
	// AccountFilter#relevant_params: a domain already selects remote
	// accounts.
	if params.Origin == "remote" && params.ByDomain != "" {
		params.Origin = ""
	}
	// Paginable#paginate_by_min_id ignores since_id.
	if minID > 0 {
		params.SinceID = 0
	}

	if !maxValid || !sinceValid || !minValid || (params.Ip != "" && !validIPFilter(params.Ip)) {
		web.JSON(w, http.StatusOK, []AdminAccount{})
		return
	}
	if l.store == nil {
		web.JSONError(w, http.StatusInternalServerError, "Admin data unavailable")
		return
	}

	rows, err := l.store.AdminListAccounts(r.Context(), params)
	if err != nil {
		slog.ErrorContext(r.Context(), "Admin account list failed", "error", err)
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}
	// The query returns the accounts after min_id in ascending order, which
	// Paginable#to_a_paginated_by_id reverses.
	if minID > 0 {
		slices.Reverse(rows)
	}

	accounts, err := SerializeAccounts(r.Context(), l.store, l.cfg, rows)
	if err != nil {
		slog.ErrorContext(r.Context(), "Admin account list serialization failed", "error", err)
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	l.setLinkHeader(w, r, rows, limit)
	web.JSON(w, http.StatusOK, accounts)
}

// setLinkHeader sets the Link header of Mastodon's
// insert_pagination_headers: next while a full page came back, and prev for
// any non-empty page.
func (l *AccountList) setLinkHeader(w http.ResponseWriter, r *http.Request, rows []db.AdminListAccountsRow, limit int32) {
	if len(rows) == 0 {
		return
	}
	links := make([]string, 0, 2)
	if len(rows) == int(limit) {
		links = append(links, "<"+l.pageURL(r, "max_id", rows[len(rows)-1].ID)+`>; rel="next"`)
	}
	links = append(links, "<"+l.pageURL(r, "min_id", rows[0].ID)+`>; rel="prev"`)
	w.Header().Set("Link", strings.Join(links, ", "))
}

// pageURL is the list's URL with the request's link parameters and key set
// to id.
func (l *AccountList) pageURL(r *http.Request, key string, id int64) string {
	params := url.Values{}
	for name, values := range r.URL.Query() {
		if slices.Contains(l.linkParams, name) {
			params[name] = slices.Clone(values)
		}
	}
	params.Set(key, strconv.FormatInt(id, 10))

	scheme, host := l.baseURL(r)
	u := url.URL{Scheme: scheme, Host: host, Path: l.path, RawQuery: params.Encode()}
	return u.String()
}

func (l *AccountList) baseURL(r *http.Request) (string, string) {
	if l.cfg != nil && l.cfg.Instance.LocalDomain != "" {
		return "https", l.cfg.Instance.LocalDomain
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	} else if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded != "" {
		scheme = forwarded
	}
	host := r.Host
	if host == "" {
		host = "localhost"
	}
	return scheme, host
}

// parseAccountsLimit is Api::BaseController#limit_param for the account
// lists: the default without a limit, otherwise its absolute value, at most
// accountsListMaxLimit. A value that isn't a number is 0, as Ruby's to_i
// makes it, and one beyond int64 is the maximum.
func parseAccountsLimit(raw string) int32 {
	if raw == "" {
		return accountsListDefaultLimit
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if errors.Is(err, strconv.ErrRange) {
		return accountsListMaxLimit
	}
	if err != nil {
		return 0
	}
	if parsed == math.MinInt64 {
		// Its absolute value overflows int64.
		return accountsListMaxLimit
	}
	if parsed < 0 {
		parsed = -parsed
	}
	return int32(min(parsed, accountsListMaxLimit))
}

// parseOptionalID parses an optional ID parameter. It reports false for a
// value that isn't a number, which selects nothing.
func parseOptionalID(raw string) (int64, bool) {
	if raw == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

// validIPFilter reports whether value is an IP address or a CIDR range, as
// AccountFilter#valid_ip? accepts them.
func validIPFilter(value string) bool {
	if strings.Contains(value, "/") {
		_, _, err := net.ParseCIDR(value)
		return err == nil
	}
	return net.ParseIP(value) != nil
}
