package v1

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
)

type statusRelationshipStub struct {
	favouritedIDs []int64
	bookmarkedIDs []int64
	rebloggedIDs  []pgtype.Int8
	pinnedIDs     []int64

	favouritedParams []db.ListFavouritedStatusIDsParams
	bookmarkedParams []db.ListBookmarkedStatusIDsParams
	rebloggedParams  []db.ListRebloggedStatusIDsParams
	pinnedParams     []db.ListPinnedStatusIDsParams
}

func (s *statusRelationshipStub) ListFavouritedStatusIDs(ctx context.Context, arg db.ListFavouritedStatusIDsParams) ([]int64, error) {
	s.favouritedParams = append(s.favouritedParams, arg)
	return s.favouritedIDs, nil
}

func (s *statusRelationshipStub) ListBookmarkedStatusIDs(ctx context.Context, arg db.ListBookmarkedStatusIDsParams) ([]int64, error) {
	s.bookmarkedParams = append(s.bookmarkedParams, arg)
	return s.bookmarkedIDs, nil
}

func (s *statusRelationshipStub) ListRebloggedStatusIDs(ctx context.Context, arg db.ListRebloggedStatusIDsParams) ([]pgtype.Int8, error) {
	s.rebloggedParams = append(s.rebloggedParams, arg)
	return s.rebloggedIDs, nil
}

func (s *statusRelationshipStub) ListPinnedStatusIDs(ctx context.Context, arg db.ListPinnedStatusIDsParams) ([]int64, error) {
	s.pinnedParams = append(s.pinnedParams, arg)
	return s.pinnedIDs, nil
}

func TestBuildStatusRelationships(t *testing.T) {
	statuses := []db.Status{
		{
			ID:         10,
			AccountID:  1,
			Visibility: 0,
		},
		{
			ID:         20,
			AccountID:  2,
			Visibility: 1,
			ReblogOfID: pgtype.Int8{Int64: 99, Valid: true},
		},
		{
			ID:         30,
			AccountID:  1,
			Visibility: 3,
		},
	}

	queries := &statusRelationshipStub{
		favouritedIDs: []int64{10, 99},
		bookmarkedIDs: []int64{20},
		rebloggedIDs: []pgtype.Int8{
			{Int64: 10, Valid: true},
			{Valid: false},
		},
		pinnedIDs: []int64{10},
	}

	relationships := buildStatusRelationships(context.Background(), queries, 1, statuses)

	if !relationships.favourited[10] || !relationships.favourited[99] {
		t.Fatalf("favourited map = %#v, want ids 10 and 99", relationships.favourited)
	}
	if !relationships.bookmarked[20] {
		t.Fatalf("bookmarked map = %#v, want id 20", relationships.bookmarked)
	}
	if !relationships.reblogged[10] || relationships.reblogged[0] {
		t.Fatalf("reblogged map = %#v, want only id 10", relationships.reblogged)
	}
	if !relationships.pinned[10] || relationships.pinned[30] {
		t.Fatalf("pinned map = %#v, want only id 10", relationships.pinned)
	}

	if len(queries.favouritedParams) != 1 {
		t.Fatalf("favourited params calls = %d, want 1", len(queries.favouritedParams))
	}
	if queries.favouritedParams[0].AccountID != 1 {
		t.Fatalf("favourited account id = %d, want 1", queries.favouritedParams[0].AccountID)
	}
	assertInt64Set(t, queries.favouritedParams[0].StatusIds, []int64{10, 20, 30, 99})

	if len(queries.bookmarkedParams) != 1 {
		t.Fatalf("bookmarked params calls = %d, want 1", len(queries.bookmarkedParams))
	}
	if queries.bookmarkedParams[0].AccountID != 1 {
		t.Fatalf("bookmarked account id = %d, want 1", queries.bookmarkedParams[0].AccountID)
	}
	assertInt64Set(t, queries.bookmarkedParams[0].StatusIds, []int64{10, 20, 30, 99})

	if len(queries.rebloggedParams) != 1 {
		t.Fatalf("reblogged params calls = %d, want 1", len(queries.rebloggedParams))
	}
	if queries.rebloggedParams[0].AccountID != 1 {
		t.Fatalf("reblogged account id = %d, want 1", queries.rebloggedParams[0].AccountID)
	}
	assertInt64Set(t, queries.rebloggedParams[0].StatusIds, []int64{10, 20, 30, 99})

	if len(queries.pinnedParams) != 1 {
		t.Fatalf("pinned params calls = %d, want 1", len(queries.pinnedParams))
	}
	if queries.pinnedParams[0].AccountID != 1 {
		t.Fatalf("pinned account id = %d, want 1", queries.pinnedParams[0].AccountID)
	}
	assertInt64Set(t, queries.pinnedParams[0].StatusIds, []int64{10})
}

func assertInt64Set(t *testing.T, got []int64, want []int64) {
	t.Helper()

	gotSet := make(map[int64]bool, len(got))
	for _, id := range got {
		gotSet[id] = true
	}

	if len(gotSet) != len(want) {
		t.Fatalf("status ids length = %d, want %d (got %v)", len(gotSet), len(want), got)
	}

	for _, id := range want {
		if !gotSet[id] {
			t.Fatalf("status ids = %v, missing %d", got, id)
		}
	}
}
