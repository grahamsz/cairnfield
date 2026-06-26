package document

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"html"
	"io"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	maxTextBytes       = 1 * 1024 * 1024
	maxPDFBytes        = 32 * 1024 * 1024
	maxOfficeBytes     = 32 * 1024 * 1024
	extractionTimeout  = 10 * time.Second
	defaultContentType = "application/octet-stream"
)

var (
	htmlNoiseRE = regexp.MustCompile(`(?is)<(script|style|noscript)[^>]*>.*?</(script|style|noscript)>`)
	htmlTagRE   = regexp.MustCompile(`(?is)<[^>]+>`)
)

// SearchableText extracts bounded plain text from documents that are useful to
// index. It mirrors Rolltop's document flow: pdftotext for PDFs, XML extraction
// from office zip formats, optional external tools for legacy .doc, and direct
// indexing for text-like files.
func SearchableText(filename, contentType string, data []byte) string {
	mediaType := normalizedMediaType(contentType)
	ext := strings.ToLower(filepath.Ext(filename))
	switch {
	case mediaType == "text/html" || ext == ".html" || ext == ".htm":
		return normalizeText(stripHTML(string(limitBytes(data, maxTextBytes))))
	case isPDF(mediaType, ext):
		text, err := extractPDFText(data)
		if err != nil {
			return ""
		}
		return normalizeText(text)
	case isDOCX(mediaType, ext):
		text, err := extractDOCXText(data)
		if err != nil {
			return ""
		}
		return normalizeText(text)
	case isODF(mediaType, ext):
		text, err := extractODFText(data)
		if err != nil {
			return ""
		}
		return normalizeText(text)
	case isDOC(mediaType, ext):
		text, err := extractDOCText(data)
		if err != nil {
			return ""
		}
		return normalizeText(text)
	case isTextLike(mediaType, ext):
		return normalizeText(string(limitBytes(data, maxTextBytes)))
	default:
		return ""
	}
}

func normalizedMediaType(contentType string) string {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.TrimSpace(contentType)
	}
	mediaType = strings.ToLower(mediaType)
	if mediaType == "" {
		return defaultContentType
	}
	return mediaType
}

func isPDF(mediaType, ext string) bool {
	return mediaType == "application/pdf" || mediaType == "application/x-pdf" || ext == ".pdf"
}

func isDOCX(mediaType, ext string) bool {
	return mediaType == "application/vnd.openxmlformats-officedocument.wordprocessingml.document" || ext == ".docx"
}

func isDOC(mediaType, ext string) bool {
	return mediaType == "application/msword" || mediaType == "application/vnd.ms-word" || mediaType == "application/x-msword" || ext == ".doc"
}

func isODF(mediaType, ext string) bool {
	switch mediaType {
	case "application/vnd.oasis.opendocument.text", "application/vnd.oasis.opendocument.spreadsheet", "application/vnd.oasis.opendocument.presentation":
		return true
	default:
		return ext == ".odt" || ext == ".ods" || ext == ".odp"
	}
}

func isTextLike(mediaType, ext string) bool {
	if strings.HasPrefix(mediaType, "text/") {
		return true
	}
	switch mediaType {
	case "application/json", "application/xml", "application/xhtml+xml", "application/csv", "application/ics", "application/javascript", "application/x-javascript":
		return true
	}
	switch ext {
	case ".txt", ".text", ".md", ".markdown", ".csv", ".tsv", ".json", ".xml", ".html", ".htm", ".ics", ".vcf", ".log", ".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".css", ".sql", ".yaml", ".yml", ".toml":
		return true
	default:
		return false
	}
}

func extractPDFText(data []byte) (string, error) {
	if len(data) == 0 {
		return "", nil
	}
	if len(data) > maxPDFBytes {
		return "", errors.New("pdf too large for search extraction")
	}
	tmp, err := os.CreateTemp("", "cairnfield-pdf-*.pdf")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), extractionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "pdftotext", "-enc", "UTF-8", "-layout", tmpName, "-")
	cmd.Stderr = io.Discard
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", err
	}
	return string(limitBytes(out, maxTextBytes)), nil
}

func extractDOCXText(data []byte) (string, error) {
	return extractOfficeZipText(data, isDOCXTextPart, map[string]bool{"t": true})
}

func extractODFText(data []byte) (string, error) {
	return extractOfficeZipText(data, func(name string) bool { return name == "content.xml" }, map[string]bool{"p": true, "span": true, "h": true, "a": true})
}

func extractOfficeZipText(data []byte, includePart func(string) bool, textElements map[string]bool) (string, error) {
	if len(data) == 0 {
		return "", nil
	}
	if len(data) > maxOfficeBytes {
		return "", errors.New("office document too large for search extraction")
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for _, file := range reader.File {
		name := strings.ToLower(file.Name)
		if !includePart(name) {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return "", err
		}
		err = appendOfficeXMLText(&out, io.LimitReader(rc, maxOfficeBytes), textElements)
		closeErr := rc.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
		if out.Len() >= maxTextBytes {
			break
		}
	}
	return out.String(), nil
}

func isDOCXTextPart(name string) bool {
	if name == "word/document.xml" {
		return true
	}
	if !strings.HasPrefix(name, "word/") || !strings.HasSuffix(name, ".xml") {
		return false
	}
	base := filepath.Base(name)
	return strings.HasPrefix(base, "header") || strings.HasPrefix(base, "footer") || base == "footnotes.xml" || base == "endnotes.xml" || base == "comments.xml"
}

func appendOfficeXMLText(out *strings.Builder, r io.Reader, textElements map[string]bool) error {
	decoder := xml.NewDecoder(r)
	decoder.Strict = false
	textDepth := 0
	for {
		tok, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if textDepth > 0 || textElements[strings.ToLower(t.Name.Local)] {
				textDepth++
			}
		case xml.CharData:
			if textDepth > 0 {
				appendBoundedText(out, string(t))
				if out.Len() >= maxTextBytes {
					return nil
				}
			}
		case xml.EndElement:
			if textDepth > 0 {
				textDepth--
			}
		}
	}
}

func extractDOCText(data []byte) (string, error) {
	if len(data) == 0 {
		return "", nil
	}
	if len(data) > maxOfficeBytes {
		return "", errors.New("doc too large for search extraction")
	}
	tmp, err := os.CreateTemp("", "cairnfield-doc-*.doc")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	commands := [][]string{
		{"antiword", "-m", "UTF-8.txt", tmpName},
		{"catdoc", "-w", tmpName},
	}
	var lastErr error
	for _, args := range commands {
		ctx, cancel := context.WithTimeout(context.Background(), extractionTimeout)
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Stderr = io.Discard
		out, err := cmd.Output()
		cancel()
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if err == nil {
			return string(limitBytes(out, maxTextBytes)), nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", errors.New("doc text extractor unavailable")
}

func appendBoundedText(out *strings.Builder, value string) {
	value = strings.TrimSpace(value)
	if value == "" || out.Len() >= maxTextBytes {
		return
	}
	if out.Len() > 0 {
		out.WriteByte(' ')
	}
	remaining := maxTextBytes - out.Len()
	if len(value) > remaining {
		value = string(limitBytes([]byte(value), remaining))
	}
	out.WriteString(value)
}

func normalizeText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func stripHTML(value string) string {
	value = htmlNoiseRE.ReplaceAllString(value, " ")
	return html.UnescapeString(htmlTagRE.ReplaceAllString(value, " "))
}

func limitBytes(data []byte, max int) []byte {
	if len(data) <= max {
		return data
	}
	return data[:max]
}
