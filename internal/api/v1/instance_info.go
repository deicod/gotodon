// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer"
	htmlrenderer "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

// InstanceInfoHandler handles instance information endpoints
type InstanceInfoHandler struct {
	queries instanceInfoQuerier
	cfg     *config.Config
}

// NewInstanceInfoHandler creates a new instance info handler
func NewInstanceInfoHandler(queries instanceInfoQuerier, cfg *config.Config) *InstanceInfoHandler {
	return &InstanceInfoHandler{queries: queries, cfg: cfg}
}

// RuleResponse represents a server rule in API responses
type RuleResponse struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Hint string `json:"hint,omitempty"`
}

// ActivityWeek represents weekly activity statistics
type ActivityWeek struct {
	Week          string `json:"week"`
	Statuses      string `json:"statuses"`
	Logins        string `json:"logins"`
	Registrations string `json:"registrations"`
}

// ExtendedDescription represents extended instance description
type ExtendedDescription struct {
	UpdatedAt *time.Time `json:"updated_at"`
	Content   string     `json:"content"`
}

// PrivacyPolicy represents the instance privacy policy
type PrivacyPolicy struct {
	UpdatedAt *time.Time `json:"updated_at"`
	Content   string     `json:"content"`
}

// TermsOfService represents the instance terms of service
type TermsOfService struct {
	EffectiveDate string  `json:"effective_date"`
	Effective     bool    `json:"effective"`
	Content       string  `json:"content"`
	SucceededBy   *string `json:"succeeded_by"`
}

type InstanceDomainBlock struct {
	Domain   string `json:"domain"`
	Digest   string `json:"digest"`
	Severity string `json:"severity"`
	Comment  string `json:"comment,omitempty"`
}

const (
	settingSiteExtendedDescription   = "site_extended_description"
	settingSiteTerms                 = "site_terms"
	settingShowDomainBlocks          = "show_domain_blocks"
	settingShowDomainBlocksRationale = "show_domain_blocks_rationale"
)

var privacyPolicyDefaultUpdatedAt = time.Date(2022, 10, 7, 0, 0, 0, 0, time.UTC)

const privacyPolicyDefaultTemplate = `This privacy policy describes how %{domain} ("%{domain}", "we", "us") collects, protects, and uses the personally identifiable information you may provide through the %{domain} website or its services.

We are committed to ensuring that your privacy is protected. Should we ask you to provide certain information by which you can be identified when using this website or its services, you can be assured it will only be used in accordance with this privacy statement.

We may change this policy from time to time by updating this page. You should check this page from time to time to ensure that you are happy with any changes. This policy is effective from 7 October 2022.

## What we collect

We may collect the following information:

- Name and email address
- Public posts and media
- IP address and browser user agent string
- Cookies

## What we do with the information we gather

We require this information to understand your needs and provide you with a better service, and in particular for the following reasons:

- Internal record keeping
- To improve the products and services we provide
- We may use the email address to send notices about the service
- For administration and security

## Security

We are committed to ensuring that your information is secure. In order to prevent unauthorized access or disclosure, we have put in place suitable physical, electronic, and managerial procedures to safeguard and secure the information we collect online.

## Cookies

A cookie is a small file which asks permission to be placed on your computer's hard drive. Once you agree, the file is added and the cookie helps analyze web traffic or lets you know when you visit a particular site. Cookies allow web applications to respond to you as an individual.

We use traffic log cookies to identify which pages are being used. This helps us analyze data about web page traffic and improve our website in order to tailor it to customer needs. We only use this information for statistical analysis purposes and then the data is removed from the system.

Overall, cookies help us provide you with a better website, by enabling us to monitor which pages you find useful and which you do not. A cookie in no way gives us access to your computer or any information about you, other than the data you choose to share with us.

You can choose to accept or decline cookies. Most web browsers automatically accept cookies, but you can usually modify your browser setting to decline cookies if you prefer. This may prevent you from taking full advantage of the website.

## Links to other websites

Our website or its services may contain links to other websites of interest. However, once you have used these links to leave our site, you should note that we do not have any control over that other website. Therefore, we cannot be responsible for the protection and privacy of any information which you provide whilst visiting such sites and such sites are not governed by this privacy statement. You should exercise caution and look at the privacy statement applicable to the website in question.

## Controlling your personal information

You may choose to restrict the collection or use of your personal information in the following ways:

- You may request access to your personal information in the data export
- You may request deletion of your personal information in the data export
- You may request correction of your personal information by updating your profile

We will not sell, distribute or lease your personal information to third parties unless we have your permission or are required by law to do so. We may use your personal information to send you promotional information about third parties which we think you may find interesting if you tell us that you wish this to happen.

You may request details of personal information which we hold about you under the General Data Protection Regulation (GDPR). A small fee will be payable. If you would like a copy of the information held on you please email us.

If you believe that any information we are holding on you is incorrect or incomplete, please write to or email us as soon as possible, at the above address. We will promptly correct any information found to be incorrect.

This document is CC-BY-SA. Originally adapted from the [Discourse privacy policy](https://meta.discourse.org/t/93694).`

var termsMarkdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(
		renderer.WithNodeRenderers(util.Prioritized(&termsImageRenderer{}, 1000)),
	),
)

var extendedDescriptionMarkdown = goldmark.New(
	goldmark.WithRendererOptions(htmlrenderer.WithUnsafe()),
)

type termsImageRenderer struct{}

func (r *termsImageRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindImage, r.renderImage)
}

func (r *termsImageRenderer) renderImage(_ util.BufWriter, _ []byte, _ ast.Node, _ bool) (ast.WalkStatus, error) {
	return ast.WalkSkipChildren, nil
}

func renderTermsMarkdown(text string) string {
	if text == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := termsMarkdown.Convert([]byte(text), &buf); err != nil {
		return "<p>" + html.EscapeString(text) + "</p>"
	}
	return buf.String()
}

func renderExtendedDescription(text string) string {
	if text == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := extendedDescriptionMarkdown.Convert([]byte(text), &buf); err != nil {
		return "<p>" + html.EscapeString(text) + "</p>"
	}
	return buf.String()
}

func formatTermsContent(text, domain string) string {
	if domain != "" {
		text = strings.ReplaceAll(text, "%{domain}", domain)
	}
	return renderTermsMarkdown(text)
}

// Rules handles GET /api/v1/instance/rules
func (h *InstanceInfoHandler) Rules(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Instance info unavailable")
		return
	}

	ctx := r.Context()
	rules, err := h.queries.ListRules(ctx)
	if err != nil {
		web.JSON(w, http.StatusOK, []RuleResponse{})
		return
	}

	result := make([]RuleResponse, len(rules))
	for i, rule := range rules {
		result[i] = RuleResponse{
			ID:   strconv.FormatInt(rule.ID, 10),
			Text: rule.Text,
			Hint: rule.Hint,
		}
	}

	web.JSON(w, http.StatusOK, result)
}

// Peers handles GET /api/v1/instance/peers
func (h *InstanceInfoHandler) Peers(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Instance info unavailable")
		return
	}

	ctx := r.Context()

	// Get distinct domains from accounts table
	dbDomains, err := h.queries.GetKnownDomains(ctx)
	if err != nil {
		web.JSON(w, http.StatusOK, []string{})
		return
	}

	// Convert pgtype.Text to plain strings
	domains := make([]string, 0, len(dbDomains))
	for _, d := range dbDomains {
		if d.Valid {
			domains = append(domains, d.String)
		}
	}

	web.JSON(w, http.StatusOK, domains)
}

// Activity handles GET /api/v1/instance/activity
func (h *InstanceInfoHandler) Activity(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Instance info unavailable")
		return
	}
	// Return last 12 weeks of activity
	// In a production system, this would query aggregated statistics

	now := time.Now()
	weeks := make([]ActivityWeek, 12)

	for i := 0; i < 12; i++ {
		weekStart := now.AddDate(0, 0, -7*(i+1))
		weeks[i] = ActivityWeek{
			Week:          strconv.FormatInt(weekStart.Unix(), 10),
			Statuses:      "0",
			Logins:        "0",
			Registrations: "0",
		}
	}

	if h.queries != nil {
		// Get actual activity stats if database available
		ctx := r.Context()
		for i := 0; i < 12; i++ {
			weekStart := now.AddDate(0, 0, -7*(i+1))
			weekEnd := now.AddDate(0, 0, -7*i)

			// Get status count for this week
			statusCount, err := h.queries.CountStatusesInRange(ctx, db.CountStatusesInRangeParams{
				CreatedAt:   pgtype.Timestamptz{Time: weekStart, Valid: true},
				CreatedAt_2: pgtype.Timestamptz{Time: weekEnd, Valid: true},
			})
			if err == nil {
				weeks[i].Statuses = strconv.FormatInt(statusCount, 10)
			}

			loginCount, err := h.queries.CountUserLoginsInRange(ctx, db.CountUserLoginsInRangeParams{
				CurrentSignInAt:   pgtype.Timestamptz{Time: weekStart, Valid: true},
				CurrentSignInAt_2: pgtype.Timestamptz{Time: weekEnd, Valid: true},
			})
			if err == nil {
				weeks[i].Logins = strconv.FormatInt(loginCount, 10)
			}

			registrationCount, err := h.queries.CountUserRegistrationsInRange(ctx, db.CountUserRegistrationsInRangeParams{
				CreatedAt:   pgtype.Timestamptz{Time: weekStart, Valid: true},
				CreatedAt_2: pgtype.Timestamptz{Time: weekEnd, Valid: true},
			})
			if err == nil {
				weeks[i].Registrations = strconv.FormatInt(registrationCount, 10)
			}
		}
	}

	web.JSON(w, http.StatusOK, weeks)
}

