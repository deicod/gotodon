package v1

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

func TestSerializeAccount_ActorTypes(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}

	tests := []struct {
		name      string
		actorType string
		wantBot   bool
		wantGroup bool
	}{
		{name: "service", actorType: "Service", wantBot: true, wantGroup: false},
		{name: "application", actorType: "Application", wantBot: true, wantGroup: false},
		{name: "group", actorType: "Group", wantBot: false, wantGroup: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbAcc := db.Account{
				ID:        1,
				Username:  "alice",
				ActorType: pgtype.Text{String: tt.actorType, Valid: true},
			}

			acc := serializeAccount(cfg, dbAcc, accountSerializeOptions{})

			if acc.Bot != tt.wantBot {
				t.Fatalf("bot = %v, want %v", acc.Bot, tt.wantBot)
			}
			if acc.Group != tt.wantGroup {
				t.Fatalf("group = %v, want %v", acc.Group, tt.wantGroup)
			}
		})
	}
}

func TestSerializeAccount_DiscoverableDefaults(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}

	dbAcc := db.Account{
		ID:       1,
		Username: "alice",
	}

	acc := serializeAccount(cfg, dbAcc, accountSerializeOptions{})

	if acc.Discoverable == nil {
		t.Fatalf("discoverable is nil, want false")
	}
	if *acc.Discoverable {
		t.Fatalf("discoverable = %v, want false", *acc.Discoverable)
	}
}

func TestSerializeAccount_RemoteAcctUsesUnicodeDomain(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}

	dbAcc := db.Account{
		ID:       1,
		Username: "alice",
		Domain:   pgtype.Text{String: "xn--bcher-kva.example", Valid: true},
	}

	acc := serializeAccount(cfg, dbAcc, accountSerializeOptions{})

	want := "alice@b\u00fccher.example"
	if acc.Acct != want {
		t.Fatalf("acct = %q, want %q", acc.Acct, want)
	}
}

func TestSerializeAccount_SuspendedBotDiscoverable(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}

	dbAcc := db.Account{
		ID:           1,
		Username:     "alice",
		ActorType:    pgtype.Text{String: "Service", Valid: true},
		Discoverable: pgtype.Bool{Bool: true, Valid: true},
		SuspendedAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}

	acc := serializeAccount(cfg, dbAcc, accountSerializeOptions{})

	if acc.Bot {
		t.Fatalf("bot = true, want false for suspended account")
	}
	if acc.Discoverable == nil || *acc.Discoverable {
		t.Fatalf("discoverable = %v, want false for suspended account", acc.Discoverable)
	}
}

func TestSerializeAccount_FormatsLocalFieldsWithRelMe(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "local.test", WebDomain: "web.test"}}

	dbAcc := db.Account{
		ID:       1,
		Username: "alice",
		Fields:   []byte(`[{"name":"Site","value":"https://example.com/profile"}]`),
	}

	acc := serializeAccount(cfg, dbAcc, accountSerializeOptions{})

	if len(acc.Fields) != 1 {
		t.Fatalf("fields length = %d, want 1", len(acc.Fields))
	}
	got := acc.Fields[0].Value
	if !strings.Contains(got, `href="https://example.com/profile"`) {
		t.Fatalf("field value = %q, want href to be linkified", got)
	}
	if !strings.Contains(got, `rel="nofollow noopener me"`) {
		t.Fatalf("field value = %q, want rel=\"nofollow noopener me\"", got)
	}
	if strings.Contains(got, "<p>") {
		t.Fatalf("field value = %q, want no paragraph wrapper", got)
	}
}

func TestSerializeAccount_RemoteVerifiedFieldUsesShortenedLink(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "local.test"}}
	verifiedAt := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	dbAcc := db.Account{
		ID:       1,
		Username: "alice",
		Domain:   pgtype.Text{String: "remote.test", Valid: true},
	}

	fields := []Field{
		{
			Name:       "Site",
			Value:      "https://remote.test/@alice",
			VerifiedAt: &verifiedAt,
		},
	}

	acc := serializeAccount(cfg, dbAcc, accountSerializeOptions{Fields: fields})

	want := shortenedLink("https://remote.test/@alice", false)
	if acc.Fields[0].Value != want {
		t.Fatalf("field value = %q, want %q", acc.Fields[0].Value, want)
	}
}

