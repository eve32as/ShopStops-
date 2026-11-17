// Package migrations implements reviewed, forward-only, one-shot migrations.
// It is deliberately independent of API startup.
package migrations

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type migration struct {
	version        int
	name, checksum string
	sql            []byte // Immutable snapshot: hash and execute these same bytes, never reopen.
}

var filenamePattern = regexp.MustCompile(`^([0-9]{3})_[a-z][a-z0-9]*(?:_[a-z0-9]+)*\.sql$`)

func loadManifest(dir string) ([]migration, error) {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("migration directory must be a readable real directory")
	}
	directory, err := os.Open(dir)
	if err != nil {
		return nil, errors.New("cannot open migration directory")
	}
	// Bound enumeration before allocating for arbitrary unexpected entries.
	entries, err := directory.ReadDir(1000)
	closeErr := directory.Close()
	if (err != nil && !errors.Is(err, io.EOF)) || closeErr != nil || len(entries) == 0 || len(entries) > 999 {
		return nil, errors.New("migration directory must contain 1..999 ordered SQL files")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var result []migration
	var total int
	for i, entry := range entries {
		name := entry.Name()
		if !filenamePattern.MatchString(name) || !strings.HasPrefix(name, fmt.Sprintf("%03d_", i+1)) {
			return nil, errors.New("invalid migration manifest: require sequential three-digit versions from 001 and safe lowercase names; no extra files")
		}
		before, err := os.Lstat(filepath.Join(dir, name))
		if err != nil || !before.Mode().IsRegular() || before.Size() > 1<<20 {
			return nil, errors.New("migration must be a regular non-symlink file of at most 1 MiB")
		}
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return nil, errors.New("cannot read migration snapshot")
		}
		after, statErr := f.Stat()
		body, readErr := io.ReadAll(io.LimitReader(f, (1<<20)+1))
		closeErr := f.Close()
		if statErr != nil || !os.SameFile(before, after) || readErr != nil || closeErr != nil || len(body) == 0 || len(body) > 1<<20 {
			return nil, errors.New("cannot read stable bounded migration snapshot")
		}
		total += len(body)
		if total > 16<<20 {
			return nil, errors.New("migration manifest exceeds 16 MiB")
		}
		if err := validateSQL(string(body)); err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		result = append(result, migration{i + 1, name, hex.EncodeToString(sum[:]), body})
	}
	return result, nil
}

// validateSQL is a conservative lexical transaction-boundary guard, NOT a SQL
// validator or sandbox. Quotes, nested comments and dollar bodies are skipped;
// PL/pgSQL BEGIN/END inside functions must not be mistaken for COMMIT/BEGIN.
// Reviewed trusted SQL remains mandatory. SQL-standard unquoted function bodies
// and nontransactional operations need separate tooling, not a bypass flag.
func validateSQL(s string) error {
	bad := errors.New("migration SQL requires review: unsupported transaction/session control, nontransactional operation or unterminated quoting")
	var words []string
	check := func() bool {
		if len(words) == 0 {
			return true
		}
		switch words[0] {
		case "BEGIN", "END", "COMMIT", "ROLLBACK", "ABORT", "START", "SAVEPOINT", "RELEASE", "PREPARE", "SET", "RESET", "DISCARD", "VACUUM", "CALL", "EXECUTE":
			return false
		}
		for _, w := range words {
			if w == "CONCURRENTLY" {
				return false
			}
		}
		return true
	}
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "--"):
			if n := strings.IndexByte(s[i:], '\n'); n >= 0 {
				i += n + 1
			} else {
				i = len(s)
			}
		case strings.HasPrefix(s[i:], "/*"):
			i += 2
			depth := 1
			for i < len(s) && depth > 0 {
				if strings.HasPrefix(s[i:], "/*") {
					depth++
					i += 2
				} else if strings.HasPrefix(s[i:], "*/") {
					depth--
					i += 2
				} else {
					i++
				}
			}
			if depth != 0 {
				return bad
			}
		case s[i] == '\'' || s[i] == '"':
			quote := s[i]
			escape := quote == '\'' && i > 0 && (s[i-1] == 'e' || s[i-1] == 'E') && (i < 2 || !identifierByte(s[i-2]))
			i++
			closed := false
			for i < len(s) {
				if escape && s[i] == '\\' {
					i += 2
					continue
				}
				if s[i] == quote {
					i++
					if i < len(s) && s[i] == quote {
						i++
						continue
					}
					closed = true
					break
				}
				i++
			}
			if !closed {
				return bad
			}
			words = append(words, "<quoted>")
		case s[i] == '$':
			j := i + 1
			for j < len(s) && (identifierByte(s[j]) && s[j] != '$') {
				j++
			}
			if j == len(s) || s[j] != '$' {
				return bad
			}
			tag := s[i : j+1]
			n := strings.Index(s[j+1:], tag)
			if n < 0 {
				return bad
			}
			i = j + 1 + n + len(tag)
			words = append(words, "<body>")
		case s[i] == ';':
			if !check() {
				return bad
			}
			words = nil
			i++
		case identifierByte(s[i]):
			j := i + 1
			for j < len(s) && identifierByte(s[j]) {
				j++
			}
			words = append(words, strings.ToUpper(s[i:j]))
			i = j
		case s[i] == '\\' || s[i] == 0:
			return bad
		default:
			i++
		}
	}
	if !check() {
		return bad
	}
	return nil
}

func identifierByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '$' || b >= 128
}
