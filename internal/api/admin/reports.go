package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	v1 "github.com/deicod/gotodon/internal/api/v1"
	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/oauth"
)

// ReportsHandler handles admin report endpoints
type ReportsHandler struct {
	queries *db.Queries
	roles   RoleLookup
}

// NewReportsHandler creates a new admin reports handler
func NewReportsHandler(queries *db.Queries) *ReportsHandler {
	return &ReportsHandler{
		queries: queries,
		roles:   NewRoleLookup(queries),
	}
}

// AdminReport represents a report in the admin view
type AdminReport struct {
	ID              string      `json:"id"`
	ActionTaken     bool        `json:"action_taken"`
	ActionTakenAt   *string     `json:"action_taken_at"`
	Category        string      `json:"category"`
	Comment         string      `json:"comment"`
	Forwarded       bool        `json:"forwarded"`
	StatusIDs       []string    `json:"status_ids"`
	RuleIDs         []string    `json:"rule_ids"`
	TargetAccountID string      `json:"target_account_id"`
	TargetAccount   *v1.Account `json:"target_account"`
	AccountID       string      `json:"account_id"`
	Account         *v1.Account `json:"account"`
	AssignedAccount *v1.Account `json:"assigned_account"`
	ActionTakenBy   *v1.Account `json:"action_taken_by_account"`
	CreatedAt       string      `json:"created_at"`
	UpdatedAt       string      `json:"updated_at"`
}

func mapCategory(c int32) string {
	switch c {
	case 1:
		return "spam"
	case 2:
		return "violation"
	default:
		return "other"
	}
}

func categoryFromString(value string) (int32, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "spam":
		return 1, true
	case "violation":
		return 2, true
	case "other":
		return 0, true
	default:
		return 0, false
	}
}

func idSliceToStrings(ids []int64) []string {
	if len(ids) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, strconv.FormatInt(id, 10))
	}
	return out
}

func accountFromUsername(id int64, username, domain string) *v1.Account {
	if id == 0 && username == "" {
		return nil
	}
	acct := username
	if domain != "" {
		acct = username + "@" + domain
	}
	return &v1.Account{
		ID:       strconv.FormatInt(id, 10),
		Username: username,
		Acct:     acct,
	}
}

func accountFromID(id int64) *v1.Account {
	if id <= 0 {
		return nil
	}
	idStr := strconv.FormatInt(id, 10)
	return &v1.Account{
		ID:       idStr,
		Username: idStr,
		Acct:     idStr,
	}
}

func accountFromDB(acc db.Account) *v1.Account {
	domain := ""
	if acc.Domain.Valid {
		domain = acc.Domain.String
	}
	return accountFromUsername(acc.ID, acc.Username, domain)
}

func (h *ReportsHandler) useDB() bool {
	return h.queries != nil
}

// GetAll handles GET /api/v1/admin/reports
func (h *ReportsHandler) GetAll(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if l := web.QueryParam(r, "limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	offset := 0
	if o := web.QueryParam(r, "offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	resolved := web.QueryParam(r, "resolved") == "true"
	accountID, _ := strconv.ParseInt(web.QueryParam(r, "account_id"), 10, 64)
	targetAccountID, _ := strconv.ParseInt(web.QueryParam(r, "target_account_id"), 10, 64)

	if h.useDB() {
		rows, err := h.queries.AdminListReports(r.Context(), db.AdminListReportsParams{
			Limit:           int32(limit),
			Offset:          int32(offset),
			Resolved:        resolved,
			AccountID:       accountID,
			TargetAccountID: targetAccountID,
		})
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}

		type reportMeta struct {
			index           int
			assignedID      pgtype.Int8
			actionTakenByID pgtype.Int8
		}

		reports := make([]AdminReport, len(rows))
		metas := make([]reportMeta, 0, len(rows))
		accountIDs := make([]int64, 0, len(rows)*2)
		accountIDSeen := make(map[int64]bool)

		for i, row := range rows {
			rep := AdminReport{
				ID:              strconv.FormatInt(row.ID, 10),
				ActionTaken:     row.ActionTakenAt.Valid,
				Category:        mapCategory(row.Category),
				Comment:         row.Comment,
				Forwarded:       row.Forwarded.Bool,
				TargetAccountID: strconv.FormatInt(row.TargetAccountID, 10),
				AccountID:       strconv.FormatInt(row.AccountID, 10),
				CreatedAt:       row.CreatedAt.Time.String(),
				UpdatedAt:       row.UpdatedAt.Time.String(),
				StatusIDs:       idSliceToStrings(row.StatusIds),
				RuleIDs:         idSliceToStrings(row.RuleIds),
			}

			if row.ActionTakenAt.Valid {
				t := row.ActionTakenAt.Time.String()
				rep.ActionTakenAt = &t
			}

			accountDomain := ""
			if row.AccountDomain.Valid {
				accountDomain = row.AccountDomain.String
			}
			rep.Account = accountFromUsername(row.AccountID, row.AccountUsername, accountDomain)

			targetDomain := ""
			if row.TargetAccountDomain.Valid {
				targetDomain = row.TargetAccountDomain.String
			}
			rep.TargetAccount = accountFromUsername(row.TargetAccountID, row.TargetAccountUsername, targetDomain)

			if row.AssignedAccountID.Valid && !accountIDSeen[row.AssignedAccountID.Int64] {
				accountIDs = append(accountIDs, row.AssignedAccountID.Int64)
				accountIDSeen[row.AssignedAccountID.Int64] = true
			}
			if row.ActionTakenByAccountID.Valid && !accountIDSeen[row.ActionTakenByAccountID.Int64] {
				accountIDs = append(accountIDs, row.ActionTakenByAccountID.Int64)
				accountIDSeen[row.ActionTakenByAccountID.Int64] = true
			}

			reports[i] = rep
			metas = append(metas, reportMeta{
				index:           i,
				assignedID:      row.AssignedAccountID,
				actionTakenByID: row.ActionTakenByAccountID,
			})
		}

		if len(accountIDs) > 0 {
			dbAccounts, err := h.queries.GetAccountsByIDs(r.Context(), accountIDs)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}

			accountMap := make(map[int64]*v1.Account, len(dbAccounts))
			for _, acc := range dbAccounts {
				accountMap[acc.ID] = accountFromDB(acc)
			}

			for _, meta := range metas {
				if meta.assignedID.Valid {
					if acc, ok := accountMap[meta.assignedID.Int64]; ok {
						reports[meta.index].AssignedAccount = acc
					}
				}
				if meta.actionTakenByID.Valid {
					if acc, ok := accountMap[meta.actionTakenByID.Int64]; ok {
						reports[meta.index].ActionTakenBy = acc
					}
				}
			}
		}

		web.JSON(w, http.StatusOK, reports)
		return
	}
	web.JSONError(w, http.StatusInternalServerError, "Report data unavailable")
}

