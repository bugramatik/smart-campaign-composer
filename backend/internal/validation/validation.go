// Package validation carries field-level validation issues. The same issue
// list is shown to the marketer in the UI and fed back to Claude in the
// repair loop, so messages are written to be actionable by both.
package validation

import (
	"fmt"
	"strings"
)

type Issue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Error is a non-empty list of issues.
type Error struct {
	Issues []Issue `json:"issues"`
}

func (e *Error) Error() string {
	parts := make([]string, len(e.Issues))
	for i, is := range e.Issues {
		parts[i] = is.String()
	}
	return "validation failed: " + strings.Join(parts, "; ")
}

func (i Issue) String() string {
	if i.Path == "" {
		return i.Message
	}
	return i.Path + ": " + i.Message
}

// Collector accumulates issues; Err returns nil when there are none.
type Collector struct {
	issues []Issue
}

func (c *Collector) Add(path, format string, args ...any) {
	c.issues = append(c.issues, Issue{Path: path, Message: fmt.Sprintf(format, args...)})
}

func (c *Collector) Err() error {
	if len(c.issues) == 0 {
		return nil
	}
	return &Error{Issues: c.issues}
}
