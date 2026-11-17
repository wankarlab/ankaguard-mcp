package launchapi

import (
	"fmt"
	"strings"
	"time"
)

const (
	defaultLimit = 50
	maxLimit     = 200
)

func clampLimit(n int) int {
	if n <= 0 {
		return defaultLimit
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// where builds a WHERE clause from conditions with numbered placeholders.
type where struct {
	conds []string
	args  []any
}

func (w *where) add(cond string, arg any) {
	w.args = append(w.args, arg)
	w.conds = append(w.conds, strings.ReplaceAll(cond, "?", fmt.Sprintf("$%d", len(w.args))))
}

func (w *where) clause() string {
	if len(w.conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(w.conds, " AND ")
}
