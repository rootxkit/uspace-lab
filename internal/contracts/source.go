package contracts

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Systems are the four producing repositories whose schemas and OpenAPI
// files the aggregate mirrors, in pinning order (the CISP first: its
// skeleton is what the authority and the ANSP copy; decision record
// §4.3).
var Systems = []string{"cisp", "authority", "ussp", "ansp"}

// Source is a mirror's SOURCE file: where the copy came from. An empty
// Commit means the owning repository has not published the file yet and
// the directory holds no copy (unpinned).
type Source struct {
	Repo      string
	Commit    string
	Path      string
	FetchedAt string
}

// Pinned reports whether the mirror holds a copy at a commit.
func (s Source) Pinned() bool { return s.Commit != "" }

// ShortCommit is the first 12 characters of the commit.
func (s Source) ShortCommit() string {
	if len(s.Commit) > 12 {
		return s.Commit[:12]
	}
	return s.Commit
}

// ReadSource parses a SOURCE file (`key = value` lines, `#` comments).
// Unknown keys are an error, so a typo is not read as "unpinned".
func ReadSource(path string) (Source, error) {
	f, err := os.Open(path) //nolint:gosec // a repository path chosen by the caller
	if err != nil {
		return Source{}, err
	}
	defer func() { _ = f.Close() }()
	var s Source
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return Source{}, fmt.Errorf("%s:%d: not key = value", path, n)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "repo":
			s.Repo = v
		case "commit":
			s.Commit = v
		case "path":
			s.Path = v
		case "fetched_at":
			s.FetchedAt = v
		default:
			return Source{}, fmt.Errorf("%s:%d: unknown key %q", path, n, k)
		}
		seen[k] = true
	}
	if err := sc.Err(); err != nil {
		return Source{}, err
	}
	for _, k := range []string{"repo", "commit", "path", "fetched_at"} {
		if !seen[k] {
			return Source{}, fmt.Errorf("%s: missing key %q", path, k)
		}
	}
	if s.Repo == "" || s.Path == "" {
		return Source{}, fmt.Errorf("%s: repo and path must be set", path)
	}
	if s.Pinned() && (len(s.Commit) != 40 || strings.Trim(s.Commit, "0123456789abcdef") != "") {
		return Source{}, fmt.Errorf("%s: commit %q is not a full 40-hex SHA", path, s.Commit)
	}
	if s.Pinned() != (s.FetchedAt != "") {
		return Source{}, fmt.Errorf("%s: commit and fetched_at must be set together", path)
	}
	return s, nil
}

// Mirror is one mirrored directory: schemas/<system>/ or api/<system>/.
type Mirror struct {
	System string
	// Kind is "schemas" or "api".
	Kind   string
	Dir    string
	Source Source
}

// Mirrors reads the SOURCE file of every system directory under
// root/schemas and root/api. A system directory without SOURCE is an
// error: the layout is part of the contract.
func Mirrors(root string) ([]Mirror, error) {
	var out []Mirror
	for _, kind := range []string{"schemas", "api"} {
		for _, sys := range Systems {
			dir := filepath.Join(root, kind, sys)
			src, err := ReadSource(filepath.Join(dir, "SOURCE"))
			if errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("%s: missing SOURCE (every mirror directory has one)", dir)
			}
			if err != nil {
				return nil, err
			}
			out = append(out, Mirror{System: sys, Kind: kind, Dir: dir, Source: src})
		}
	}
	return out, nil
}