func TestSerializeAccount_RemoteFieldSanitizesHTML(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "local.test"}}

	dbAcc := db.Account{
		ID:       1,
		Username: "alice",
		Domain:   pgtype.Text{String: "remote.test", Valid: true},
	}

	fields := []Field{
		{
			Name:  "Site",
			Value: `<p class="foo mention">hi</p><a href="javascript:alert(1)">bad</a><a href="https://example.com" class="ellipsis">good</a>`,
		},
	}

	acc := serializeAccount(cfg, dbAcc, accountSerializeOptions{Fields: fields})
	got := acc.Fields[0].Value

	if strings.Contains(got, "javascript:") {
		t.Fatalf("expected javascript href to be removed, got %q", got)
	}
	if strings.Contains(got, "foo") {
		t.Fatalf("expected disallowed class to be removed, got %q", got)
	}
	if !strings.Contains(got, `class="mention"`) {
		t.Fatalf("expected mention class to remain, got %q", got)
	}
	if !strings.Contains(got, `href="https://example.com"`) {
		t.Fatalf("expected allowed link to remain, got %q", got)
	}
	if !strings.Contains(got, `rel="nofollow noopener"`) || !strings.Contains(got, `target="_blank"`) {
		t.Fatalf("expected rel/target to be enforced, got %q", got)
	}
}

func TestSerializeAccount_FormatsLocalNote(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "local.test"}}

	dbAcc := db.Account{
		ID:       1,
		Username: "alice",
		Note:     "Hello https://example.com\nLine two",
	}

	acc := serializeAccount(cfg, dbAcc, accountSerializeOptions{})
	got := acc.Note

	if !strings.Contains(got, `href="https://example.com"`) {
		t.Fatalf("note = %q, want linkified url", got)
	}
	if !strings.Contains(got, "<p>") || !strings.Contains(got, "<br />") {
		t.Fatalf("note = %q, want paragraph and line breaks", got)
	}
	if strings.Contains(got, `rel="nofollow noopener me"`) {
		t.Fatalf("note = %q, want rel without me", got)
	}
}

func TestSerializeAccount_RemoteNoteSanitizesHTML(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "local.test"}}

	dbAcc := db.Account{
		ID:       1,
		Username: "alice",
		Domain:   pgtype.Text{String: "remote.test", Valid: true},
		Note:     `<p class="foo mention">hi</p><a href="javascript:alert(1)">bad</a><a href="https://example.com" class="ellipsis">good</a>`,
	}

	acc := serializeAccount(cfg, dbAcc, accountSerializeOptions{})
	got := acc.Note

	if strings.Contains(got, "javascript:") {
		t.Fatalf("expected javascript href to be removed, got %q", got)
	}
	if strings.Contains(got, "foo") {
		t.Fatalf("expected disallowed class to be removed, got %q", got)
	}
	if !strings.Contains(got, `class="mention"`) {
		t.Fatalf("expected mention class to remain, got %q", got)
	}
	if !strings.Contains(got, `href="https://example.com"`) {
		t.Fatalf("expected allowed link to remain, got %q", got)
	}
	if !strings.Contains(got, `rel="nofollow noopener"`) || !strings.Contains(got, `target="_blank"`) {
		t.Fatalf("expected rel/target to be enforced, got %q", got)
	}
}

func TestBuildCredentialAccount_SourceFieldsRemainRaw(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "local.test", WebDomain: "web.test"}}

	dbAcc := db.Account{
		ID:       1,
		Username: "alice",
		Note:     "Hello https://example.com/profile",
		Fields:   []byte(`[{"name":"Site","value":"https://example.com/profile"}]`),
	}

	cred := buildCredentialAccount(context.Background(), nil, cfg, dbAcc)

	if len(cred.Fields) != 1 || len(cred.Source.Fields) != 1 {
		t.Fatalf("fields length = %d/%d, want 1/1", len(cred.Fields), len(cred.Source.Fields))
	}
	if cred.Source.Fields[0].Value != "https://example.com/profile" {
		t.Fatalf("source field value = %q, want raw value", cred.Source.Fields[0].Value)
	}
	if cred.Source.Note != "Hello https://example.com/profile" {
		t.Fatalf("source note = %q, want raw value", cred.Source.Note)
	}
	if !strings.Contains(cred.Fields[0].Value, `rel="nofollow noopener me"`) {
		t.Fatalf("serialized field = %q, want formatted value", cred.Fields[0].Value)
	}
	if !strings.Contains(cred.Note, `href="https://example.com/profile"`) {
		t.Fatalf("serialized note = %q, want formatted value", cred.Note)
	}
}

