package search

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
	blevequery "github.com/blevesearch/bleve/v2/search/query"

	"cairnfield/backend/store"
)

type Service struct {
	root    string
	mu      sync.Mutex
	indexes map[int64]bleve.Index
	writers map[int64]*sync.Mutex
}

const indexVersion = "bleve-v2"

type Hit struct {
	ID    int64   `json:"id"`
	Score float64 `json:"score"`
}

func OpenPerUser(root string) (*Service, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	return &Service{root: root, indexes: map[int64]bleve.Index{}, writers: map[int64]*sync.Mutex{}}, nil
}

func (s *Service) Close() error {
	s.mu.Lock()
	indexes := make([]bleve.Index, 0, len(s.indexes))
	for _, idx := range s.indexes {
		indexes = append(indexes, idx)
	}
	s.indexes = map[int64]bleve.Index{}
	s.mu.Unlock()
	var first error
	for _, idx := range indexes {
		if err := idx.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (s *Service) indexForUser(userID int64) (bleve.Index, error) {
	if userID == 0 {
		return nil, fmt.Errorf("user id is required")
	}
	s.mu.Lock()
	if idx := s.indexes[userID]; idx != nil {
		s.mu.Unlock()
		return idx, nil
	}
	s.mu.Unlock()
	idx, err := openIndex(filepath.Join(s.root, strconv.FormatInt(userID, 10), indexVersion))
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if existing := s.indexes[userID]; existing != nil {
		s.mu.Unlock()
		_ = idx.Close()
		return existing, nil
	}
	s.indexes[userID] = idx
	s.mu.Unlock()
	return idx, nil
}

func openIndex(path string) (bleve.Index, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	idx, err := bleve.Open(path)
	if err == nil {
		return idx, nil
	}
	m := bleve.NewIndexMapping()
	m.StoreDynamic = false
	doc := bleve.NewDocumentMapping()
	for _, field := range []string{"user_id", "folder_path", "is_encrypted", "is_shared", "has_image"} {
		doc.AddFieldMappingsAt(field, keywordField())
	}
	for _, field := range []string{"title", "title_compound", "content", "headers", "tags", "path_text", "asset_text", "compound"} {
		doc.AddFieldMappingsAt(field, textField())
	}
	doc.AddFieldMappingsAt("updated_at", dateField())
	m.DefaultMapping = doc
	return bleve.New(path, m)
}

func textField() *mapping.FieldMapping {
	f := bleve.NewTextFieldMapping()
	f.Store = false
	f.DocValues = false
	return f
}

func keywordField() *mapping.FieldMapping {
	f := bleve.NewKeywordFieldMapping()
	f.Store = false
	f.IncludeInAll = false
	return f
}

func dateField() *mapping.FieldMapping {
	f := bleve.NewDateTimeFieldMapping()
	f.Store = false
	f.IncludeInAll = false
	return f
}

func (s *Service) writerForUser(userID int64) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writers[userID] == nil {
		s.writers[userID] = &sync.Mutex{}
	}
	return s.writers[userID]
}

func (s *Service) Index(ctx context.Context, doc store.SearchDocument) error {
	if doc.UserID == 0 || doc.NoteID == 0 {
		return nil
	}
	idx, err := s.indexForUser(doc.UserID)
	if err != nil {
		return err
	}
	writer := s.writerForUser(doc.UserID)
	writer.Lock()
	defer writer.Unlock()
	if doc.Encrypted {
		return idx.Delete(strconv.FormatInt(doc.NoteID, 10))
	}
	return idx.Index(strconv.FormatInt(doc.NoteID, 10), map[string]any{
		"user_id":        strconv.FormatInt(doc.UserID, 10),
		"title":          doc.Title,
		"title_compound": compound(doc.Title),
		"folder_path":    doc.FolderPath,
		"path_text":      strings.ReplaceAll(strings.Trim(doc.FolderPath, "/"), "/", " "),
		"content":        doc.Content,
		"headers":        doc.HeaderJSON,
		"asset_text":     doc.AssetText,
		"tags":           tagsFromHeader(doc.HeaderJSON),
		"updated_at":     doc.UpdatedAt,
		"is_encrypted":   strconv.FormatBool(doc.Encrypted),
		"is_shared":      strconv.FormatBool(doc.Shared),
		"has_image":      strconv.FormatBool(doc.HasImage),
		"compound":       compound(doc.Title, doc.FolderPath, doc.Content, doc.HeaderJSON, doc.AssetText),
	})
}

func (s *Service) Delete(ctx context.Context, userID, noteID int64) error {
	idx, err := s.indexForUser(userID)
	if err != nil {
		return err
	}
	writer := s.writerForUser(userID)
	writer.Lock()
	defer writer.Unlock()
	return idx.Delete(strconv.FormatInt(noteID, 10))
}

func (s *Service) Rebuild(ctx context.Context, userID int64, docs []store.SearchDocument) error {
	idx, err := s.indexForUser(userID)
	if err != nil {
		return err
	}
	batch := idx.NewBatch()
	for _, d := range docs {
		if d.UserID != userID || d.Encrypted {
			continue
		}
		batch.Index(strconv.FormatInt(d.NoteID, 10), map[string]any{
			"user_id":        strconv.FormatInt(d.UserID, 10),
			"title":          d.Title,
			"title_compound": compound(d.Title),
			"folder_path":    d.FolderPath,
			"path_text":      strings.ReplaceAll(strings.Trim(d.FolderPath, "/"), "/", " "),
			"content":        d.Content,
			"headers":        d.HeaderJSON,
			"asset_text":     d.AssetText,
			"tags":           tagsFromHeader(d.HeaderJSON),
			"updated_at":     d.UpdatedAt,
			"is_encrypted":   strconv.FormatBool(d.Encrypted),
			"is_shared":      strconv.FormatBool(d.Shared),
			"has_image":      strconv.FormatBool(d.HasImage),
			"compound":       compound(d.Title, d.FolderPath, d.Content, d.HeaderJSON, d.AssetText),
		})
	}
	writer := s.writerForUser(userID)
	writer.Lock()
	defer writer.Unlock()
	return idx.Batch(batch)
}

func (s *Service) Search(ctx context.Context, userID int64, queryText string, limit, offset int) ([]Hit, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	queryText = strings.TrimSpace(queryText)
	if queryText == "" {
		return nil, nil
	}
	idx, err := s.indexForUser(userID)
	if err != nil {
		return nil, err
	}
	req := bleve.NewSearchRequestOptions(buildQuery(userID, queryText), limit, offset, false)
	req.SortBy([]string{"-_score", "-updated_at"})
	res, err := idx.Search(req)
	if err != nil {
		return nil, err
	}
	out := make([]Hit, 0, len(res.Hits))
	for _, hit := range res.Hits {
		id, err := strconv.ParseInt(hit.ID, 10, 64)
		if err == nil {
			out = append(out, Hit{ID: id, Score: hit.Score})
		}
	}
	return out, nil
}

type parsedQuery struct {
	Text      []string
	Title     string
	Path      string
	Tag       string
	After     time.Time
	Before    time.Time
	Encrypted *bool
	Shared    *bool
	HasImage  *bool
}

func buildQuery(userID int64, raw string) blevequery.Query {
	p := parse(raw)
	must := []blevequery.Query{term("user_id", strconv.FormatInt(userID, 10))}
	for _, text := range p.Text {
		if text == "" {
			continue
		}
		queries := []blevequery.Query{
			match("title", text, 3.5),
			match("title_compound", compound(text), 4),
			match("content", text, 1),
			match("asset_text", text, 1.2),
			match("headers", text, .8),
			match("path_text", text, 1.2),
			match("compound", text, 1.4),
		}
		must = append(must, bleve.NewDisjunctionQuery(queries...))
	}
	if p.Title != "" {
		must = append(must, match("title", p.Title, 4))
	}
	if p.Path != "" {
		q := bleve.NewPrefixQuery(normalizePath(p.Path))
		q.SetField("folder_path")
		must = append(must, q)
	}
	if p.Tag != "" {
		must = append(must, match("tags", p.Tag, 2.5))
	}
	if !p.After.IsZero() {
		q := bleve.NewDateRangeQuery(p.After, time.Time{})
		q.SetField("updated_at")
		must = append(must, q)
	}
	if !p.Before.IsZero() {
		q := bleve.NewDateRangeQuery(time.Time{}, p.Before)
		q.SetField("updated_at")
		must = append(must, q)
	}
	if p.Encrypted != nil {
		must = append(must, term("is_encrypted", strconv.FormatBool(*p.Encrypted)))
	}
	if p.Shared != nil {
		must = append(must, term("is_shared", strconv.FormatBool(*p.Shared)))
	}
	if p.HasImage != nil {
		must = append(must, term("has_image", strconv.FormatBool(*p.HasImage)))
	}
	return bleve.NewConjunctionQuery(must...)
}

func term(field, value string) blevequery.Query {
	q := bleve.NewTermQuery(value)
	q.SetField(field)
	return q
}

func match(field, value string, boost float64) blevequery.Query {
	q := bleve.NewMatchQuery(value)
	q.SetField(field)
	q.SetBoost(boost)
	return q
}

func parse(raw string) parsedQuery {
	var p parsedQuery
	for _, tok := range splitTokens(raw) {
		lower := strings.ToLower(tok)
		switch {
		case strings.HasPrefix(lower, "title:"):
			p.Title = unquote(tok[len("title:"):])
		case strings.HasPrefix(lower, "path:"):
			p.Path = unquote(tok[len("path:"):])
		case strings.HasPrefix(lower, "tag:"):
			p.Tag = unquote(tok[len("tag:"):])
		case strings.HasPrefix(lower, "after:"):
			p.After = parseDate(unquote(tok[len("after:"):]), false)
		case strings.HasPrefix(lower, "before:"):
			p.Before = parseDate(unquote(tok[len("before:"):]), true)
		case strings.HasPrefix(lower, "year:"):
			year, _ := strconv.Atoi(unquote(tok[len("year:"):]))
			if year > 0 {
				p.After = time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
				p.Before = time.Date(year, 12, 31, 23, 59, 59, 0, time.UTC)
			}
		case lower == "is:encrypted":
			v := true
			p.Encrypted = &v
		case lower == "is:shared":
			v := true
			p.Shared = &v
		case lower == "has:image":
			v := true
			p.HasImage = &v
		default:
			p.Text = append(p.Text, unquote(tok))
		}
	}
	return p
}

func splitTokens(raw string) []string {
	var out []string
	var b strings.Builder
	quoted := false
	for _, r := range raw {
		switch {
		case r == '"':
			quoted = !quoted
			b.WriteRune(r)
		case r == ' ' || r == '\t' || r == '\n':
			if quoted {
				b.WriteRune(r)
			} else if b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
			}
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

func unquote(value string) string {
	return strings.Trim(strings.TrimSpace(value), `"`)
}

func normalizePath(path string) string {
	path = "/" + strings.Trim(strings.TrimSpace(path), "/")
	if path == "/" {
		return "/"
	}
	return path
}

func parseDate(value string, end bool) time.Time {
	for _, layout := range []string{"2006-01-02", "2006/01/02"} {
		t, err := time.ParseInLocation(layout, value, time.UTC)
		if err == nil {
			if end {
				return t.Add(24*time.Hour - time.Second)
			}
			return t
		}
	}
	return time.Time{}
}

func compound(values ...string) string {
	var b strings.Builder
	for _, value := range values {
		for _, r := range strings.ToLower(value) {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			}
		}
		b.WriteByte(' ')
	}
	return b.String()
}

func tagsFromHeader(header string) string {
	if !strings.Contains(header, "tags") {
		return ""
	}
	header = strings.NewReplacer("[", " ", "]", " ", `"`, " ", ",", " ", ":", " ").Replace(header)
	return header
}
