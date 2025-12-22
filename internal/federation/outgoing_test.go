package federation

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

type recordingEnqueuer struct {
	calls []deliveryCall
}

type deliveryCall struct {
	inbox      string
	activity   *activitypub.Activity
	localActor db.Account
}

func (r *recordingEnqueuer) Enqueue(localActor db.Account, inbox string, activity *activitypub.Activity) error {
	r.calls = append(r.calls, deliveryCall{inbox: inbox, activity: activity, localActor: localActor})
	return nil
}

type outgoingStore struct {
	accounts  map[int64]db.Account
	statuses  map[int64]db.Status
	mentions  map[int64][]db.Mention
	tags      map[int64][]string
	media     map[int64][]db.MediaAttachment
	followers map[int64][]db.Account
}

func (s *outgoingStore) GetAccountByID(ctx context.Context, id int64) (db.Account, error) {
	acc, ok := s.accounts[id]
	if !ok {
		return db.Account{}, errors.New("account not found")
	}
	return acc, nil
}

func (s *outgoingStore) GetAccountsByIDs(ctx context.Context, ids []int64) ([]db.Account, error) {
	result := make([]db.Account, 0, len(ids))
	for _, id := range ids {
		acc, ok := s.accounts[id]
		if ok {
			result = append(result, acc)
		}
	}
	return result, nil
}

func (s *outgoingStore) GetStatusByID(ctx context.Context, id int64) (db.Status, error) {
	status, ok := s.statuses[id]
	if !ok {
		return db.Status{}, errors.New("status not found")
	}
	return status, nil
}

func (s *outgoingStore) GetMentionsByStatusID(ctx context.Context, statusID int64) ([]db.Mention, error) {
	return s.mentions[statusID], nil
}

func (s *outgoingStore) GetTagsByStatusIDs(ctx context.Context, statusIds []int64) ([]db.GetTagsByStatusIDsRow, error) {
	var rows []db.GetTagsByStatusIDsRow
	for _, statusID := range statusIds {
		for idx, tag := range s.tags[statusID] {
			rows = append(rows, db.GetTagsByStatusIDsRow{
				StatusID: statusID,
				ID:       int64(idx + 1),
				Name:     tag,
			})
		}
	}
	return rows, nil
}

func (s *outgoingStore) ListMediaAttachmentsByStatus(ctx context.Context, statusID pgtype.Int8) ([]db.MediaAttachment, error) {
	if !statusID.Valid {
		return nil, nil
	}
	return s.media[statusID.Int64], nil
}

func (s *outgoingStore) GetFollowers(ctx context.Context, arg db.GetFollowersParams) ([]db.Account, error) {
	followers := s.followers[arg.TargetAccountID]
	if len(followers) == 0 {
		return nil, nil
	}
	start := int(arg.Offset)
	if start >= len(followers) {
		return nil, nil
	}
	limit := int(arg.Limit)
	if limit <= 0 {
		limit = len(followers)
	}
	end := start + limit
	if end > len(followers) {
		end = len(followers)
	}
	return followers[start:end], nil
}

