// Package secrets keeps the values a scenario read as secrets (FR-CLI-17)
// out of everything VegaLoad writes or prints. A value is registered once,
// when the run starts. Output that could carry text a script produced (a
// check name, a console line, an error message, an audit record) goes
// through Redact first.
//
// Redact replaces every occurrence of a registered value with a marker.
// It cannot find a secret the script has changed, for example one it has
// base64-encoded or split in two. It is a safety net for the common
// mistake of printing or logging a value, not a guarantee against a script
// that tries to hide one.
package secrets

import (
	"sort"
	"strings"
	"sync"
)

// Marker replaces a secret in redacted text.
const Marker = "[redacted]"

var (
	mu     sync.RWMutex
	values []string // longest first, so a secret that contains another is replaced whole
)

// Register adds v to the secrets Redact removes. An empty value is
// ignored, since it would match everywhere.
func Register(v string) {
	if v == "" {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	for _, have := range values {
		if have == v {
			return
		}
	}
	values = append(values, v)
	sort.SliceStable(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
}

// Redact returns s with every registered secret replaced by Marker.
func Redact(s string) string {
	mu.RLock()
	defer mu.RUnlock()
	for _, v := range values {
		if strings.Contains(s, v) {
			s = strings.ReplaceAll(s, v, Marker)
		}
	}
	return s
}

// Reset forgets every registered secret. It is for tests.
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	values = nil
}
