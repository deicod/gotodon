package admin

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	v1 "github.com/deicod/gotodon/internal/api/v1"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

// AdminAccount is an account as Mastodon's REST::Admin::AccountSerializer
// renders it, for the v1 and v2 admin account endpoints. The user fields
// are null for an account without a user, such as a remote account or the
// instance actor, as Account's nil-safe user_* delegates give them.
type AdminAccount struct {
	ID                     string     `json:"id"`
	Username               string     `json:"username"`
	Domain                 *string    `json:"domain"`
	CreatedAt              time.Time  `json:"created_at"`
	Email                  *string    `json:"email"`
	IP                     *string    `json:"ip"`
	Confirmed              *bool      `json:"confirmed"`
	Suspended              bool       `json:"suspended"`
	Silenced               bool       `json:"silenced"`
	Sensitized             bool       `json:"sensitized"`
	Disabled               *bool      `json:"disabled"`
	Approved               *bool      `json:"approved"`
	Locale                 *string    `json:"locale"`
	InviteRequest          *string    `json:"invite_request"`
	CreatedByApplicationID *string    `json:"created_by_application_id,omitempty"`
	InvitedByAccountID     *string    `json:"invited_by_account_id,omitempty"`
	Account                v1.Account `json:"account"`
	// Role is the user's role, or the everyone role for a user without
	// one, as REST::Admin::AccountSerializer#role gives it. It is null for
	// an account without a user.
	Role *v1.AccountRole `json:"role"`
	// Ips are the user's IPs, newest first. They are null for an account
	// without a user.
	Ips []AdminIP `json:"ips"`
}

// AdminIP is an IP that a user signed up or signed in from, as Mastodon's
// REST::Admin::IpSerializer renders it.
type AdminIP struct {
	IP     string    `json:"ip"`
	UsedAt time.Time `json:"used_at"`
}

// AccountEntityStore is the part of *db.Queries that SerializeAccounts
// reads. Tests use stubs.
type AccountEntityStore interface {
	GetAccountsByIDs(ctx context.Context, ids []int64) ([]db.Account, error)
	AdminListUserIPs(ctx context.Context, userIds []int64) ([]db.AdminListUserIPsRow, error)
}

// SerializeAccounts renders rows as REST::Admin::AccountSerializer does, in
// the same order. It loads each row's account for the nested account entity
// and the IPs of every user. It fails if a lookup fails or a row's account
// is gone.
func SerializeAccounts(ctx context.Context, store AccountEntityStore, cfg *config.Config, rows []db.AdminListAccountsRow) ([]AdminAccount, error) {
	results := make([]AdminAccount, 0, len(rows))
	if len(rows) == 0 {
		return results, nil
	}

	accountIDs := make([]int64, 0, len(rows))
	userIDs := make([]int64, 0, len(rows))
	for _, row := range rows {
		accountIDs = append(accountIDs, row.ID)
		if row.UserID.Valid {
			userIDs = append(userIDs, row.UserID.Int64)
		}
	}

	accounts, err := store.GetAccountsByIDs(ctx, accountIDs)
	if err != nil {
		return nil, fmt.Errorf("load accounts: %w", err)
	}
	accountByID := make(map[int64]db.Account, len(accounts))
	for _, acc := range accounts {
		accountByID[acc.ID] = acc
	}

	ipsByUserID, err := userIPs(ctx, store, userIDs)
	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		acc, ok := accountByID[row.ID]
		if !ok {
			return nil, fmt.Errorf("account %d not found", row.ID)
		}
		results = append(results, serializeAccount(cfg, row, acc, ipsByUserID))
	}
	return results, nil
}

// userIPs loads the IPs of the users userIDs, newest first, as User#ips
// gives them.
func userIPs(ctx context.Context, store AccountEntityStore, userIDs []int64) (map[int64][]AdminIP, error) {
	ipsByUserID := map[int64][]AdminIP{}
	if len(userIDs) == 0 {
		return ipsByUserID, nil
	}
	rows, err := store.AdminListUserIPs(ctx, userIDs)
	if err != nil {
		return nil, fmt.Errorf("load user IPs: %w", err)
	}
	for _, row := range rows {
		if row.Ip == nil || !row.UsedAt.Valid {
			continue
		}
		ipsByUserID[row.UserID] = append(ipsByUserID[row.UserID], AdminIP{
			IP:     row.Ip.String(),
			UsedAt: row.UsedAt.Time,
		})
	}
	for _, ips := range ipsByUserID {
		sort.SliceStable(ips, func(i, j int) bool {
			return ips[i].UsedAt.After(ips[j].UsedAt)
		})
	}
	return ipsByUserID, nil
}

func serializeAccount(cfg *config.Config, row db.AdminListAccountsRow, acc db.Account, ipsByUserID map[int64][]AdminIP) AdminAccount {
	apiAccount := v1.SerializeAccount(cfg, acc)
	if row.FollowersCount.Valid {
		apiAccount.FollowersCount = row.FollowersCount.Int64
	}
	if row.FollowingCount.Valid {
		apiAccount.FollowingCount = row.FollowingCount.Int64
	}
	if row.StatusesCount.Valid {
		apiAccount.StatusesCount = row.StatusesCount.Int64
	}
	if row.LastStatusAt.Valid {
		lastStatus := row.LastStatusAt.Time.Format("2006-01-02")
		apiAccount.LastStatusAt = &lastStatus
	}

	result := AdminAccount{
		ID:         strconv.FormatInt(row.ID, 10),
		Username:   row.Username,
		CreatedAt:  row.CreatedAt.Time,
		Suspended:  row.SuspendedAt.Valid,
		Silenced:   row.SilencedAt.Valid,
		Sensitized: row.SensitizedAt.Valid,
		Account:    apiAccount,
	}
	if row.Domain.Valid {
		domain := row.Domain.String
		result.Domain = &domain
	}

	// Without a user, every user field stays null and the conditional
	// created_by_application_id and invited_by_account_id are left out.
	if !row.UserID.Valid {
		return result
	}

	if row.Email.Valid {
		email := row.Email.String
		result.Email = &email
	}
	if row.Locale.Valid {
		locale := row.Locale.String
		result.Locale = &locale
	}
	if row.InviteRequest.Valid {
		inviteRequest := row.InviteRequest.String
		result.InviteRequest = &inviteRequest
	}
	if row.CreatedByApplicationID.Valid {
		createdByApp := strconv.FormatInt(row.CreatedByApplicationID.Int64, 10)
		result.CreatedByApplicationID = &createdByApp
	}
	if row.InvitedByAccountID.Valid {
		invitedBy := strconv.FormatInt(row.InvitedByAccountID.Int64, 10)
		result.InvitedByAccountID = &invitedBy
	}

	confirmed := row.ConfirmedAt.Valid
	approved := row.Approved.Bool
	disabled := row.Disabled.Bool
	result.Confirmed = &confirmed
	result.Approved = &approved
	result.Disabled = &disabled

	role := v1.SerializeUserRole(row.RoleID, row.RoleName, row.RoleColor, row.RolePermissions, row.RoleHighlighted)
	result.Role = &role

	ips := ipsByUserID[row.UserID.Int64]
	if ips == nil {
		ips = []AdminIP{}
	}
	result.Ips = ips
	if len(ips) > 0 {
		ip := ips[0].IP
		result.IP = &ip
	}
	return result
}
