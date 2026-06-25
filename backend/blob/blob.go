package blob

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Store struct {
	Root string
}

type Saved struct {
	Path   string
	SHA256 string
	Size   int64
}

func New(root string) *Store {
	return &Store{Root: root}
}

func (s *Store) SaveAsset(userID int64, filename string, data []byte) (Saved, error) {
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	if strings.TrimSpace(filename) == "" {
		filename = "image"
	}
	name := fmt.Sprintf("%s-%s", hash[:16], safeSegment(filename))
	now := time.Now().UTC()
	parts := []string{
		"users", strconv.FormatInt(userID, 10), "blobs",
		"assets", now.Format("2006"), now.Format("01"), name,
	}
	return s.save(parts, data, hash)
}

func (s *Store) save(parts []string, data []byte, hash string) (Saved, error) {
	rel := filepath.Join(parts...)
	if filepath.IsAbs(rel) || strings.Contains(rel, "..") {
		return Saved{}, errors.New("unsafe blob path")
	}
	abs := filepath.Join(s.Root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return Saved{}, err
	}
	if err := os.WriteFile(abs, data, 0o600); err != nil {
		return Saved{}, err
	}
	return Saved{Path: rel, SHA256: hash, Size: int64(len(data))}, nil
}

func (s *Store) OpenUserBlob(userID int64, rel string) (*os.File, error) {
	clean := filepath.Clean(rel)
	if !userBlobPathAllowed(userID, clean) {
		return nil, errors.New("blob path is outside user scope")
	}
	return os.Open(filepath.Join(s.Root, clean))
}

func userBlobPathAllowed(userID int64, clean string) bool {
	if filepath.IsAbs(clean) || clean == "." || strings.Contains(clean, "..") {
		return false
	}
	prefix := filepath.Join("users", strconv.FormatInt(userID, 10), "blobs") + string(filepath.Separator)
	return strings.HasPrefix(clean, prefix)
}

func safeSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." {
		return "_"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) > 120 {
		out = out[:120]
	}
	return out
}
