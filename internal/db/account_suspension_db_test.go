package db

import (
	"context"
	"fmt"
	"sort"
	"testing"
)

// suspensionFixture is the data the suspension query tests read: three
// authors, each with one public status that is tagged, carries a preview
// card, has stats, is trending and is in a viewer's home and list timelines.
type suspensionFixture struct {
	alice, sam, rob, viewer int64
	statusOf                map[int64]int64
	listID, cardID          int64
}

func setupSuspensionFixture(t *testing.T, exec func(sql string, args ...any) int64) suspensionFixture {
	t.Helper()
	remote := "remote.example"
	f := suspensionFixture{statusOf: map[int64]int64{}}
	f.alice = exec(`INSERT INTO accounts (username, uri) VALUES ('alice', 'https://local.test/users/alice') RETURNING id`)
	f.sam = exec(`INSERT INTO accounts (username, uri) VALUES ('sam', 'https://local.test/users/sam') RETURNING id`)
	f.rob = exec(`INSERT INTO accounts (username, domain, uri) VALUES ('rob', $1, 'https://remote.example/users/rob') RETURNING id`, remote)
	f.viewer = exec(`INSERT INTO accounts (username, uri) VALUES ('viewer', 'https://local.test/users/viewer') RETURNING id`)

	tagID := exec(`INSERT INTO tags (name) VALUES ('suspension') RETURNING id`)
	f.cardID = exec(`INSERT INTO preview_cards (url) VALUES ('https://news.example/a') RETURNING id`)
	f.listID = exec(`INSERT INTO lists (account_id, title) VALUES ($1, 'all') RETURNING id`, f.viewer)
	for _, author := range []int64{f.alice, f.sam, f.rob} {
		statusID := exec(`INSERT INTO statuses (account_id, text, visibility, local)
			SELECT $1, 'hello', 0, a.domain IS NULL FROM accounts a WHERE a.id = $1 RETURNING id`, author)
		f.statusOf[author] = statusID
		exec(`INSERT INTO statuses_tags (status_id, tag_id) VALUES ($1, $2) RETURNING status_id`, statusID, tagID)
		exec(`INSERT INTO preview_cards_statuses (preview_card_id, status_id) VALUES ($1, $2) RETURNING status_id`, f.cardID, statusID)
		exec(`INSERT INTO status_stats (status_id) VALUES ($1) RETURNING id`, statusID)
		exec(`INSERT INTO status_trends (status_id, account_id, allowed) VALUES ($1, $2, true) RETURNING id`, statusID, author)
		exec(`INSERT INTO follows (account_id, target_account_id) VALUES ($1, $2) RETURNING id`, f.viewer, author)
		exec(`INSERT INTO list_accounts (list_id, account_id) VALUES ($1, $2) RETURNING id`, f.listID, author)
	}
	return f
}