// ExtendedDescription handles GET /api/v1/instance/extended_description
func (h *InstanceInfoHandler) ExtendedDescriptionHandler(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Instance info unavailable")
		return
	}
	desc := extendedDescriptionFromSettings(r.Context(), h.queries)
	web.JSON(w, http.StatusOK, desc)
}

// PrivacyPolicy handles GET /api/v1/instance/privacy_policy
func (h *InstanceInfoHandler) PrivacyPolicyHandler(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Instance info unavailable")
		return
	}
	domain := instanceFederationDomain(h.cfg)
	policy := privacyPolicyFromSettings(r.Context(), h.queries, domain)
	web.JSON(w, http.StatusOK, policy)
}

// TermsOfService handles GET /api/v1/instance/terms_of_service
func (h *InstanceInfoHandler) TermsOfServiceHandler(w http.ResponseWriter, r *http.Request) {
	h.termsOfService(w, r, "")
}

// TermsOfServiceByDate handles GET /api/v1/instance/terms_of_service/:date
func (h *InstanceInfoHandler) TermsOfServiceByDate(w http.ResponseWriter, r *http.Request) {
	date := web.Param(r, "date")
	h.termsOfService(w, r, date)
}

func (h *InstanceInfoHandler) termsOfService(w http.ResponseWriter, r *http.Request, date string) {
	var parsed *time.Time
	if date != "" {
		parsedDate, parseErr := time.Parse("2006-01-02", date)
		if parseErr != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		parsed = &parsedDate
	}

	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Instance info unavailable")
		return
	}

	ctx := r.Context()
	var tos db.TermsOfService
	var err error

	if parsed != nil {
		tos, err = h.queries.GetTermsOfServiceByEffectiveDate(ctx, pgtype.Date{Time: *parsed, Valid: true})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				web.JSONError(w, http.StatusNotFound, "Record not found")
				return
			}
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}
	} else {
		tos, err = h.queries.GetCurrentTermsOfService(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			tos, err = h.queries.GetUpcomingTermsOfService(ctx)
		}
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				web.JSONError(w, http.StatusNotFound, "Record not found")
				return
			}
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}
	}

	resp := h.serializeTermsOfService(ctx, tos)
	web.JSON(w, http.StatusOK, resp)
}

func (h *InstanceInfoHandler) serializeTermsOfService(ctx context.Context, tos db.TermsOfService) TermsOfService {
	domain := ""
	if h.cfg != nil {
		domain = h.cfg.Instance.LocalDomain
	}

	effectiveDate := ""
	if tos.EffectiveDate.Valid {
		effectiveDate = tos.EffectiveDate.Time.Format("2006-01-02")
	} else if tos.PublishedAt.Valid {
		effectiveDate = tos.PublishedAt.Time.Format(time.RFC3339)
	}

	effective := false
	now := time.Now().UTC()
	if tos.EffectiveDate.Valid {
		today := now.Format("2006-01-02")
		effective = tos.EffectiveDate.Time.Format("2006-01-02") <= today
	} else if tos.PublishedAt.Valid {
		effective = !tos.PublishedAt.Time.After(now)
	}

	var succeededBy *string
	if tos.EffectiveDate.Valid && h.queries != nil {
		next, err := h.queries.GetTermsOfServiceSucceededBy(ctx, db.GetTermsOfServiceSucceededByParams{
			EffectiveDate: tos.EffectiveDate,
			ID:            tos.ID,
		})
		if err == nil && next.EffectiveDate.Valid {
			date := next.EffectiveDate.Time.Format("2006-01-02")
			succeededBy = &date
		}
	}

	return TermsOfService{
		EffectiveDate: effectiveDate,
		Effective:     effective,
		Content:       formatTermsContent(tos.Text, domain),
		SucceededBy:   succeededBy,
	}
}

