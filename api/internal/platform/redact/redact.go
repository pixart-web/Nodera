// Package redact removes credentials from text before it is logged, stored or
// returned to a client. It is a defence-in-depth measure: secrets should never
// reach a log line in the first place.
package redact

import "regexp"

var redactions = []struct {
	re  *regexp.Regexp
	rep string
}{
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`), "[REDACTED PRIVATE KEY]"},
	{regexp.MustCompile(`(?i)(authorization:\s*(?:bearer|basic)\s+)[^\s"']+`), "${1}[REDACTED]"},
	{regexp.MustCompile(`(?i)\b(ndr_[a-z]{2,6}_)[A-Za-z0-9_-]{8,}`), "${1}[REDACTED]"},
	{regexp.MustCompile(`(?i)("?(?:password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key|client[_-]?secret)"?\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;&]+)`), "${1}[REDACTED]"},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), "[REDACTED AWS KEY]"},
	{regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}\b`), "[REDACTED GITHUB TOKEN]"},
	{regexp.MustCompile(`(?i)(://[^:/\s@]+:)[^@\s]+(@)`), "${1}[REDACTED]${2}"},
}

// String redacts known credential shapes in s.
func String(s string) string {
	for _, r := range redactions {
		s = r.re.ReplaceAllString(s, r.rep)
	}
	return s
}