func statusIDsOf(statuses []Status) []int64 {
	ids := make([]int64, 0, len(statuses))
	for _, status := range statuses {
		ids = append(ids, status.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func TestTimelinesLeaveOutSuspendedAccounts(t *testing.T) {
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
	f := setupSuspensionFixture(t, exec)

	// timelines returns the status IDs each timeline query reads.
	timelines := func() map[string][]int64 {
		t.Helper()
		got := map[string][]int64{}
		read := func(name string, statuses []Status, err error) {
			t.Helper()
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			got[name] = statusIDsOf(statuses)
		}
		s, err := q.GetHomeTimeline(ctx, GetHomeTimelineParams{AccountID: f.viewer, Limit: 20})
		read("home", s, err)
		s, err = q.GetListTimeline(ctx, GetListTimelineParams{ListID: f.listID, Limit: 20})
		read("list", s, err)
		s, err = q.GetPublicTimeline(ctx, GetPublicTimelineParams{Limit: 20})
		read("public", s, err)
		s, err = q.GetPublicTimeline(ctx, GetPublicTimelineParams{Limit: 20, Local: true})
		read("public local", s, err)
		s, err = q.GetHashtagTimeline(ctx, GetHashtagTimelineParams{Tag: "suspension", Limit: 20})
		read("hashtag", s, err)
		s, err = q.GetLinkTimelineStatuses(ctx, GetLinkTimelineStatusesParams{PreviewCardID: f.cardID, Limit: 20})
		read("link", s, err)
		s, err = q.GetLinkTimelineStatusesMinID(ctx, GetLinkTimelineStatusesMinIDParams{PreviewCardID: f.cardID, ID: 0, Limit: 20})
		read("link min_id", s, err)
		trending, err := q.GetTrendingStatuses(ctx, 20)
		if err != nil {
			t.Fatalf("trending: %v", err)
		}
		ids := make([]int64, 0, len(trending))
		for _, row := range trending {
			ids = append(ids, row.ID)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		got["trending"] = ids
		return got
	}
	check := func(stage string, want map[string][]int64) {
		t.Helper()
		got := timelines()
		for name, ids := range want {
			if !equalIDs(got[name], ids) {
				t.Errorf("%s: %s timeline = %v, want %v", stage, name, got[name], ids)
			}
		}
	}

	alice, sam, rob := f.statusOf[f.alice], f.statusOf[f.sam], f.statusOf[f.rob]
	all := []int64{alice, sam, rob}
	local := []int64{alice, sam}
	check("before suspension", map[string][]int64{
		"home": all, "list": all, "public": all, "public local": local,
		"hashtag": all, "link": all, "link min_id": all, "trending": all,
	})

	if err := q.AdminSuspendAccount(ctx, f.sam); err != nil {
		t.Fatalf("suspend sam: %v", err)
	}
	if err := q.AdminSuspendAccount(ctx, f.rob); err != nil {
		t.Fatalf("suspend rob: %v", err)
	}
	only := []int64{alice}
	check("after suspension", map[string][]int64{
		"home": only, "list": only, "public": only, "public local": only,
		"hashtag": only, "link": only, "link min_id": only, "trending": only,
	})

	if err := q.AdminUnsuspendAccount(ctx, f.sam); err != nil {
		t.Fatalf("unsuspend sam: %v", err)
	}
	back := []int64{alice, sam}
	check("after unsuspension", map[string][]int64{
		"home": back, "list": back, "public": back, "public local": back,
		"hashtag": back, "link": back, "link min_id": back, "trending": back,
	})
}

func TestSuspensionFollowQueries(t *testing.T) {
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
	remote := "remote.example"
	rob := exec(`INSERT INTO accounts (username, domain, uri) VALUES ('rob', $1, 'https://remote.example/users/rob') RETURNING id`, remote)
	dave := exec(`INSERT INTO accounts (username, domain, uri) VALUES ('dave', $1, 'https://remote.example/users/dave') RETURNING id`, remote)
	var locals []int64
	for _, name := range []string{"alice", "carol", "frank"} {
		locals = append(locals, exec(`INSERT INTO accounts (username, uri) VALUES ($1, $2) RETURNING id`, name, "https://local.test/users/"+name))
	}

	var robFollows, robRequests []int64
	for _, target := range locals {
		robFollows = append(robFollows, exec(`INSERT INTO follows (account_id, target_account_id, uri) VALUES ($1, $2, $3) RETURNING id`, rob, target, fmt.Sprintf("https://remote.example/f/%d", target)))
		robRequests = append(robRequests, exec(`INSERT INTO follow_requests (account_id, target_account_id) VALUES ($1, $2) RETURNING id`, rob, target))
	}
	// Neither a follow of a remote account nor another account's follow
	// of a local one is listed.
	exec(`INSERT INTO follows (account_id, target_account_id) VALUES ($1, $2) RETURNING id`, rob, dave)
	exec(`INSERT INTO follows (account_id, target_account_id) VALUES ($1, $2) RETURNING id`, dave, locals[0])
	exec(`INSERT INTO follow_requests (account_id, target_account_id) VALUES ($1, $2) RETURNING id`, rob, dave)
	exec(`INSERT INTO follow_requests (account_id, target_account_id) VALUES ($1, $2) RETURNING id`, dave, locals[0])

	follows, err := q.ListFollowsByAccountToLocal(ctx, ListFollowsByAccountToLocalParams{AccountID: rob, Limit: 10})
	if err != nil {
		t.Fatalf("ListFollowsByAccountToLocal: %v", err)
	}
	var ids []int64
	for _, follow := range follows {
		ids = append(ids, follow.ID)
		if follow.AccountID != rob || !follow.Uri.Valid {
			t.Fatalf("follow = %+v", follow)
		}
	}
	if !equalIDs(ids, robFollows) {
		t.Fatalf("follows = %v, want %v", ids, robFollows)
	}
	page, err := q.ListFollowsByAccountToLocal(ctx, ListFollowsByAccountToLocalParams{AccountID: rob, AfterID: robFollows[0], Limit: 1})
	if err != nil {
		t.Fatalf("ListFollowsByAccountToLocal page: %v", err)
	}
	if len(page) != 1 || page[0].ID != robFollows[1] {
		t.Fatalf("page after %d = %+v, want follow %d", robFollows[0], page, robFollows[1])
	}

	requests, err := q.ListFollowRequestsByAccountToLocal(ctx, ListFollowRequestsByAccountToLocalParams{AccountID: rob, Limit: 10})
	if err != nil {
		t.Fatalf("ListFollowRequestsByAccountToLocal: %v", err)
	}
	ids = nil
	for _, request := range requests {
		ids = append(ids, request.ID)
	}
	if !equalIDs(ids, robRequests) {
		t.Fatalf("follow requests = %v, want %v", ids, robRequests)
	}
	page2, err := q.ListFollowRequestsByAccountToLocal(ctx, ListFollowRequestsByAccountToLocalParams{AccountID: rob, AfterID: robRequests[1], Limit: 10})
	if err != nil {
		t.Fatalf("ListFollowRequestsByAccountToLocal page: %v", err)
	}
	if len(page2) != 1 || page2[0].ID != robRequests[2] {
		t.Fatalf("requests after %d = %+v, want request %d", robRequests[1], page2, robRequests[2])
	}
}

func TestDeleteStatusTrendsByAccount(t *testing.T) {
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
	f := setupSuspensionFixture(t, exec)

	if err := q.DeleteStatusTrendsByAccount(ctx, f.sam); err != nil {
		t.Fatalf("DeleteStatusTrendsByAccount: %v", err)
	}
	trending, err := q.ListTrendingStatuses(ctx, ListTrendingStatusesParams{Limit: 10})
	if err != nil {
		t.Fatalf("ListTrendingStatuses: %v", err)
	}
	if got, want := statusIDsOf(trending), []int64{f.statusOf[f.alice], f.statusOf[f.rob]}; !equalIDs(got, want) {
		t.Fatalf("status trends = %v, want %v", got, want)
	}
	// Deleting again changes nothing.
	if err := q.DeleteStatusTrendsByAccount(ctx, f.sam); err != nil {
		t.Fatalf("DeleteStatusTrendsByAccount again: %v", err)
	}
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
