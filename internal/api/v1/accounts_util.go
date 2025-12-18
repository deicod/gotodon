package v1

import (
	"fmt"
	"strconv"
	"time"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

// SerializeAccount converts a db.Account to an API Account
func SerializeAccount(cfg *config.Config, dbAcc db.Account) Account {
	createdAt := time.Now()
	if dbAcc.CreatedAt.Valid {
		createdAt = dbAcc.CreatedAt.Time
	}

	acct := dbAcc.Username
	if dbAcc.Domain.Valid && dbAcc.Domain.String != "" {
		acct = dbAcc.Username + "@" + dbAcc.Domain.String
	}

	url := dbAcc.Uri
	if dbAcc.Url.Valid {
		url = dbAcc.Url.String
	}

	discoverable := dbAcc.Discoverable.Valid && dbAcc.Discoverable.Bool
	bot := dbAcc.ActorType.Valid && dbAcc.ActorType.String == "Service"

	domain := "localhost:3000"
	if cfg != nil {
		domain = cfg.Instance.LocalDomain
	}

	avatar := fmt.Sprintf("https://%s/avatars/original/missing.png", domain)
	header := fmt.Sprintf("https://%s/headers/original/missing.png", domain)

	if dbAcc.AvatarFileName.Valid && dbAcc.AvatarFileName.String != "" {
		avatar = dbAcc.AvatarFileName.String
	}
	if dbAcc.HeaderFileName.Valid && dbAcc.HeaderFileName.String != "" {
		header = dbAcc.HeaderFileName.String
	}

	return Account{
		ID:             strconv.FormatInt(dbAcc.ID, 10),
		Username:       dbAcc.Username,
		Acct:           acct,
		DisplayName:    dbAcc.DisplayName,
		Locked:         dbAcc.Locked,
		Bot:            bot,
		Discoverable:   &discoverable,
		Indexable:      dbAcc.Indexable,
		Group:          false,
		CreatedAt:      createdAt,
		Note:           dbAcc.Note,
		URL:            url,
		URI:            dbAcc.Uri,
		Avatar:         avatar,
		AvatarStatic:   avatar,
		Header:         header,
		HeaderStatic:   header,
		FollowersCount: 0,
		FollowingCount: 0,
		StatusesCount:  0,
		LastStatusAt:   nil,
		Emojis:         []CustomEmoji{},
		Fields:         []Field{},
	}
}