func TestOutgoingService_DeliverStatusCreateQueuesFollowersAndMentions(t *testing.T) {
	local := localAccount(1, "alice")
	remoteFollower := remoteAccount(2, "remote.example", "bob", "https://remote.example/inbox", "https://remote.example/shared")
	remoteMention := remoteAccount(3, "mention.example", "carol", "https://mention.example/inbox", "")
	localFollower := localAccount(4, "dora")

	store := &outgoingStore{
		accounts: map[int64]db.Account{
			local.ID:          local,
			remoteFollower.ID: remoteFollower,
			remoteMention.ID:  remoteMention,
			localFollower.ID:  localFollower,
		},
		statuses: map[int64]db.Status{
			10: {
				ID:         10,
				AccountID:  local.ID,
				Text:       "hello",
				Visibility: outgoingVisibilityPublic,
				Local:      pgtype.Bool{Bool: true, Valid: true},
				CreatedAt:  pgtype.Timestamptz{Time: time.Unix(1700000000, 0), Valid: true},
			},
		},
		mentions: map[int64][]db.Mention{
			10: {{AccountID: remoteMention.ID}},
		},
		followers: map[int64][]db.Account{
			local.ID: {remoteFollower, localFollower},
		},
	}

	enqueuer := &recordingEnqueuer{}
	service := NewOutgoingService(store, "local.test", enqueuer)

	if err := service.DeliverStatusCreate(context.Background(), 10); err != nil {
		t.Fatalf("DeliverStatusCreate error: %v", err)
	}

	gotInboxes := []string{}
	for _, call := range enqueuer.calls {
		gotInboxes = append(gotInboxes, call.inbox)
		if call.activity.Type != activitypub.TypeCreate {
			t.Fatalf("activity type = %q, want Create", call.activity.Type)
		}
		if obj, ok := call.activity.ActivityObject.(activitypub.Object); ok {
			if obj.Type != activitypub.TypeNote {
				t.Fatalf("object type = %q, want Note", obj.Type)
			}
		} else {
			t.Fatalf("activity object type = %T, want Object", call.activity.ActivityObject)
		}
	}

	sort.Strings(gotInboxes)
	wantInboxes := []string{"https://mention.example/inbox", "https://remote.example/shared"}
	sort.Strings(wantInboxes)
	if len(gotInboxes) != len(wantInboxes) {
		t.Fatalf("inboxes = %v, want %v", gotInboxes, wantInboxes)
	}
	for i, inbox := range gotInboxes {
		if inbox != wantInboxes[i] {
			t.Fatalf("inboxes = %v, want %v", gotInboxes, wantInboxes)
		}
	}
}

func TestOutgoingService_DeliverStatusUpdateUsesEditedTimestamp(t *testing.T) {
	local := localAccount(1, "alice")
	remoteFollower := remoteAccount(2, "remote.example", "bob", "https://remote.example/inbox", "https://remote.example/shared")
	editedAt := time.Unix(1700001000, 0)

	store := &outgoingStore{
		accounts: map[int64]db.Account{
			local.ID:          local,
			remoteFollower.ID: remoteFollower,
		},
		statuses: map[int64]db.Status{
			20: {
				ID:         20,
				AccountID:  local.ID,
				Text:       "edited",
				Visibility: outgoingVisibilityPublic,
				Local:      pgtype.Bool{Bool: true, Valid: true},
				CreatedAt:  pgtype.Timestamptz{Time: time.Unix(1700000000, 0), Valid: true},
				EditedAt:   pgtype.Timestamptz{Time: editedAt, Valid: true},
			},
		},
		followers: map[int64][]db.Account{
			local.ID: {remoteFollower},
		},
	}
	enqueuer := &recordingEnqueuer{}
	service := NewOutgoingService(store, "local.test", enqueuer)

	if err := service.DeliverStatusUpdate(context.Background(), 20); err != nil {
		t.Fatalf("DeliverStatusUpdate error: %v", err)
	}
	if len(enqueuer.calls) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(enqueuer.calls))
	}
	call := enqueuer.calls[0]
	if call.activity.Type != activitypub.TypeUpdate {
		t.Fatalf("activity type = %q, want Update", call.activity.Type)
	}
	if call.activity.Published != formatTimestamp(editedAt) {
		t.Fatalf("activity published = %q, want %q", call.activity.Published, formatTimestamp(editedAt))
	}
	if obj, ok := call.activity.ActivityObject.(activitypub.Object); ok {
		if obj.Updated != formatTimestamp(editedAt) {
			t.Fatalf("note updated = %q, want %q", obj.Updated, formatTimestamp(editedAt))
		}
	} else {
		t.Fatalf("activity object type = %T, want Object", call.activity.ActivityObject)
	}
}