// Get handles GET /api/v1/admin/reports/:id
func (h *ReportsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Not found")
		return
	}

	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Report data unavailable")
		return
	}

	row, err := h.queries.AdminGetReport(r.Context(), id)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Not found")
		return
	}

	accountIDs := []int64{row.AccountID, row.TargetAccountID}
	if row.AssignedAccountID.Valid {
		accountIDs = append(accountIDs, row.AssignedAccountID.Int64)
	}
	if row.ActionTakenByAccountID.Valid {
		accountIDs = append(accountIDs, row.ActionTakenByAccountID.Int64)
	}

	dbAccounts, err := h.queries.GetAccountsByIDs(r.Context(), accountIDs)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	accountMap := make(map[int64]*v1.Account, len(dbAccounts))
	for _, acc := range dbAccounts {
		accountMap[acc.ID] = accountFromDB(acc)
	}

	rep := AdminReport{
		ID:              strconv.FormatInt(row.ID, 10),
		ActionTaken:     row.ActionTakenAt.Valid,
		Category:        mapCategory(row.Category),
		Comment:         row.Comment,
		Forwarded:       row.Forwarded.Bool,
		TargetAccountID: strconv.FormatInt(row.TargetAccountID, 10),
		AccountID:       strconv.FormatInt(row.AccountID, 10),
		CreatedAt:       row.CreatedAt.Time.String(),
		UpdatedAt:       row.UpdatedAt.Time.String(),
		StatusIDs:       idSliceToStrings(row.StatusIds),
		RuleIDs:         idSliceToStrings(row.RuleIds),
	}

	if row.ActionTakenAt.Valid {
		t := row.ActionTakenAt.Time.String()
		rep.ActionTakenAt = &t
	}

	rep.Account = accountMap[row.AccountID]
	rep.TargetAccount = accountMap[row.TargetAccountID]

	if row.AssignedAccountID.Valid {
		rep.AssignedAccount = accountMap[row.AssignedAccountID.Int64]
	}
	if row.ActionTakenByAccountID.Valid {
		rep.ActionTakenBy = accountMap[row.ActionTakenByAccountID.Int64]
	}

	web.JSON(w, http.StatusOK, rep)
}

