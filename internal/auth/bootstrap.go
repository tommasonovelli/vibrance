package auth

import (
	"context"
	"errors"
	"fmt"

	"vibrance/internal/httpx"
	"vibrance/internal/store"
)

// The variables of the first admin (DESIGN.md §7.4, §11.1). They are read
// here and nowhere else, and only when the server has no account.
const (
	envAdminUsername = "VIBRANCE_ADMIN_USERNAME"
	envAdminPassword = "VIBRANCE_ADMIN_PASSWORD"

	defaultAdminUsername = "admin"
)

// The stable codes of a server that has no account and cannot create the
// first admin. The server does not start.
const (
	// CodeAdminPasswordMissing: VIBRANCE_ADMIN_PASSWORD is not set.
	CodeAdminPasswordMissing = "admin_password_missing"
	// CodeAdminPasswordInvalid: it is not a password CheckPassword accepts.
	CodeAdminPasswordInvalid = "admin_password_invalid"
	// CodeAdminUsernameInvalid: VIBRANCE_ADMIN_USERNAME is not a name
	// CheckUsername accepts.
	CodeAdminUsernameInvalid = "admin_username_invalid"
)

// BootstrapError is a refusal of Bootstrap, with its stable code. Its
// message names the variable and the rule, never the value (I5).
type BootstrapError struct {
	Code string
	Msg  string
}

func (e *BootstrapError) Error() string { return e.Msg }

// Bootstrap creates the first admin when the server has no account, from
// VIBRANCE_ADMIN_USERNAME (default "admin") and VIBRANCE_ADMIN_PASSWORD. An
// empty variable is an unset one. With any account in the database the two
// variables are not looked at, valid or not: the password of the admin is
// then the one in the database, changed from the API or with
// `vibrance user reset-password`. A refusal is a *BootstrapError.
func (s *Service) Bootstrap(ctx context.Context, getenv func(string) string) error {
	var users int64
	if err := s.store.Read(ctx, func(q *store.Queries) (err error) {
		users, err = q.CountUsers(ctx)
		return err
	}); err != nil {
		return fmt.Errorf("auth: counting the accounts: %w", err)
	}
	if users > 0 {
		return nil
	}
	username := getenv(envAdminUsername)
	if username == "" {
		username = defaultAdminUsername
	}
	if err := CheckUsername(username); err != nil {
		return &BootstrapError{Code: CodeAdminUsernameInvalid, Msg: envAdminUsername + ": " + ruleOf(err)}
	}
	password := getenv(envAdminPassword)
	if password == "" {
		return &BootstrapError{Code: CodeAdminPasswordMissing, Msg: envAdminPassword +
			" is required: the server has no account yet, and creates the first admin with it"}
	}
	if err := CheckPassword(password); err != nil {
		return &BootstrapError{Code: CodeAdminPasswordInvalid, Msg: envAdminPassword + ": " + ruleOf(err)}
	}
	user, created, err := s.addUser(ctx, username, password, RoleAdmin, true)
	if err != nil {
		return fmt.Errorf("auth: creating the first admin: %w", err)
	}
	if created {
		s.log.Info("first admin created", "username", user.Username, "user_id", user.ID)
	}
	// Not created: another process made an account in the meantime, and the
	// variables no longer count.
	return nil
}

// ruleOf is the sentence of a refusal of CheckUsername or CheckPassword.
func ruleOf(err error) string {
	var e *httpx.Error
	if errors.As(err, &e) {
		return e.Message
	}
	return "not valid"
}
