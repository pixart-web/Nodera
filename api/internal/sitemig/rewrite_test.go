package sitemig

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// verifySerialized checks every s:N:"..."; token in text has a correct length.
func verifySerialized(t *testing.T, text string) {
	t.Helper()
	re := regexp.MustCompile(`s:(\d+):"`)
	for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
		n, _ := strconv.Atoi(text[m[2]:m[3]])
		end := m[1] + n
		if end+2 > len(text) || text[end:end+2] != `";` {
			t.Fatalf("broken serialized length at %d (n=%d): %q", m[0], n, text[m[0]:min(len(text), m[1]+n+4)])
		}
	}
}

func TestRewriteURLs_SerializedLengthsAreRecomputed(t *testing.T) {
	sql := "INSERT INTO `wp_options` VALUES (1,'siteurl','http://old.example.com','yes'),(2,'widget','a:1:{s:3:\"url\";s:22:\"http://old.example.com\";}','yes');"
	out, n := RewriteURLs(sql, "http://old.example.com", "https://new.example.org")
	if n != 2 {
		t.Fatalf("modified literals = %d, want 2", n)
	}
	if strings.Contains(out, "old.example.com") {
		t.Fatalf("old URL left behind: %s", out)
	}
	if !strings.Contains(out, `s:23:\"`) && !strings.Contains(out, `s:23:"`) {
		t.Fatalf("length not updated: %s", out)
	}
	if !strings.Contains(out, "'siteurl','https://new.example.org'") {
		t.Fatalf("plain value not rewritten: %s", out)
	}
}

func TestRewriteURLs_NestedSerializedAndJSONEscapes(t *testing.T) {
	inner := `a:1:{s:1:"u";s:22:"http://old.example.com";}`
	outer := `a:1:{s:4:"data";s:` + strconv.Itoa(len(inner)) + `:"` + inner + `";}`
	sql := "INSERT INTO t VALUES ('" + strings.ReplaceAll(outer, `'`, `\'`) + "','{\"link\":\"http:\\\\/\\\\/old.example.com\\\\/x\"}');"
	out, _ := RewriteURLs(sql, "http://old.example.com", "https://a-much-longer-new-domain.example.net")
	if strings.Contains(out, "old.example.com") || strings.Contains(out, `old.example.com`) {
		t.Fatalf("old URL survived: %s", out)
	}
	// Decode the first literal and verify both lengths (outer and inner) are valid.
	_, decoded, ok := readLiteral(out, strings.Index(out, "'"))
	if !ok {
		t.Fatal("cannot re-read literal")
	}
	verifySerialized(t, decoded)
	if !strings.Contains(out, `https:\\/\\/a-much-longer-new-domain.example.net\\/x`) && !strings.Contains(out, `https:\/\/a-much-longer-new-domain.example.net\/x`) {
		t.Fatalf("JSON-escaped URL not rewritten: %s", out)
	}
}

func TestRewriteURLs_OnlyTouchesLiterals(t *testing.T) {
	sql := "-- http://old.example.com in a comment\n/* http://old.example.com */\nCREATE TABLE `http://old.example.com` (a int);\nINSERT INTO t VALUES (5,'it''s http://old.example.com');\n"
	out, n := RewriteURLs(sql, "http://old.example.com", "http://new.example.com")
	if n != 1 {
		t.Fatalf("n=%d", n)
	}
	if strings.Count(out, "http://old.example.com") != 3 { // comment, block comment, identifier remain
		t.Fatalf("non-literal text was altered:\n%s", out)
	}
	if !strings.Contains(out, `'it\'s http://new.example.com'`) {
		t.Fatalf("literal not rewritten with proper escaping:\n%s", out)
	}
}

func TestRewriteURLs_MultibyteAndNoop(t *testing.T) {
	sql := `INSERT INTO t VALUES ('s:16:"é http://a.test";');`
	out, _ := RewriteURLs(sql, "http://a.test", "http://bb.test")
	if !strings.Contains(out, `s:17:"é http://bb.test";`) {
		t.Fatalf("byte length with multibyte chars wrong: %s", out)
	}
	same, n := RewriteURLs(sql, "http://zzz.test", "http://q.test")
	if same != sql || n != 0 {
		t.Fatal("no-op rewrite must return input unchanged")
	}
	if got := CountOccurrences(out, "http://a.test"); got != 0 {
		t.Fatalf("occurrences = %d", got)
	}
}
