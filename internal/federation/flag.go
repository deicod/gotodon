package federation

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	apihandler "github.com/deicod/gotodon/internal/api/activitypub"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/mailer"
	"github.com/deicod/gotodon/internal/moderation"
	"github.com/deicod/gotodon/pkg/activitypub"
)

// maxFlagCommentLength is how many characters of a Flag's content its
// reports keep, as ActivityPub::Activity::Flag#report_comment cuts it.
const maxFlagCommentLength = 5000

// FlagStore describes the persistence needed for incoming Flag activities.
type FlagStore interface {
	CreateStore
	moderation.ReportNotificationStore
	DomainRejectsReports(ctx context.Context, domains []string) (bool, error)
	ListReportableStatusIDs(ctx context.Context, arg db.ListReportableStatusIDsParams) ([]int64, error)
	CreateRemoteReport(ctx context.Context, arg db.CreateRemoteReportParams) (db.Report, error)
}

// FlagService files the reports that incoming Flag activities make.
type FlagService struct {
	store       FlagStore
	localDomain string
	fetcher     activitypub.ActorFetcher
	helper      *CreateService
	mail        mailer.Deliverer
}

// NewFlagService creates a service for Flag activities.
func NewFlagService(store FlagStore, localDomain string, fetcher activitypub.ActorFetcher) *FlagService {
	return &FlagService{
		store:       store,
		localDomain: localDomain,
		fetcher:     fetcher,
		helper:      NewCreateService(store, localDomain, fetcher),
	}
}

// SetMailDeliverer enables new-report mails to the users who can manage
// reports, as POST /api/v1/reports sends them.
func (s *FlagService) SetMailDeliverer(deliverer mailer.Deliverer) {
	s.mail = deliverer
}

// flagTarget is an account a Flag reports, with the statuses of that account
// the Flag names.
type flagTarget struct {
	account  db.Account
	statuses []db.Status
}

