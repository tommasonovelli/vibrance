package auth

import (
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"vibrance/internal/httpx"
)

// The two roles (DESIGN.md §7.1).
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// The stable codes of the refusals of this package (§8.4).
const (
	CodeLoginRequired          = "login_required"
	CodeInvalidCredentials     = "invalid_credentials"
	CodeForbidden              = "forbidden"
	CodeSessionNotFound        = "session_not_found"
	CodeUserNotFound           = "user_not_found"
	CodeUsernameTaken          = "username_taken"
	CodeUsernameInvalid        = "username_invalid"
	CodePasswordInvalid        = "password_invalid"
	CodeCurrentPasswordInvalid = "current_password_invalid"
	CodeLastAdmin              = "last_admin"
	CodeCannotModifySelf       = "cannot_modify_self"
)

// The limits of a password (§7.1): 12 to 1024 bytes.
const (
	MinPasswordBytes = 12
	MaxPasswordBytes = 1024
)

// loginRequired is the answer to every request without a live session: no
// credentials, a token that is not one, a session that expired or was
// revoked, an account that was disabled. They are told apart nowhere.
func loginRequired() *httpx.Error {
	return &httpx.Error{Status: http.StatusUnauthorized, Code: CodeLoginRequired,
		Message: "A session is required: sign in."}
}

// invalidCredentials is the answer to every refused sign-in: a name that no
// account has, a wrong password, a disabled account. One answer for all, so
// that it does not tell whether the account exists (§7.2).
func invalidCredentials() *httpx.Error {
	return &httpx.Error{Status: http.StatusUnauthorized, Code: CodeInvalidCredentials,
		Message: "The user name or the password is wrong."}
}

// CheckUsername refuses, with 422 username_invalid, a name that is not
// ^[a-z0-9][a-z0-9._-]{2,31}$ (§7.1): 3 to 32 characters among the ASCII
// lowercase letters, the digits, the dot, the underscore and the hyphen, the
// first a letter or a digit. The message says the rule, never the value.
func CheckUsername(name string) error {
	ok := len(name) >= 3 && len(name) <= 32 && name[0] != '.' && name[0] != '_' && name[0] != '-'
	for i := 0; ok && i < len(name); i++ {
		c := name[i]
		ok = ('a' <= c && c <= 'z') || ('0' <= c && c <= '9') || c == '.' || c == '_' || c == '-'
	}
	if !ok {
		return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: CodeUsernameInvalid,
			Message: "A user name has 3 to 32 characters among a-z, 0-9, dot, underscore and hyphen, and begins with a letter or a digit."}
	}
	return nil
}

// CheckPassword refuses, with 422 password_invalid, a password that is not
// 12 to 1024 bytes of UTF-8 without control characters (§7.1), such as a
// line break or a tab. The message says the rule, never the value or its
// length.
func CheckPassword(password string) error {
	var rule string
	switch {
	case len(password) < MinPasswordBytes:
		rule = "A password has at least 12 bytes."
	case len(password) > MaxPasswordBytes:
		rule = "A password has at most 1024 bytes."
	case !utf8.ValidString(password):
		rule = "A password is valid UTF-8."
	case strings.ContainsFunc(password, unicode.IsControl):
		rule = "A password has no control characters, such as a line break or a tab."
	default:
		return nil
	}
	return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: CodePasswordInvalid, Message: rule}
}

// FoldUsername is a user name as a sign-in compares it (§7.1): it lowers the
// ASCII letters of the name and nothing else. The names of the accounts are
// ASCII, and the lower case of Unicode would make U+212A, the Kelvin sign,
// the k of an account.
func FoldUsername(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
