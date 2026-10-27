// Package env reads a Lambda's configuration from its environment.
package env

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Loader reads environment variables and remembers which required ones were missing, so a
// misconfigured function fails at startup naming all of them at once rather than one per deploy.
type Loader struct {
	missing map[string]struct{}
}

func (l *Loader) note(name string) {
	if l.missing == nil {
		l.missing = map[string]struct{}{}
	}
	l.missing[name] = struct{}{}
}

// Require returns the variable's value, or notes it as missing and returns "".
func (l *Loader) Require(name string) string {
	value := os.Getenv(name)
	if value == "" {
		l.note(name)
	}
	return value
}

// Optional returns the variable's value, which may be "".
func (l *Loader) Optional(name string) string {
	return os.Getenv(name)
}

// Int returns the variable parsed as an integer, or fallback when it is unset or unparseable.
func (l *Loader) Int(name string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil {
		return fallback
	}
	return value
}

// Err reports the variables that Require found unset.
func (l *Loader) Err() error {
	if len(l.missing) == 0 {
		return nil
	}
	names := make([]string, 0, len(l.missing))
	for name := range l.missing {
		names = append(names, name)
	}
	sort.Strings(names)
	return fmt.Errorf("unset environment variables: %s", strings.Join(names, ", "))
}