func TestOutgoingService_DeliverStatusDeleteDirectTargetsMentions(t *testing.T) {
	local := localAccount(1, "alice")
	remoteMention := remoteAccount(2, "mention.example", "carol", "https://mention.example/inbox", "")
	remoteFollower := remoteAccount(3, "remote.example", "bob", "https://remote.example/inbox", "https://remote.example/shared")

	store := &outgoingStore{
		accounts: map[int64]db.Account{
			local.ID:          local,
			remoteMention.ID:  remoteMention,
			remoteFollower.ID: remoteFollower,
		},
		followers: map[int64][]db.Account{
			local.ID: {remoteFollower},
		},
	}
	enqueuer := &recordingEnqueuer{}
	service := NewOutgoingService(store, "local.test", enqueuer)

	status := db.Status{
		ID:         30,
		AccountID:  local.ID,
		Visibility: outgoingVisibilityDirect,
		Local:      pgtype.Bool{Bool: true, Valid: true},
		CreatedAt:  pgtype.Timestamptz{Time: time.Unix(1700000000, 0), Valid: true},
	}

	if err := service.DeliverStatusDelete(context.Background(), status, []db.Account{remoteMention}); err != nil {
		t.Fatalf("DeliverStatusDelete error: %v", err)
	}

	if len(enqueuer.calls) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(enqueuer.calls))
	}
	if enqueuer.calls[0].inbox != "https://mention.example/inbox" {
		t.Fatalf("inbox = %q, want mention inbox", enqueuer.calls[0].inbox)
	}
}

func TestOutgoingService_DeliverFollowAndUndo(t *testing.T) {
	local := localAccount(1, "alice")
	remote := remoteAccount(2, "remote.example", "bob", "https://remote.example/inbox", "")

	store := &outgoingStore{
		accounts: map[int64]db.Account{
			local.ID:  local,
			remote.ID: remote,
		},
	}
	enqueuer := &recordingEnqueuer{}
	service := NewOutgoingService(store, "local.test", enqueuer)

	if err := service.DeliverFollow(context.Background(), local.ID, remote.ID, 44); err != nil {
		t.Fatalf("DeliverFollow error: %v", err)
	}
	if len(enqueuer.calls) != 1 || enqueuer.calls[0].activity.Type != activitypub.TypeFollow {
		t.Fatalf("follow deliveries = %+v", enqueuer.calls)
	}
	enqueuer.calls = nil

	if err := service.DeliverUndoFollow(context.Background(), local.ID, remote.ID, 44); err != nil {
		t.Fatalf("DeliverUndoFollow error: %v", err)
	}
	if len(enqueuer.calls) != 1 || enqueuer.calls[0].activity.Type != activitypub.TypeUndo {
		t.Fatalf("undo deliveries = %+v", enqueuer.calls)
	}
}

func TestOutgoingService_DeliverFollowResponseAccept(t *testing.T) {
	local := localAccount(1, "alice")
	remote := remoteAccount(2, "remote.example", "bob", "https://remote.example/inbox", "")

	store := &outgoingStore{
		accounts: map[int64]db.Account{
			local.ID:  local,
			remote.ID: remote,
		},
	}
	enqueuer := &recordingEnqueuer{}
	service := NewOutgoingService(store, "local.test", enqueuer)

	request := db.FollowRequest{ID: 9, AccountID: remote.ID, TargetAccountID: local.ID, Uri: pgtype.Text{String: "https://remote.example/follow/1", Valid: true}}
	if err := service.DeliverFollowResponse(context.Background(), request, true); err != nil {
		t.Fatalf("DeliverFollowResponse error: %v", err)
	}
	if len(enqueuer.calls) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(enqueuer.calls))
	}
	if enqueuer.calls[0].activity.Type != activitypub.TypeAccept {
		t.Fatalf("activity type = %q, want Accept", enqueuer.calls[0].activity.Type)
	}
}

