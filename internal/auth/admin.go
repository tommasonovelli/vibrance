package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"vibrance/internal/httpx"
	"vibrance/internal/store"
)

// The administration of the accounts (DESIGN.md §7.5). The rules that keep
// the server administrable are checked in the write transaction of the
// change, so that two admins who act at once cannot both pass them: the
// writes run one at a time, and the second sees the first.

func userNotFound() *httpx.Error {
	return &httpx.Error{Status: http.StatusNotFound, Code: CodeUserNotFound, Message: "There is no such user."}
}

func lastAdmin() *httpx.Error {
	return &httpx.Error{Status: http.StatusConflict, Code: CodeLastAdmin,
		Message: "The last enabled admin cannot be deleted, demoted or disabled."}
}

func cannotModifySelf() *httpx.Error {
	return &httpx.Error{Status: http.StatusConflict, Code: CodeCannotModifySelf,
		Message: "An admin cannot delete or disable their own account."}
}

// Me returns the account of the user of the request. An account deleted
// since the request was authenticated has no session any more: 401
// login_required.
func (s *Service) Me(ctx context.Context, p Principal) (User, error) {
	u, err := s.GetUser(ctx, p.UserID)
	if refusalOf(err) == CodeUserNotFound {
		return User{}, loginRequired()
	}
	return u, err
}

// GetUser returns the account id, or 404 user_not_found.
func (s *Service) GetUser(ctx context.Context, id string) (User, error) {
	var row store.User
	err := s.store.Read(ctx, func(q *store.Queries) (err error) {
		row, err = q.GetUser(ctx, id)
		return err
	})
	switch {
	case noRows(ctx, err):
		return User{}, userNotFound()
	case err != nil:
		return User{}, fmt.Errorf("auth: reading an account: %w", err)
	}
	return userOf(row), nil
}

// UpdateUser gives the account id the role and the state the admin of the
// request sent, and returns it as it is then. Disabling an account revokes
// its sessions at once (§7.3). The refusals: 404 user_not_found;
// 409 cannot_modify_self when an admin disables their own account; 409
// last_admin when no enabled admin would be left. An admin may demote
// themselves while another enabled admin is left.
func (s *Service) UpdateUser(ctx context.Context, actor Principal, id, role string, disabled bool) (User, error) {
	if role != RoleAdmin && role != RoleUser {
		return User{}, fmt.Errorf("auth: the unknown role %q", role)
	}
	var updated store.User
	err := s.store.WithWriteTx(ctx, func(q *store.Queries) error {
		target, err := adminTarget(ctx, q, actor, id, disabled, role != RoleAdmin || disabled)
		if err != nil {
			return err
		}
		updated = target
		updated.Role, updated.Disabled = role, flag(disabled)
		if err := q.UpdateUser(ctx, store.UpdateUserParams{ID: id, Role: role, Disabled: updated.Disabled}); err != nil {
			return err
		}
		if disabled {
			_, err = q.DeleteSessionsOfUser(ctx, id)
		}
		return err
	})
	if err != nil {
		return User{}, adminFailure("updating an account", err)
	}
	s.log.Info("account updated", "user_id", id, "role", role, "disabled", disabled, "by", actor.UserID)
	return userOf(updated), nil
}

// DeleteUser deletes the account id with its sessions, favorites and
// playlists (§7.5). The refusals: 404 user_not_found; 409
// cannot_modify_self for the account of the request; 409 last_admin for the
// last enabled admin.
func (s *Service) DeleteUser(ctx context.Context, actor Principal, id string) error {
	err := s.store.WithWriteTx(ctx, func(q *store.Queries) error {
		if _, err := adminTarget(ctx, q, actor, id, true, true); err != nil {
			return err
		}
		return q.DeleteUser(ctx, id)
	})
	if err != nil {
		return adminFailure("deleting an account", err)
	}
	s.log.Info("account deleted", "user_id", id, "by", actor.UserID)
	return nil
}

// adminTarget reads, in the transaction q, the account id that the admin
// actor is about to change, and refuses the change when §7.5 forbids it.
// self says that the change is forbidden on the account of the actor;
// demotes, that the account would no longer be an enabled admin after it.
//
// last_admin is checked before cannot_modify_self: the only admin who
// disables or deletes their own account is told that the server would be
// left without an admin, which is the reason that cannot be worked around.
func adminTarget(ctx context.Context, q *store.Queries, actor Principal, id string, self, demotes bool) (store.User, error) {
	target, err := q.GetUser(ctx, id)
	switch {
	case noRows(ctx, err):
		return store.User{}, userNotFound()
	case err != nil:
		return store.User{}, err
	}
	if demotes && target.Role == RoleAdmin && target.Disabled == 0 {
		admins, err := q.CountEnabledAdmins(ctx)
		if err != nil {
			return store.User{}, err
		}
		if admins <= 1 {
			return store.User{}, lastAdmin()
		}
	}
	if self && target.ID == actor.UserID {
		return store.User{}, cannotModifySelf()
	}
	return target, nil
}

// adminFailure is the error of a change of an account: a refusal as it is,
// anything else with what was being done.
func adminFailure(doing string, err error) error {
	var refusal *httpx.Error
	if errors.As(err, &refusal) {
		return refusal
	}
	return fmt.Errorf("auth: %s: %w", doing, err)
}

// refusalOf is the code of a refusal of the API, or "".
func refusalOf(err error) string {
	var refusal *httpx.Error
	if errors.As(err, &refusal) {
		return refusal.Code
	}
	return ""
}

// flag is a boolean as the database stores it.
func flag(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