// HandleFlag files the reports of a Flag activity, as Mastodon's
// ActivityPub::Activity::Flag does. The inbox has verified that the actor
// signed the delivery. Each account the object names gets a report from the
// actor's account, holding the activity's content as its comment and the
// named statuses of that account that the actor's server could have seen.
//
// The activity's id must be hosted with the actor: it identifies the reports,
// so a Flag delivered again files nothing new. A Flag is dropped, filing
// nothing, when a domain block rejects reports from the actor's domain. Its
// object's URIs are only looked up, never fetched: one that names no known
// account or status is skipped, and so are statuses whose author the Flag
// doesn't name. A suspended account gets no report, nor does a remote one
// unless one of its named statuses replies to a local account: those are the
// reports Mastodon forwards to the servers of the accounts replied to.
func (s *FlagService) HandleFlag(ctx context.Context, activity *activitypub.Activity) error {
	if activity == nil {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Invalid flag activity"}
	}

	actorURI := extractActorURI(activity.Actor)
	if actorURI == "" {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing flag actor"}
	}
	if s.localDomain == "" {
		return &apihandler.InboxError{Status: httpStatusInternalError, Message: "Local domain not configured"}
	}
	if isLocalURI(actorURI, s.localDomain) {
		return nil
	}
	if activity.ID == "" || !activitypub.SameHost(activity.ID, actorURI) {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Invalid flag id"}
	}

	objectURIs := flagObjectURIs(activity.ActivityObject)
	if len(objectURIs) == 0 {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing flag object"}
	}

	domain := activitypub.ExtractDomain(actorURI)
	rejected, err := s.store.DomainRejectsReports(ctx, domainBlockVariants(domain))
	if err != nil {
		return err
	}
	if rejected {
		return nil
	}

	reporter, err := s.ensureReporter(ctx, actorURI)
	if err != nil {
		return err
	}
	if reporter.SuspendedAt.Valid {
		return &apihandler.InboxError{Status: httpStatusForbidden, Message: "Actor is suspended"}
	}

	targets, err := s.resolveTargets(ctx, objectURIs)
	if err != nil {
		return err
	}

	comment := flagComment(activity.Content)
	for _, target := range targets {
		if target.account.SuspendedAt.Valid {
			continue
		}
		if target.account.Domain.Valid {
			replies, err := s.repliesToLocalAccount(ctx, target.statuses)
			if err != nil {
				return err
			}
			if !replies {
				continue
			}
		}

		statusIDs := []int64{}
		if len(target.statuses) > 0 {
			named := make([]int64, len(target.statuses))
			for i, status := range target.statuses {
				named[i] = status.ID
			}
			statusIDs, err = s.store.ListReportableStatusIDs(ctx, db.ListReportableStatusIDsParams{
				AccountID: target.account.ID,
				StatusIds: named,
				Domain:    domain,
			})
			if err != nil {
				return err
			}
		}

		report, err := s.store.CreateRemoteReport(ctx, db.CreateRemoteReportParams{
			AccountID:       reporter.ID,
			TargetAccountID: target.account.ID,
			StatusIds:       statusIDs,
			Comment:         comment,
			Uri:             activity.ID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// The reporter's report of this account under this id exists.
			continue
		}
		if err != nil {
			return err
		}
		moderation.NotifyNewReport(ctx, s.store, s.mail, report)
	}
	return nil
}

func (s *FlagService) ensureReporter(ctx context.Context, actorURI string) (db.Account, error) {
	if s.fetcher == nil {
		return db.Account{}, &apihandler.InboxError{Status: httpStatusInternalError, Message: "Actor fetcher unavailable"}
	}
	actor, err := s.fetcher.FetchActor(ctx, actorURI)
	if err != nil {
		return db.Account{}, &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Actor not found"}
	}
	return s.helper.ensureActorAccount(ctx, actorURI, actor)
}

// resolveTargets looks up the accounts and statuses that uris name. It
// returns the accounts in the order the Flag names them, each with its named
// statuses, also in order. Statuses of accounts the Flag doesn't name are
// left out.
func (s *FlagService) resolveTargets(ctx context.Context, uris []string) ([]flagTarget, error) {
	var targets []flagTarget
	index := make(map[int64]int)
	var statuses []db.Status
	for _, uri := range uris {
		account, err := s.store.GetAccountByURI(ctx, uri)
		if err == nil {
			if _, seen := index[account.ID]; !seen {
				index[account.ID] = len(targets)
				targets = append(targets, flagTarget{account: account})
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}

		status, err := s.store.GetStatusByURI(ctx, pgtype.Text{String: uri, Valid: true})
		if err == nil {
			statuses = append(statuses, status)
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}

	seen := make(map[int64]bool)
	for _, status := range statuses {
		i, named := index[status.AccountID]
		if !named || seen[status.ID] {
			continue
		}
		seen[status.ID] = true
		targets[i].statuses = append(targets[i].statuses, status)
	}
	return targets, nil
}

// repliesToLocalAccount reports whether one of statuses replies to a local
// account.
func (s *FlagService) repliesToLocalAccount(ctx context.Context, statuses []db.Status) (bool, error) {
	checked := make(map[int64]bool)
	for _, status := range statuses {
		if !status.InReplyToAccountID.Valid {
			continue
		}
		id := status.InReplyToAccountID.Int64
		if checked[id] {
			continue
		}
		checked[id] = true
		account, err := s.store.GetAccountByID(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return false, err
		}
		if !account.Domain.Valid {
			return true, nil
		}
	}
	return false, nil
}

// flagObjectURIs returns the URIs a Flag's object names, once each, in order.
// The object is one URI or object, or an array of them; entries without a URI
// are skipped.
func flagObjectURIs(object any) []string {
	var items []any
	switch value := object.(type) {
	case []any:
		items = value
	case nil:
		return nil
	default:
		items = []any{value}
	}

	seen := make(map[string]bool)
	uris := make([]string, 0, len(items))
	for _, item := range items {
		uri := strings.TrimSpace(extractObjectURI(item))
		if uri == "" || seen[uri] {
			continue
		}
		seen[uri] = true
		uris = append(uris, uri)
	}
	return uris
}

// flagComment is the comment of the reports a Flag whose content is content
// files: its first maxFlagCommentLength characters, or "" when the content is
// not text.
func flagComment(content any) string {
	text, ok := content.(string)
	if !ok {
		return ""
	}
	runes := []rune(text)
	if len(runes) > maxFlagCommentLength {
		return string(runes[:maxFlagCommentLength])
	}
	return text
}

// domainBlockVariants returns the lowercase host of domain, which may carry a
// port, followed by each of its parent domains, as Mastodon's
// DomainBlock.rule_for matches blocks against them.
func domainBlockVariants(domain string) []string {
	host := strings.ToLower(strings.TrimSpace(domain))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return []string{}
	}
	segments := strings.Split(host, ".")
	variants := make([]string, 0, len(segments))
	for i := range segments {
		variants = append(variants, strings.Join(segments[i:], "."))
	}
	return variants
}