type instanceInfoQuerier interface {
	GetSettingsByVars(ctx context.Context, vars []string) ([]db.GetSettingsByVarsRow, error)
	ListRules(ctx context.Context) ([]db.Rule, error)
	GetKnownDomains(ctx context.Context) ([]pgtype.Text, error)
	CountStatusesInRange(ctx context.Context, arg db.CountStatusesInRangeParams) (int64, error)
	CountUserRegistrationsInRange(ctx context.Context, arg db.CountUserRegistrationsInRangeParams) (int64, error)
	CountUserLoginsInRange(ctx context.Context, arg db.CountUserLoginsInRangeParams) (int64, error)
	ListPublicDomainBlocks(ctx context.Context) ([]db.DomainBlock, error)
	GetTermsOfServiceByEffectiveDate(ctx context.Context, effectiveDate pgtype.Date) (db.TermsOfService, error)
	GetCurrentTermsOfService(ctx context.Context) (db.TermsOfService, error)
	GetUpcomingTermsOfService(ctx context.Context) (db.TermsOfService, error)
	GetTermsOfServiceSucceededBy(ctx context.Context, arg db.GetTermsOfServiceSucceededByParams) (db.TermsOfService, error)
}

func extendedDescriptionFromSettings(ctx context.Context, q instanceInfoQuerier) ExtendedDescription {
	if q == nil {
		return ExtendedDescription{
			UpdatedAt: nil,
			Content:   "",
		}
	}

	settings, err := q.GetSettingsByVars(ctx, []string{settingSiteExtendedDescription})
	if err != nil {
		return ExtendedDescription{
			UpdatedAt: nil,
			Content:   "",
		}
	}

	for _, row := range settings {
		if row.Var != settingSiteExtendedDescription || !row.Value.Valid {
			continue
		}
		text := strings.TrimSpace(row.Value.String)
		if text == "" {
			continue
		}
		var updatedAt *time.Time
		if row.UpdatedAt.Valid {
			t := row.UpdatedAt.Time
			updatedAt = &t
		}
		return ExtendedDescription{
			UpdatedAt: updatedAt,
			Content:   renderExtendedDescription(text),
		}
	}

	return ExtendedDescription{
		UpdatedAt: nil,
		Content:   "",
	}
}

func privacyPolicyFromSettings(ctx context.Context, q instanceInfoQuerier, domain string) PrivacyPolicy {
	if q == nil {
		updated := privacyPolicyDefaultUpdatedAt
		return PrivacyPolicy{
			UpdatedAt: &updated,
			Content:   formatTermsContent(privacyPolicyDefaultTemplate, domain),
		}
	}

	settings, err := q.GetSettingsByVars(ctx, []string{settingSiteTerms})
	if err != nil {
		updated := privacyPolicyDefaultUpdatedAt
		return PrivacyPolicy{
			UpdatedAt: &updated,
			Content:   formatTermsContent(privacyPolicyDefaultTemplate, domain),
		}
	}

	for _, row := range settings {
		if row.Var != settingSiteTerms || !row.Value.Valid {
			continue
		}
		text := strings.TrimSpace(row.Value.String)
		if text == "" {
			continue
		}
		var updatedAt *time.Time
		if row.UpdatedAt.Valid {
			t := row.UpdatedAt.Time
			updatedAt = &t
		}
		return PrivacyPolicy{
			UpdatedAt: updatedAt,
			Content:   formatTermsContent(text, domain),
		}
	}

	updated := privacyPolicyDefaultUpdatedAt
	return PrivacyPolicy{
		UpdatedAt: &updated,
		Content:   formatTermsContent(privacyPolicyDefaultTemplate, domain),
	}
}

