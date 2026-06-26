package document

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestSearchableTextExtractsDOCX(t *testing.T) {
	text := SearchableText("community.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", officeZipFixture(t, map[string]string{
		"word/document.xml": `<w:document xmlns:w="urn:test"><w:body><w:p><w:r><w:t>Pillars of Community docx text</w:t></w:r></w:p></w:body></w:document>`,
	}))
	if !strings.Contains(text, "Pillars of Community docx text") {
		t.Fatalf("searchable text = %q", text)
	}
}

func TestSearchableTextExtractsODT(t *testing.T) {
	text := SearchableText("forecast.odt", "application/vnd.oasis.opendocument.text", officeZipFixture(t, map[string]string{
		"content.xml": `<office:document-content xmlns:office="urn:office" xmlns:text="urn:text"><office:body><text:p>Open office searchable needle</text:p></office:body></office:document-content>`,
	}))
	if !strings.Contains(text, "Open office searchable needle") {
		t.Fatalf("searchable text = %q", text)
	}
}

func officeZipFixture(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
