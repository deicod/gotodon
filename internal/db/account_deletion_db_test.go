package db

import (
	"context"
	"fmt"
	"testing"
)

// TestAccountDeletionQueries checks the queries of account deletion that
// the end-to-end deletion tests in internal/workers don't drive to their
// edges: paging, keep lists given as NULL, batch limits and what they
// leave of other accounts.
func TestAccountDeletionQueries(t *testing.T) {
	conn := openMigratedDB(t)
	q := New(conn)
	ctx := context.Background()
	exec := func(sql string, args ...any) int64 {
		t.Helper()
		var id int64
		if err := conn.QueryRow(ctx, sql, args...).Scan(&id); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return id
	}
	remote := func(username, domain, inbox, shared string) int64 {
		return exec(`INSERT INTO accounts (username, domain, uri, inbox_url, shared_inbox_url) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			username, domain, "https://"+domain+"/users/"+username, inbox, shared)
	}

	alice := exec(`INSERT INTO accounts (username, uri) VALUES ('alice', 'https://local.test/users/alice') RETURNING id`)
	carol := exec(`INSERT INTO accounts (username, uri) VALUES ('carol', 'https://local.test/users/carol') RETURNING id`)
	remote("bob", "a.example", "https://a.example/users/bob/inbox", "https://a.example/inbox")
	remote("dan", "a.example", "https://a.example/users/dan/inbox", "https://a.example/inbox")
	remote("erin", "b.example", "https://b.example/users/erin/inbox", "")
	remote("fay", "c.example", "", "")
	gone := remote("gil", "d.example", "https://d.example/inbox", "")
	if _, err := conn.Exec(ctx, `UPDATE accounts SET suspended_at = now() WHERE id = $1`, gone); err != nil {
		t.Fatal(err)
	}

	t.Run("ListRemoteInboxes", func(t *testing.T) {
		var all []string
		after := ""
		for page := 0; page < 5; page++ {
			inboxes, err := q.ListRemoteInboxes(ctx, ListRemoteInboxesParams{After: after, Limit: 1})
			if err != nil {
				t.Fatalf("ListRemoteInboxes: %v", err)
			}
			if len(inboxes) == 0 {
				break
			}
			all = append(all, inboxes...)
			after = inboxes[len(inboxes)-1]
		}
		// A shared inbox once, a personal inbox where there is none, and
		// nothing of local, inbox-less or suspended accounts.
		if want := "[https://a.example/inbox https://b.example/users/erin/inbox]"; fmt.Sprint(all) != want {
			t.Fatalf("inboxes = %v, want %s", all, want)
		}
	})

	statusOf := func(account int64) int64 {
		id := exec(`INSERT INTO statuses (account_id, text) VALUES ($1, 'hi') RETURNING id`, account)
		exec(`INSERT INTO status_stats (status_id) VALUES ($1) RETURNING id`, id)
		return id
	}
	first, second, third := statusOf(alice), statusOf(alice), statusOf(alice)
	carols := statusOf(carol)

	t.Run("ListUnresolvedReportedStatusIDs", func(t *testing.T) {
		exec(`INSERT INTO reports (account_id, target_account_id, status_ids) VALUES ($1, $2, ARRAY[$3::bigint, $4::bigint]) RETURNING id`, carol, alice, second, first)
		exec(`INSERT INTO reports (account_id, target_account_id, status_ids) VALUES ($1, $2, ARRAY[$3::bigint]) RETURNING id`, carol, alice, second)
		exec(`INSERT INTO reports (account_id, target_account_id, status_ids, action_taken_at) VALUES ($1, $2, ARRAY[$3::bigint], now()) RETURNING id`, carol, alice, third)
		exec(`INSERT INTO reports (account_id, target_account_id, status_ids) VALUES ($1, $2, ARRAY[$3::bigint]) RETURNING id`, alice, carol, carols)
		ids, err := q.ListUnresolvedReportedStatusIDs(ctx, alice)
		if err != nil {
			t.Fatalf("ListUnresolvedReportedStatusIDs: %v", err)
		}
		if want := fmt.Sprint([]int64{first, second}); fmt.Sprint(ids) != want {
			t.Fatalf("ids = %v, want %s: each once, resolved reports and other targets left out", ids, want)
		}
	})

	t.Run("ListAccountStatusesForDeletion", func(t *testing.T) {
		for name, keep := range map[string][]int64{"nil keep list": nil, "empty keep list": {}} {
			statuses, err := q.ListAccountStatusesForDeletion(ctx, ListAccountStatusesForDeletionParams{AccountID: alice, KeepIds: keep, Limit: 10})
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if len(statuses) != 3 {
				t.Fatalf("%s: %d statuses, want all three of alice's", name, len(statuses))
			}
		}
		statuses, err := q.ListAccountStatusesForDeletion(ctx, ListAccountStatusesForDeletionParams{AccountID: alice, AfterID: first, KeepIds: []int64{second}, Limit: 10})
		if err != nil {
			t.Fatalf("ListAccountStatusesForDeletion: %v", err)
		}
		if len(statuses) != 1 || statuses[0].ID != third {
			t.Fatalf("statuses = %v, want only %d", statusIDsOf(statuses), third)
		}
	})

	t.Run("DeleteAccountFavouritesBatch", func(t *testing.T) {
		exec(`INSERT INTO favourites (account_id, status_id) VALUES ($1, $2) RETURNING id`, alice, carols)
		exec(`INSERT INTO favourites (account_id, status_id) VALUES ($1, $2) RETURNING id`, alice, first)
		exec(`INSERT INTO favourites (account_id, status_id) VALUES ($1, $2) RETURNING id`, carol, carols)
		if _, err := conn.Exec(ctx, `UPDATE status_stats SET favourites_count = 2 WHERE status_id = $1`, carols); err != nil {
			t.Fatal(err)
		}
		deleted, err := q.DeleteAccountFavouritesBatch(ctx, DeleteAccountFavouritesBatchParams{AccountID: alice, Limit: 1})
		if err != nil || deleted != 1 {
			t.Fatalf("first batch: deleted %d, err %v; want 1", deleted, err)
		}
		deleted, err = q.DeleteAccountFavouritesBatch(ctx, DeleteAccountFavouritesBatchParams{AccountID: alice, Limit: 10})
		if err != nil || deleted != 1 {
			t.Fatalf("second batch: deleted %d, err %v; want 1", deleted, err)
		}
		if n := exec(`SELECT favourites_count FROM status_stats WHERE status_id = $1`, carols); n != 1 {
			t.Fatalf("carol's status has %d favourites, want carol's own", n)
		}
		if n := exec(`SELECT count(*) FROM favourites WHERE account_id = $1`, carol); n != 1 {
			t.Fatal("carol's favourite is gone")
		}
	})

	t.Run("DeleteAccountPollsBatch", func(t *testing.T) {
		kept := exec(`INSERT INTO polls (account_id, status_id, expires_at) VALUES ($1, $2, now()) RETURNING id`, alice, second)
		exec(`INSERT INTO polls (account_id, status_id, expires_at) VALUES ($1, $2, now()) RETURNING id`, alice, third)
		exec(`INSERT INTO polls (account_id, expires_at) VALUES ($1, now()) RETURNING id`, alice)
		carolPoll := exec(`INSERT INTO polls (account_id, status_id, expires_at) VALUES ($1, $2, now()) RETURNING id`, carol, carols)
		deleted, err := q.DeleteAccountPollsBatch(ctx, DeleteAccountPollsBatchParams{AccountID: alice, KeepStatusIds: []int64{second}, Limit: 10})
		if err != nil || deleted != 2 {
			t.Fatalf("deleted %d, err %v; want alice's two unreported polls", deleted, err)
		}
		if n := exec(`SELECT count(*) FROM polls WHERE id IN ($1, $2)`, kept, carolPoll); n != 2 {
			t.Fatal("the reported status's poll or carol's poll is gone")
		}
	})

	t.Run("DeleteAccountFollowsBatch", func(t *testing.T) {
		dave := exec(`INSERT INTO accounts (username, uri) VALUES ('dave', 'https://local.test/users/dave') RETURNING id`)
		exec(`INSERT INTO follows (account_id, target_account_id) VALUES ($1, $2) RETURNING id`, alice, carol)
		exec(`INSERT INTO follows (account_id, target_account_id) VALUES ($1, $2) RETURNING id`, carol, alice)
		exec(`INSERT INTO follows (account_id, target_account_id) VALUES ($1, $2) RETURNING id`, carol, dave)
		deleted, err := q.DeleteAccountFollowsBatch(ctx, DeleteAccountFollowsBatchParams{AccountID: alice, Limit: 1})
		if err != nil || deleted != 1 {
			t.Fatalf("first batch: deleted %d, err %v; want the limit, 1", deleted, err)
		}
		deleted, err = q.DeleteAccountFollowsBatch(ctx, DeleteAccountFollowsBatchParams{AccountID: alice, Limit: 10})
		if err != nil || deleted != 1 {
			t.Fatalf("second batch: deleted %d, err %v; want the other direction, 1", deleted, err)
		}
		if n := exec(`SELECT count(*) FROM follows WHERE account_id = $1 AND target_account_id = $2`, carol, dave); n != 1 {
			t.Fatal("carol's follow of dave is gone")
		}
	})

	t.Run("PurgeAccountProfile", func(t *testing.T) {
		exec(`INSERT INTO account_stats (account_id, statuses_count, followers_count, following_count) VALUES ($1, 3, 2, 1) RETURNING id`, alice)
		exec(`INSERT INTO account_stats (account_id, statuses_count) VALUES ($1, 5) RETURNING id`, carol)
		if _, err := conn.Exec(ctx, `UPDATE accounts SET display_name = 'Alice', note = 'bio', fields = '[{"name":"a","value":"b"}]', also_known_as = ARRAY['https://x.example/users/a'], memorial = true, avatar_file_name = 'a.png' WHERE id = $1`, alice); err != nil {
			t.Fatal(err)
		}
		if err := q.PurgeAccountProfile(ctx, alice); err != nil {
			t.Fatalf("PurgeAccountProfile: %v", err)
		}
		account, err := q.GetAccountByIDAllowSuspended(ctx, alice)
		if err != nil {
			t.Fatalf("GetAccountByIDAllowSuspended: %v", err)
		}
		if account.DisplayName != "" || account.Note != "" || string(account.Fields) != "[]" || len(account.AlsoKnownAs) != 0 ||
			account.Memorial || account.AvatarFileName.String != "" || !account.SuspendedAt.Valid || account.SuspensionOrigin.Int32 != 0 {
			t.Fatalf("account = %+v, want an empty profile, suspended by this server", account)
		}
		if account.Uri != "https://local.test/users/alice" || account.Username != "alice" {
			t.Fatalf("account lost its identity: %q %q", account.Username, account.Uri)
		}
		if n := exec(`SELECT statuses_count + followers_count + following_count FROM account_stats WHERE account_id = $1`, alice); n != 0 {
			t.Fatalf("alice's counts add up to %d", n)
		}
		if n := exec(`SELECT statuses_count FROM account_stats WHERE account_id = $1`, carol); n != 5 {
			t.Fatal("carol's counts changed")
		}
	})

	t.Run("DisableAccountDeletionUser and DeleteUserByAccountID", func(t *testing.T) {
		aliceUser := exec(`INSERT INTO users (email, account_id) VALUES ('alice@local.test', $1) RETURNING id`, alice)
		carolUser := exec(`INSERT INTO users (email, account_id) VALUES ('carol@local.test', $1) RETURNING id`, carol)
		exec(`INSERT INTO invites (user_id, code, uses) VALUES ($1, 'unused', 0) RETURNING id`, aliceUser)
		exec(`INSERT INTO invites (user_id, code, uses) VALUES ($1, 'used', 1) RETURNING id`, aliceUser)
		exec(`INSERT INTO oauth_access_tokens (token, resource_owner_id) VALUES ('a', $1) RETURNING id`, aliceUser)
		exec(`INSERT INTO oauth_access_tokens (token, resource_owner_id) VALUES ('c', $1) RETURNING id`, carolUser)
		exec(`INSERT INTO session_activations (user_id, session_id) VALUES ($1, 's') RETURNING id`, aliceUser)

		if err := q.DisableAccountDeletionUser(ctx, alice); err != nil {
			t.Fatalf("DisableAccountDeletionUser: %v", err)
		}
		if n := exec(`SELECT count(*) FROM users WHERE id = $1 AND disabled`, aliceUser); n != 1 {
			t.Fatal("alice's user isn't disabled")
		}
		if n := exec(`SELECT count(*) FROM invites WHERE user_id = $1`, aliceUser); n != 1 {
			t.Fatalf("alice has %d invites, want the used one", n)
		}
		if n := exec(`SELECT count(*) FROM oauth_access_tokens WHERE revoked_at IS NULL`); n != 1 {
			t.Fatalf("%d usable tokens, want carol's", n)
		}
		if n := exec(`SELECT count(*) FROM session_activations WHERE user_id = $1`, aliceUser); n != 0 {
			t.Fatal("alice still has a session")
		}

		if err := q.DeleteUserByAccountID(ctx, alice); err != nil {
			t.Fatalf("DeleteUserByAccountID: %v", err)
		}
		if n := exec(`SELECT count(*) FROM users`); n != 1 {
			t.Fatalf("%d users, want carol's", n)
		}
		if n := exec(`SELECT count(*) FROM accounts WHERE id = $1`, alice); n != 1 {
			t.Fatal("deleting the user deleted the account")
		}
	})
}
