package db

import (
	"strings"
	"testing"
)

func TestAdminSuspendAccountQuery_ManagesDeletionRequests(t *testing.T) {
	if !strings.Contains(adminSuspendAccount, "INSERT INTO account_deletion_requests") {
		t.Fatalf("AdminSuspendAccount query must insert account_deletion_requests row")
	}
	if !strings.Contains(adminSuspendAccount, "WHERE NOT EXISTS") {
		t.Fatalf("AdminSuspendAccount query must avoid duplicate account_deletion_requests rows")
	}
}

func TestAdminUnsuspendAccountQuery_CleansDeletionRequests(t *testing.T) {
	if !strings.Contains(adminUnsuspendAccount, "DELETE FROM account_deletion_requests") {
		t.Fatalf("AdminUnsuspendAccount query must delete account_deletion_requests rows")
	}
}
