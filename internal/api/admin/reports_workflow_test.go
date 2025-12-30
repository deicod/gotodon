package admin

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	v1 "github.com/deicod/gotodon/internal/api/v1"
)

func seedMemReport(h *ReportsHandler, id int64) *memReport {
	timestamp := time.Unix(1_700_000_000, 0).UTC()
	report := &memReport{
		ID:              id,
		StatusIDs:       []int64{10},
		RuleIDs:         []int64{3},
		Comment:         "test report",
		Forwarded:       false,
		Category:        1,
		AccountID:       111,
		TargetAccountID: 222,
		Account: &v1.Account{
			ID:       "111",
			Username: "reporter",
			Acct:     "reporter",
		},
		TargetAccount: &v1.Account{
			ID:       "222",
			Username: "target",
			Acct:     "target",
		},
		CreatedAt: timestamp.Add(-time.Hour),
		UpdatedAt: timestamp.Add(-time.Hour),
	}
	h.memReports[id] = report
	return report
}

func TestReportsHandler_MockMode_Workflow(t *testing.T) {
	h := NewReportsHandler(nil)
	seedMemReport(h, 42)

	adminID := int64(12345)
	adminIDStr := strconv.FormatInt(adminID, 10)

	req, rec := mockAdminRequest(http.MethodPut, "/api/v1/admin/reports/42", `{"category":"violation","rule_ids":["5","6"]}`)
	req.SetPathValue("id", "42")
	h.Update(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, want %d", rec.Code, http.StatusOK)
	}

	var updated AdminReport
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if updated.Category != "violation" {
		t.Fatalf("category = %q, want %q", updated.Category, "violation")
	}
	if len(updated.RuleIDs) != 2 || updated.RuleIDs[0] != "5" || updated.RuleIDs[1] != "6" {
		t.Fatalf("rule_ids = %v, want [5 6]", updated.RuleIDs)
	}

	req, rec = mockAdminRequest(http.MethodPost, "/api/v1/admin/reports/42/assign_to_self", "")
	req.SetPathValue("id", "42")
	h.AssignToSelf(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("assign status = %d, want %d", rec.Code, http.StatusOK)
	}

	var assigned AdminReport
	if err := json.Unmarshal(rec.Body.Bytes(), &assigned); err != nil {
		t.Fatalf("decode assign response: %v", err)
	}
	if assigned.AssignedAccount == nil || assigned.AssignedAccount.ID != adminIDStr {
		t.Fatalf("assigned_account.id = %v, want %s", assigned.AssignedAccount, adminIDStr)
	}

	req, rec = mockAdminRequest(http.MethodPost, "/api/v1/admin/reports/42/unassign", "")
	req.SetPathValue("id", "42")
	h.Unassign(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unassign status = %d, want %d", rec.Code, http.StatusOK)
	}

	var unassigned AdminReport
	if err := json.Unmarshal(rec.Body.Bytes(), &unassigned); err != nil {
		t.Fatalf("decode unassign response: %v", err)
	}
	if unassigned.AssignedAccount != nil {
		t.Fatalf("assigned_account = %v, want nil", unassigned.AssignedAccount)
	}

	req, rec = mockAdminRequest(http.MethodPost, "/api/v1/admin/reports/42/resolve", "")
	req.SetPathValue("id", "42")
	h.Resolve(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("resolve status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resolved AdminReport
	if err := json.Unmarshal(rec.Body.Bytes(), &resolved); err != nil {
		t.Fatalf("decode resolve response: %v", err)
	}
	if !resolved.ActionTaken {
		t.Fatalf("action_taken = false, want true")
	}
	if resolved.ActionTakenBy == nil || resolved.ActionTakenBy.ID != adminIDStr {
		t.Fatalf("action_taken_by_account.id = %v, want %s", resolved.ActionTakenBy, adminIDStr)
	}

	req, rec = mockAdminRequest(http.MethodPost, "/api/v1/admin/reports/42/reopen", "")
	req.SetPathValue("id", "42")
	h.Reopen(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("reopen status = %d, want %d", rec.Code, http.StatusOK)
	}

	var reopened AdminReport
	if err := json.Unmarshal(rec.Body.Bytes(), &reopened); err != nil {
		t.Fatalf("decode reopen response: %v", err)
	}
	if reopened.ActionTaken {
		t.Fatalf("action_taken = true, want false")
	}
	if reopened.ActionTakenBy != nil {
		t.Fatalf("action_taken_by_account = %v, want nil", reopened.ActionTakenBy)
	}
	if reopened.ActionTakenAt != nil {
		t.Fatalf("action_taken_at = %v, want nil", reopened.ActionTakenAt)
	}
}

func TestReportsHandler_Update_InvalidCategory(t *testing.T) {
	h := NewReportsHandler(nil)
	seedMemReport(h, 7)

	req, rec := mockAdminRequest(http.MethodPut, "/api/v1/admin/reports/7", `{"category":"nope"}`)
	req.SetPathValue("id", "7")
	h.Update(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestReportsHandler_Update_InvalidRuleIDs(t *testing.T) {
	h := NewReportsHandler(nil)
	seedMemReport(h, 9)

	cases := []string{
		`{"rule_ids":["nope"]}`,
		`{"rule_ids":[-1]}`,
		`{"rule_ids":null}`,
	}

	for i, body := range cases {
		req, rec := mockAdminRequest(http.MethodPut, "/api/v1/admin/reports/9", body)
		req.SetPathValue("id", "9")
		h.Update(rec, req)

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("case %d status = %d, want %d", i, rec.Code, http.StatusUnprocessableEntity)
		}
	}
}