func instanceDomainBlockSettings(ctx context.Context, q instanceInfoQuerier) (string, string) {
	showBlocks := "disabled"
	showRationale := "disabled"
	if q == nil {
		return showBlocks, showRationale
	}

	settings, err := q.GetSettingsByVars(ctx, []string{settingShowDomainBlocks, settingShowDomainBlocksRationale})
	if err != nil {
		return showBlocks, showRationale
	}

	for _, row := range settings {
		if !row.Value.Valid {
			continue
		}
		value := strings.ToLower(strings.TrimSpace(row.Value.String))
		switch row.Var {
		case settingShowDomainBlocks:
			if value != "" {
				showBlocks = value
			}
		case settingShowDomainBlocksRationale:
			if value != "" {
				showRationale = value
			}
		}
	}

	return showBlocks, showRationale
}

func domainBlockSeverity(severity int32) string {
	switch severity {
	case 0:
		return "silence"
	case 1:
		return "suspend"
	case 2:
		return "noop"
	default:
		return "noop"
	}
}

func domainBlockDigest(domain string) string {
	hash := sha256.Sum256([]byte(domain))
	return hex.EncodeToString(hash[:])
}

func domainBlockPublicDomain(domain string, obfuscate bool) string {
	if !obfuscate {
		return domain
	}
	chars := []rune(domain)
	length := len(chars)
	if length == 0 {
		return domain
	}
	visibleRatio := length / 4
	result := make([]rune, len(chars))
	for i, chr := range chars {
		if i > visibleRatio && i < length-visibleRatio && chr != '.' {
			result[i] = '*'
		} else {
			result[i] = chr
		}
	}
	return string(result)
}

// TranslationLanguages handles GET /api/v1/instance/translation_languages
func (h *InstanceInfoHandler) TranslationLanguages(w http.ResponseWriter, r *http.Request) {
	// Return empty map since translation is not enabled
	web.JSON(w, http.StatusOK, map[string][]string{})
}

// Languages handles GET /api/v1/instance/languages
func (h *InstanceInfoHandler) Languages(w http.ResponseWriter, r *http.Request) {
	// Return available languages for posting
	web.JSON(w, http.StatusOK, []string{"en"})
}

// DomainBlocksPublic handles GET /api/v1/instance/domain_blocks (public info if enabled)
func (h *InstanceInfoHandler) DomainBlocksPublic(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID, authenticated := middleware.GetAccountID(ctx)
	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Instance info unavailable")
		return
	}

	showBlocks, showRationale := instanceDomainBlockSettings(ctx, h.queries)
	allowed := false
	switch showBlocks {
	case "all":
		allowed = true
	case "users":
		allowed = authenticated && accountID > 0
	}

	if !allowed {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if h.queries == nil {
		web.JSON(w, http.StatusOK, []InstanceDomainBlock{})
		return
	}

	blocks, err := h.queries.ListPublicDomainBlocks(ctx)
	if err != nil {
		web.JSON(w, http.StatusOK, []InstanceDomainBlock{})
		return
	}

	includeComment := false
	switch showRationale {
	case "all":
		includeComment = true
	case "users":
		includeComment = authenticated && accountID > 0
	}

	resp := make([]InstanceDomainBlock, 0, len(blocks))
	for _, block := range blocks {
		comment := ""
		if includeComment && block.PublicComment.Valid {
			comment = strings.TrimSpace(block.PublicComment.String)
		}
		resp = append(resp, InstanceDomainBlock{
			Domain:   domainBlockPublicDomain(block.Domain, block.Obfuscate),
			Digest:   domainBlockDigest(block.Domain),
			Severity: domainBlockSeverity(block.Severity),
			Comment:  comment,
		})
	}

	web.JSON(w, http.StatusOK, resp)
}

// RegisterRoutes registers the instance info routes
func (h *InstanceInfoHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/instance/rules", h.Rules)
	mux.HandleFunc("GET /api/v1/instance/peers", h.Peers)
	mux.HandleFunc("GET /api/v1/instance/activity", h.Activity)
	mux.HandleFunc("GET /api/v1/instance/extended_description", h.ExtendedDescriptionHandler)
	mux.HandleFunc("GET /api/v1/instance/privacy_policy", h.PrivacyPolicyHandler)
	mux.HandleFunc("GET /api/v1/instance/terms_of_service", h.TermsOfServiceHandler)
	mux.HandleFunc("GET /api/v1/instance/terms_of_service/{date}", h.TermsOfServiceByDate)
	mux.HandleFunc("GET /api/v1/instance/translation_languages", h.TranslationLanguages)
	mux.HandleFunc("GET /api/v1/instance/languages", h.Languages)
	mux.HandleFunc("GET /api/v1/instance/domain_blocks", h.DomainBlocksPublic)
}