type accountSerializeStub struct {
	accountsByUsername map[string]db.Account
}

func (s *accountSerializeStub) ListAccountStatsByAccountIDs(ctx context.Context, accountIds []int64) ([]db.AccountStat, error) {
	return nil, nil
}

func (s *accountSerializeStub) ListUserSettingsByAccountIDs(ctx context.Context, accountIds []int64) ([]db.ListUserSettingsByAccountIDsRow, error) {
	return nil, nil
}

func (s *accountSerializeStub) ListUserRolesByAccountIDs(ctx context.Context, accountIds []int64) ([]db.ListUserRolesByAccountIDsRow, error) {
	return nil, nil
}

func (s *accountSerializeStub) ListCustomEmojisByShortcodesAndDomain(ctx context.Context, arg db.ListCustomEmojisByShortcodesAndDomainParams) ([]db.CustomEmoji, error) {
	return nil, nil
}

func (s *accountSerializeStub) GetAccountsByIDs(ctx context.Context, ids []int64) ([]db.Account, error) {
	return nil, nil
}

func (s *accountSerializeStub) GetAccountsByUsernames(ctx context.Context, usernames []string) ([]db.Account, error) {
	accounts := make([]db.Account, 0, len(usernames))
	for _, username := range usernames {
		if acc, ok := s.accountsByUsername[strings.ToLower(username)]; ok {
			accounts = append(accounts, acc)
		}
	}
	return accounts, nil
}

func (s *accountSerializeStub) GetAccountsByUsernamesAndDomains(ctx context.Context, arg db.GetAccountsByUsernamesAndDomainsParams) ([]db.Account, error) {
	return nil, nil
}

type accountCredentialStub struct {
	accountSerializeStub
	user         db.User
	followCount  int64
	userRoleErr  error
}

func (s *accountCredentialStub) GetUserByAccountID(ctx context.Context, accountID int64) (db.User, error) {
	return s.user, nil
}

func (s *accountCredentialStub) CountFollowRequestsForAccount(ctx context.Context, targetAccountID int64) (int64, error) {
	return s.followCount, nil
}

func (s *accountCredentialStub) GetUserRole(ctx context.Context, userID int64) (db.UserRole, error) {
	if s.userRoleErr != nil {
		return db.UserRole{}, s.userRoleErr
	}
	return db.UserRole{}, nil
}

func TestSerializeAccounts_LinkifiesNoteMentions(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "local.test", WebDomain: "web.test"}}

	dbAcc := db.Account{
		ID:       1,
		Username: "alice",
		Note:     "Hi @bob",
	}

	stub := &accountSerializeStub{
		accountsByUsername: map[string]db.Account{
			"bob": {ID: 2, Username: "bob"},
		},
	}

	serialized := SerializeAccounts(context.Background(), stub, cfg, []db.Account{dbAcc}, false)
	acc, ok := serialized[1]
	if !ok {
		t.Fatalf("missing serialized account")
	}
	if !strings.Contains(acc.Note, `href="https://web.test/@bob"`) {
		t.Fatalf("note = %q, want mention link", acc.Note)
	}
	if !strings.HasPrefix(acc.Note, "<p>") {
		t.Fatalf("note = %q, want paragraph wrapper", acc.Note)
	}
}

func TestBuildCredentialAccount_SourceQuotePolicyFallsBackToPublicForInvalidStoredValue(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "local.test", WebDomain: "web.test"}}
	dbAcc := db.Account{
		ID:       1,
		Username: "alice",
	}
	stub := &accountCredentialStub{
		user: db.User{
			ID: 1,
			Settings: pgtype.Text{
				String: `{"default_quote_policy":"bad-value"}`,
				Valid:  true,
			},
		},
	}

	cred := buildCredentialAccount(context.Background(), stub, cfg, dbAcc)

	if cred.Source.QuotePolicy != "public" {
		t.Fatalf("source.quote_policy = %q, want public", cred.Source.QuotePolicy)
	}
}
