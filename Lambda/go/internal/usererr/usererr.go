// Package usererr marks the errors whose text is meant for whoever made the request.
//
// Most failures belong in the log. A few are the answer to the request itself - a name already
// taken, a mod with no release for this game version - and the control panel shows those as they
// are rather than replacing them with "please try again later".
package usererr

import (
	"errors"
	"fmt"
)

type userError struct {
	message string
}

func (e *userError) Error() string { return e.message }

// New returns an error whose message is meant for the caller.
func New(message string) error {
	return &userError{message: message}
}

// Newf returns a formatted error whose message is meant for the caller.
func Newf(format string, args ...any) error {
	return &userError{message: fmt.Sprintf(format, args...)}
}

// Message returns the text to show the caller, and whether this error carries one.
func Message(err error) (string, bool) {
	var user *userError
	if errors.As(err, &user) {
		return user.message, true
	}
	return "", false
}
