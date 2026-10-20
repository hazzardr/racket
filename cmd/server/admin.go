package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"racket/internal/db"
)

// usage lists the admin commands. Each runs against $RACKET_DB, alongside a
// running server or not.
const usage = `usage:
  racket                     start the server
  racket remove-user WHO     remove a member, by username or Discord ID`

// cmdRemoveUser removes a member.
const cmdRemoveUser = "remove-user"

// errUsage reports a command line racket doesn't understand.
var errUsage = errors.New(usage)

// runCommand runs the admin command in args.
func runCommand(ctx context.Context, args []string) error {
	if len(args) != 2 || args[0] != cmdRemoveUser {
		return errUsage
	}
	sqlDB, err := openDB(getenv("RACKET_DB", "racket.db"))
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	u, err := removeUser(ctx, sqlDB, args[1])
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "removed member; they can sign in again while they're in the Discord server",
		"username", u.Username, "discord_id", u.DiscordID)
	return nil
}

// removeUser removes the member who, by username or Discord ID, as if they
// had left: they're signed out everywhere, and their machines and spares
// drop out of the app. Parts they lent stay in friends' machines.
func removeUser(ctx context.Context, sqlDB *sql.DB, who string) (db.User, error) {
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return db.User{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := db.New(tx)

	users, err := q.FindCurrentUsers(ctx, who)
	if err != nil {
		return db.User{}, fmt.Errorf("find %q: %w", who, err)
	}
	switch len(users) {
	case 0:
		return db.User{}, fmt.Errorf("no current member has the username or Discord ID %q", who)
	case 1:
	default:
		ids := make([]string, len(users))
		for i, u := range users {
			ids[i] = u.DiscordID
		}
		return db.User{}, fmt.Errorf("%d members are named %q; pass one's Discord ID instead: %s",
			len(users), who, strings.Join(ids, ", "))
	}

	u := users[0]
	if _, err = q.SoftDeleteUser(ctx, u.ID); err != nil {
		return db.User{}, fmt.Errorf("remove %s: %w", u.Username, err)
	}
	if err = q.DeleteSessionsByUser(ctx, u.ID); err != nil {
		return db.User{}, fmt.Errorf("end %s's sessions: %w", u.Username, err)
	}
	if err = tx.Commit(); err != nil {
		return db.User{}, fmt.Errorf("commit: %w", err)
	}
	return u, nil
}
