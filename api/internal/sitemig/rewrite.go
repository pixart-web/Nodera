package sitemig

import (
	"regexp"
	"strconv"
	"strings"
)

// RewriteURLs replaces oldURL with newURL throughout a MySQL/MariaDB dump,
// correctly updating PHP-serialized string lengths (s:N:"...";) — including
// serialized values nested inside serialized strings and JSON-escaped URLs
// (https:\/\/example.com). A naive search/replace corrupts these values and
// breaks WordPress options, widgets and page-builder data.
//
// It operates on SQL string literals only: identifiers, numbers, comments and
// keywords are never touched. It returns the new dump and the number of
// literals that were modified.
func RewriteURLs(sql, oldURL, newURL string) (string, int) {
	oldURL = strings.TrimRight(oldURL, "/")
	newURL = strings.TrimRight(newURL, "/")
	if oldURL == "" || oldURL == newURL {
		return sql, 0
	}
	var out strings.Builder
	out.Grow(len(sql))
	changed := 0
	i := 0
	for i < len(sql) {
		c := sql[i]
		switch {
		case c == '-' && strings.HasPrefix(sql[i:], "-- "):
			j := strings.IndexByte(sql[i:], '\n')
			if j < 0 {
				j = len(sql) - i
			}
			out.WriteString(sql[i : i+j])
			i += j
		case c == '#':
			j := strings.IndexByte(sql[i:], '\n')
			if j < 0 {
				j = len(sql) - i
			}
			out.WriteString(sql[i : i+j])
			i += j
		case c == '/' && strings.HasPrefix(sql[i:], "/*"):
			j := strings.Index(sql[i+2:], "*/")
			if j < 0 {
				out.WriteString(sql[i:])
				i = len(sql)
			} else {
				out.WriteString(sql[i : i+2+j+2])
				i += 2 + j + 2
			}
		case c == '\'':
			end, decoded, ok := readLiteral(sql, i)
			if !ok {
				out.WriteString(sql[i:])
				i = len(sql)
				break
			}
			rewritten := rewriteSerialized(decoded, oldURL, newURL)
			if rewritten == decoded {
				out.WriteString(sql[i:end])
			} else {
				out.WriteByte('\'')
				out.WriteString(encodeLiteral(rewritten))
				out.WriteByte('\'')
				changed++
			}
			i = end
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.String(), changed
}

// readLiteral parses the single-quoted literal starting at sql[start] and
// returns the index just past the closing quote and the decoded content.
func readLiteral(sql string, start int) (end int, decoded string, ok bool) {
	var b strings.Builder
	i := start + 1
	for i < len(sql) {
		c := sql[i]
		switch c {
		case '\\':
			if i+1 >= len(sql) {
				return 0, "", false
			}
			n := sql[i+1]
			switch n {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case '0':
				b.WriteByte(0)
			case 'Z':
				b.WriteByte(26)
			case 'b':
				b.WriteByte('\b')
			default: // \\ \' \" \% \_ and anything else: the character itself
				if n == '%' || n == '_' {
					b.WriteByte('\\')
				}
				b.WriteByte(n)
			}
			i += 2
		case '\'':
			if i+1 < len(sql) && sql[i+1] == '\'' {
				b.WriteByte('\'')
				i += 2
				continue
			}
			return i + 1, b.String(), true
		default:
			b.WriteByte(c)
			i++
		}
	}
	return 0, "", false
}

func encodeLiteral(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			b.WriteString(`\\`)
		case '\'':
			b.WriteString(`\'`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case 0:
			b.WriteString(`\0`)
		case 26:
			b.WriteString(`\Z`)
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

var serialTok = regexp.MustCompile(`s:(\d+):"`)

// rewriteSerialized rewrites a decoded string value: plain occurrences are
// replaced, and every PHP serialized string token has its length recomputed
// (recursing into the token's content, which may itself be serialized data).
func rewriteSerialized(text, oldURL, newURL string) string {
	if !strings.Contains(text, oldURL) && !strings.Contains(text, jsonEsc(oldURL)) {
		return text
	}
	var out strings.Builder
	pos := 0
	for pos < len(text) {
		loc := serialTok.FindStringSubmatchIndex(text[pos:])
		if loc == nil {
			out.WriteString(replacePlain(text[pos:], oldURL, newURL))
			break
		}
		tokStart := pos + loc[0]
		contentStart := pos + loc[1]
		n, _ := strconv.Atoi(text[pos+loc[2] : pos+loc[3]])
		// A valid token: exactly n bytes then `";`
		if contentStart+n+2 <= len(text) && text[contentStart+n:contentStart+n+2] == `";` && (tokStart == 0 || !isAlnum(text[tokStart-1])) {
			out.WriteString(replacePlain(text[pos:tokStart], oldURL, newURL))
			inner := rewriteSerialized(text[contentStart:contentStart+n], oldURL, newURL)
			out.WriteString("s:" + strconv.Itoa(len(inner)) + `:"` + inner + `";`)
			pos = contentStart + n + 2
			continue
		}
		// Not a real token (e.g. the text merely contains "s:3:\""): treat the match as plain text.
		out.WriteString(replacePlain(text[pos:contentStart], oldURL, newURL))
		pos = contentStart
	}
	return out.String()
}

func isAlnum(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func jsonEsc(s string) string { return strings.ReplaceAll(s, "/", `\/`) }

func replacePlain(s, oldURL, newURL string) string {
	s = strings.ReplaceAll(s, oldURL, newURL)
	return strings.ReplaceAll(s, jsonEsc(oldURL), jsonEsc(newURL))
}

// CountOccurrences reports how many times url (plain or JSON-escaped) remains.
func CountOccurrences(sql, url string) int {
	url = strings.TrimRight(url, "/")
	return strings.Count(sql, url) + strings.Count(sql, jsonEsc(url))
}
