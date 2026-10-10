package sitemig

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/nodera/nodera/internal/providers"
)

var (
	wpVersionRe = regexp.MustCompile(`\$wp_version\s*=\s*'([^']+)'`)
	siteURLRe   = regexp.MustCompile(`'siteurl',\s*'(https?://[^']+)'`)
)

func randomPassword() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// cleanEntry returns a safe relative name for a zip entry, or "" to skip it.
func cleanEntry(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	if strings.ContainsRune(name, 0) || strings.Contains(name, `\`) || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("unsafe path in archive: %q", name)
	}
	name = strings.TrimSuffix(name, "/")
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return "", fmt.Errorf("path traversal in archive: %q", name)
		}
	}
	return path.Clean(name), nil
}

type entry struct {
	name string // path relative to the WordPress root
	f    *zip.File
}

// openArchive validates limits and finds the WordPress root inside the zip.
func openArchive(data []byte) (*zip.Reader, string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, "", errors.New("not a valid zip archive")
	}
	if len(zr.File) > maxEntries {
		return nil, "", errors.New("archive has too many entries")
	}
	var total uint64
	root, depth := "", 1<<30
	for _, f := range zr.File {
		total += f.UncompressedSize64
		if total > maxExtracted {
			return nil, "", errors.New("archive expands beyond the size limit")
		}
		name, err := cleanEntry(f.Name)
		if err != nil {
			return nil, "", err
		}
		if base := path.Base(name); (base == "wp-config.php" || name == "wp-includes/version.php" || strings.HasSuffix(name, "/wp-includes/version.php")) && !f.FileInfo().IsDir() {
			dir := path.Dir(name)
			if strings.HasSuffix(name, "wp-includes/version.php") {
				dir = path.Dir(path.Dir(name))
			}
			if dir == "." {
				dir = ""
			}
			if d := strings.Count(dir, "/"); d < depth {
				root, depth = dir, d
			}
		}
	}
	return zr, root, nil
}

func entries(zr *zip.Reader, root string) []entry {
	var out []entry
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || f.Mode()&0o170000 == 0o120000 { // skip dirs and symlinks
			continue
		}
		name, err := cleanEntry(f.Name)
		if err != nil || name == "" {
			continue
		}
		rel := name
		if root != "" {
			if !strings.HasPrefix(name, root+"/") {
				// Files outside the WordPress root: only a top-level SQL dump is kept.
				if strings.HasSuffix(strings.ToLower(name), ".sql") && !strings.Contains(name, "/") {
					out = append(out, entry{name: name, f: f})
				}
				continue
			}
			rel = strings.TrimPrefix(name, root+"/")
		}
		out = append(out, entry{name: rel, f: f})
	}
	return out
}

func readAll(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("entry exceeds the size limit")
	}
	return b, nil
}

func isSQL(name string) bool {
	l := strings.ToLower(name)
	return strings.HasSuffix(l, ".sql") && strings.Count(l, "/") <= 1
}

// inspectArchive reads the archive (read-only) and describes the site in it.
func inspectArchive(data []byte) (SourceInfo, error) {
	zr, root, err := openArchive(data)
	if err != nil {
		return SourceInfo{}, err
	}
	var info SourceInfo
	plugins, themes := map[string]bool{}, map[string]bool{}
	var sqlFile *zip.File
	for _, e := range entries(zr, root) {
		switch {
		case isSQL(e.name):
			if sqlFile == nil || e.name == "database.sql" {
				sqlFile = e.f
			}
			continue
		case e.name == "wp-config.php":
			if b, err := readAll(e.f, 1<<20); err == nil {
				info.Multisite = bytes.Contains(b, []byte("MULTISITE")) && regexp.MustCompile(`MULTISITE['"]\s*,\s*true`).Match(b)
			}
			info.IsWordPress = true
			continue // never counted as a transferable file
		case e.name == "wp-includes/version.php":
			info.IsWordPress = true
			if b, err := readAll(e.f, 1<<20); err == nil {
				if m := wpVersionRe.FindSubmatch(b); m != nil {
					info.WPVersion = string(m[1])
				}
			}
		case strings.HasPrefix(e.name, "wp-content/plugins/"):
			if p := strings.SplitN(strings.TrimPrefix(e.name, "wp-content/plugins/"), "/", 2); len(p) == 2 {
				plugins[p[0]] = true
			}
		case strings.HasPrefix(e.name, "wp-content/themes/"):
			if p := strings.SplitN(strings.TrimPrefix(e.name, "wp-content/themes/"), "/", 2); len(p) == 2 {
				themes[p[0]] = true
			}
		}
		info.Files++
		info.SizeBytes += int64(e.f.UncompressedSize64)
	}
	if sqlFile != nil {
		info.HasDatabase = true
		info.DBBytes = int64(sqlFile.UncompressedSize64)
		if sqlFile.UncompressedSize64 <= 256<<20 {
			if b, err := readAll(sqlFile, 256<<20); err == nil {
				if m := siteURLRe.FindSubmatch(b); m != nil {
					info.SiteURL = string(m[1])
				}
			}
		}
	}
	info.Plugins, info.Themes = keys(plugins), keys(themes)
	return info, nil
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// extractArchive writes the site into stage/files and the dump into
// stage/database.sql. wp-config.php is deliberately not copied: it holds the
// old server's database credentials, and the target container generates its
// own configuration. Extraction is bounded by actual (not declared) sizes.
func extractArchive(ctx context.Context, fs providers.FilesystemProvider, data []byte, stage string) (SourceInfo, error) {
	info, err := inspectArchive(data)
	if err != nil {
		return SourceInfo{}, err
	}
	zr, root, err := openArchive(data)
	if err != nil {
		return SourceInfo{}, err
	}
	var written int64
	wroteSQL := false
	for _, e := range entries(zr, root) {
		if err := ctx.Err(); err != nil {
			return SourceInfo{}, err
		}
		if e.name == "wp-config.php" || strings.HasPrefix(e.name, "wp-content/cache/") {
			continue
		}
		b, err := readAll(e.f, maxExtracted-written)
		if err != nil {
			return SourceInfo{}, fmt.Errorf("%s: %w", e.name, err)
		}
		written += int64(len(b))
		if written > maxExtracted {
			return SourceInfo{}, errors.New("archive expands beyond the size limit")
		}
		if isSQL(e.name) {
			if wroteSQL && e.name != "database.sql" {
				continue
			}
			if err := fs.WriteFile(ctx, stage+"/database.sql", b); err != nil {
				return SourceInfo{}, err
			}
			wroteSQL = true
			continue
		}
		if err := fs.WriteFile(ctx, stage+"/files/"+e.name, b); err != nil {
			return SourceInfo{}, err
		}
	}
	return info, nil
}