func TestOutgoingService_DeliverLikeAndUndo(t *testing.T) {
	local := localAccount(1, "alice")
	remote := remoteAccount(2, "remote.example", "bob", "https://remote.example/inbox", "")

	store := &outgoingStore{
		accounts: map[int64]db.Account{
			local.ID:  local,
			remote.ID: remote,
		},
		statuses: map[int64]db.Status{
			55: {ID: 55, AccountID: remote.ID, Uri: pgtype.Text{String: "https://remote.example/status/55", Valid: true}},
		},
	}
	enqueuer := &recordingEnqueuer{}
	service := NewOutgoingService(store, "local.test", enqueuer)

	fav := db.Favourite{ID: 7, AccountID: local.ID, StatusID: 55}
	if err := service.DeliverLike(context.Background(), fav); err != nil {
		t.Fatalf("DeliverLike error: %v", err)
	}
	if len(enqueuer.calls) != 1 || enqueuer.calls[0].activity.Type != activitypub.TypeLike {
		t.Fatalf("like deliveries = %+v", enqueuer.calls)
	}
	enqueuer.calls = nil

	if err := service.DeliverUndoLike(context.Background(), fav); err != nil {
		t.Fatalf("DeliverUndoLike error: %v", err)
	}
	if len(enqueuer.calls) != 1 || enqueuer.calls[0].activity.Type != activitypub.TypeUndo {
		t.Fatalf("undo deliveries = %+v", enqueuer.calls)
	}
}

func TestOutgoingService_DeliverAnnounceAndUndo(t *testing.T) {
	local := localAccount(1, "alice")
	remote := remoteAccount(2, "remote.example", "bob", "https://remote.example/inbox", "https://remote.example/shared")
	remoteFollower := remoteAccount(3, "follower.example", "carl", "https://follower.example/inbox", "https://follower.example/shared")

	store := &outgoingStore{
		accounts: map[int64]db.Account{
			local.ID:          local,
			remote.ID:         remote,
			remoteFollower.ID: remoteFollower,
		},
		statuses: map[int64]db.Status{
			80: {ID: 80, AccountID: local.ID, ReblogOfID: pgtype.Int8{Int64: 81, Valid: true}, Visibility: outgoingVisibilityPublic, Local: pgtype.Bool{Bool: true, Valid: true}},
			81: {ID: 81, AccountID: remote.ID, Uri: pgtype.Text{String: "https://remote.example/status/81", Valid: true}},
		},
		followers: map[int64][]db.Account{
			local.ID: {remoteFollower},
		},
	}

	enqueuer := &recordingEnqueuer{}
	service := NewOutgoingService(store, "local.test", enqueuer)

	if err := service.DeliverAnnounce(context.Background(), 80); err != nil {
		t.Fatalf("DeliverAnnounce error: %v", err)
	}
	if len(enqueuer.calls) != 2 {
		t.Fatalf("announce deliveries = %d, want 2", len(enqueuer.calls))
	}
	enqueuer.calls = nil

	if err := service.DeliverUndoAnnounce(context.Background(), store.statuses[80]); err != nil {
		t.Fatalf("DeliverUndoAnnounce error: %v", err)
	}
	if len(enqueuer.calls) != 2 {
		t.Fatalf("undo deliveries = %d, want 2", len(enqueuer.calls))
	}
}

func localAccount(id int64, username string) db.Account {
	return db.Account{
		ID:         id,
		Username:   username,
		Uri:        "https://local.test/users/" + username,
		PrivateKey: pgtype.Text{String: "private-key", Valid: true},
	}
}

func remoteAccount(id int64, domain, username, inbox, sharedInbox string) db.Account {
	account := db.Account{
		ID:             id,
		Username:       username,
		Domain:         pgtype.Text{String: domain, Valid: true},
		Uri:            "https://" + domain + "/users/" + username,
		InboxUrl:       inbox,
		SharedInboxUrl: sharedInbox,
	}
	return account
}