// Update handles PUT /api/v1/admin/reports/:id
func (h *ReportsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Not found")
		return
	}

	var payload map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request")
		return
	}

	var category pgtype.Int4
	if raw, ok := payload["category"]; ok {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			web.JSONError(w, http.StatusUnprocessableEntity, "Invalid category")
			return
		}
		mapped, ok := categoryFromString(value)
		if !ok {
			web.JSONError(w, http.StatusUnprocessableEntity, "Invalid category")
			return
		}
		category = pgtype.Int4{Int32: mapped, Valid: true}
	}

	var ruleIDs []int64
	if raw, ok := payload["rule_ids"]; ok {
		trimmed := strings.TrimSpace(string(raw))
		if trimmed == "" || trimmed == "null" {
			web.JSONError(w, http.StatusUnprocessableEntity, "Invalid rule_ids")
			return
		}

		var rawStrings []string
		if err := json.Unmarshal(raw, &rawStrings); err == nil {
			parsed := make([]int64, 0, len(rawStrings))
			for _, value := range rawStrings {
				value = strings.TrimSpace(value)
				if value == "" {
					web.JSONError(w, http.StatusUnprocessableEntity, "Invalid rule_ids")
					return
				}
				idValue, err := strconv.ParseInt(value, 10, 64)
				if err != nil || idValue <= 0 {
					web.JSONError(w, http.StatusUnprocessableEntity, "Invalid rule_ids")
					return
				}
				parsed = append(parsed, idValue)
			}
			ruleIDs = parsed
		} else {
			var rawInts []int64
			if err := json.Unmarshal(raw, &rawInts); err != nil {
				web.JSONError(w, http.StatusUnprocessableEntity, "Invalid rule_ids")
				return
			}
			for _, idValue := range rawInts {
				if idValue <= 0 {
					web.JSONError(w, http.StatusUnprocessableEntity, "Invalid rule_ids")
					return
				}
			}
			ruleIDs = rawInts
		}
	}

	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Report data unavailable")
		return
	}

	_, err = h.queries.AdminUpdateReport(r.Context(), db.AdminUpdateReportParams{
		ID:       id,
		Category: category,
		RuleIds:  ruleIDs,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			web.JSONError(w, http.StatusNotFound, "Not found")
			return
		}
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	h.Get(w, r)
}

// AssignToSelf handles POST /api/v1/admin/reports/:id/assign_to_self
func (h *ReportsHandler) AssignToSelf(w http.ResponseWriter, r *http.Request) {
	adminID, _ := middleware.GetAccountID(r.Context())

	id, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Not found")
		return
	}

	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Report data unavailable")
		return
	}

	err = h.queries.AdminAssignReport(r.Context(), db.AdminAssignReportParams{
		ID:                id,
		AssignedAccountID: pgtype.Int8{Int64: adminID, Valid: true},
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	h.Get(w, r)
}

// Unassign handles POST /api/v1/admin/reports/:id/unassign
func (h *ReportsHandler) Unassign(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Not found")
		return
	}

	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Report data unavailable")
		return
	}

	err = h.queries.AdminAssignReport(r.Context(), db.AdminAssignReportParams{
		ID:                id,
		AssignedAccountID: pgtype.Int8{Valid: false},
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	h.Get(w, r)
}

// Reopen handles POST /api/v1/admin/reports/:id/reopen
func (h *ReportsHandler) Reopen(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Not found")
		return
	}

	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Report data unavailable")
		return
	}

	if err := h.queries.AdminUnresolveReport(r.Context(), id); err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	h.Get(w, r)
}

// Resolve handles POST /api/v1/admin/reports/:id/resolve
func (h *ReportsHandler) Resolve(w http.ResponseWriter, r *http.Request) {
	adminID, _ := middleware.GetAccountID(r.Context())

	id, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Not found")
		return
	}

	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Report data unavailable")
		return
	}

	err = h.queries.AdminResolveReport(r.Context(), db.AdminResolveReportParams{
		ID:                     id,
		ActionTakenByAccountID: pgtype.Int8{Int64: adminID, Valid: true},
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	h.Get(w, r)
}

// RegisterRoutes registers admin report routes. Every action needs
// manage_reports, as in Mastodon's ReportPolicy. Reads need
// admin:read:reports and writes admin:write:reports, as in
// Api::V1::Admin::ReportsController.
func (h *ReportsHandler) RegisterRoutes(mux RouteRegistrar) {
	read := WithAccess(h.roles, oauth.ScopeAdminReadReports, auth.PermissionManageReports)
	write := WithAccess(h.roles, oauth.ScopeAdminWriteReports, auth.PermissionManageReports)

	mux.HandleFunc("GET /api/v1/admin/reports", read(h.GetAll))
	mux.HandleFunc("GET /api/v1/admin/reports/{id}", read(h.Get))
	mux.HandleFunc("PUT /api/v1/admin/reports/{id}", write(h.Update))
	mux.HandleFunc("PATCH /api/v1/admin/reports/{id}", write(h.Update))
	mux.HandleFunc("POST /api/v1/admin/reports/{id}/resolve", write(h.Resolve))
	mux.HandleFunc("POST /api/v1/admin/reports/{id}/assign_to_self", write(h.AssignToSelf))
	mux.HandleFunc("POST /api/v1/admin/reports/{id}/unassign", write(h.Unassign))
	mux.HandleFunc("POST /api/v1/admin/reports/{id}/reopen", write(h.Reopen))
}
