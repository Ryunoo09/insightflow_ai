package datasets

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFormatFromFilename(t *testing.T) {
	cases := map[string]string{
		"data.csv":   FormatCSV,
		"DATA.CSV":   FormatCSV,
		"book.xlsx":  FormatXLSX,
		"book.XLSX":  FormatXLSX,
	}
	for name, want := range cases {
		got, ok := FormatFromFilename(name)
		if !ok || got != want {
			t.Fatalf("FormatFromFilename(%q) = %q,%v want %q", name, got, ok, want)
		}
	}
	for _, name := range []string{"data.xls", "data.xlsm", "data.json", "noext"} {
		if _, ok := FormatFromFilename(name); ok {
			t.Fatalf("FormatFromFilename(%q) unexpectedly supported", name)
		}
	}
}

func TestSanitizeFilename(t *testing.T) {
	if got := SanitizeFilename(`..\..\etc\passwd`); got != "passwd" {
		t.Fatalf("path traversal not stripped: %q", got)
	}
	if got := SanitizeFilename("/a/b/c.csv"); got != "c.csv" {
		t.Fatalf("path not stripped: %q", got)
	}
	if got := SanitizeFilename("a\x00b\n.csv"); got != "ab.csv" {
		t.Fatalf("control chars not stripped: %q", got)
	}
	if got := SanitizeFilename("   "); got != "dataset" {
		t.Fatalf("empty name fallback: %q", got)
	}
	long := make([]byte, 400)
	for i := range long {
		long[i] = 'a'
	}
	if got := SanitizeFilename(string(long)); len(got) != 255 {
		t.Fatalf("length not bounded: %d", len(got))
	}
}

func TestValidateSignature(t *testing.T) {
	// A real XLSX starts with the ZIP local-file header.
	if err := ValidateSignature(FormatXLSX, []byte{'P', 'K', 3, 4, 0, 0}); err != nil {
		t.Fatalf("valid xlsx signature rejected: %v", err)
	}
	// A renamed executable (MZ header) must be rejected.
	if err := ValidateSignature(FormatXLSX, []byte{'M', 'Z', 0x90, 0x00}); !errors.Is(err, ErrInvalidFormat) {
		t.Fatalf("fake xlsx accepted: %v", err)
	}
	// CSV with NUL bytes is treated as binary.
	if err := ValidateSignature(FormatCSV, []byte("a,b,c\x00d")); !errors.Is(err, ErrInvalidFormat) {
		t.Fatalf("binary csv accepted: %v", err)
	}
	if err := ValidateSignature(FormatCSV, []byte("a,b,c\n1,2,3\n")); err != nil {
		t.Fatalf("valid csv rejected: %v", err)
	}
	if err := ValidateSignature("xls", []byte("whatever")); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("unsupported format accepted: %v", err)
	}
}

func TestCheckXLSXArchive(t *testing.T) {
	okPath := writeZip(t, "ok.xlsx", []byte("small content"))
	if err := CheckXLSXArchive(okPath); err != nil {
		t.Fatalf("normal archive rejected: %v", err)
	}

	// Highly compressible data balloons the ratio above the ceiling.
	bombPath := writeZip(t, "bomb.xlsx", bytes.Repeat([]byte{0}, 2<<20))
	if err := CheckXLSXArchive(bombPath); !errors.Is(err, ErrInvalidFormat) {
		t.Fatalf("zip bomb accepted: %v", err)
	}

	if err := CheckXLSXArchive(filepath.Join(t.TempDir(), "missing.xlsx")); !errors.Is(err, ErrInvalidFormat) {
		t.Fatalf("missing archive accepted: %v", err)
	}
}

func writeZip(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("xl/workbook.xml")
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
	return path
}
