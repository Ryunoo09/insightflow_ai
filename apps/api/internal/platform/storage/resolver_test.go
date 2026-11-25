package storage

import (
	"path/filepath"
	"strings"
	"testing"
)

func newTestResolver(t *testing.T) *Resolver {
	t.Helper()
	r, err := NewResolver(filepath.Join(t.TempDir(), "storage"))
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return r
}

func TestBuildsPathsUnderRoot(t *testing.T) {
	r := newTestResolver(t)

	got, err := r.ParquetPath("ws1", "ds1", 2)
	if err != nil {
		t.Fatalf("ParquetPath: %v", err)
	}
	want := filepath.Join(r.Root(), "parquet", "ws1", "ds1", "2", "data.parquet")
	if got != want {
		t.Fatalf("ParquetPath = %q, want %q", got, want)
	}

	if _, err := r.SourcePath("ws1", "ds1", 1, ".CSV"); err != nil {
		t.Fatalf("SourcePath: %v", err)
	}
	if _, err := r.TempPath("scratch.tmp"); err != nil {
		t.Fatalf("TempPath: %v", err)
	}
}

func TestRejectsUnsafeSegments(t *testing.T) {
	r := newTestResolver(t)

	cases := []struct {
		name string
		call func() error
	}{
		{"traversal in temp name", func() error { _, err := r.TempPath("../../etc/passwd"); return err }},
		{"separator in workspace id", func() error { _, err := r.SourcePath("a/b", "ds1", 1, "csv"); return err }},
		{"parent dir as dataset id", func() error { _, err := r.ParquetPath("ws1", "..", 1); return err }},
		{"backslash in id", func() error { _, err := r.ParquetPath(`ws1\evil`, "ds1", 1); return err }},
		{"empty workspace id", func() error { _, err := r.ParquetPath("", "ds1", 1); return err }},
		{"empty extension", func() error { _, err := r.SourcePath("ws1", "ds1", 1, "."); return err }},
	}

	for _, tc := range cases {
		if err := tc.call(); err == nil {
			t.Fatalf("%s: expected error, got nil", tc.name)
		} else if !strings.Contains(err.Error(), "storage:") {
			t.Fatalf("%s: error %q does not look like a storage error", tc.name, err)
		}
	}
}

func TestRejectsEmptyRoot(t *testing.T) {
	if _, err := NewResolver(""); err == nil {
		t.Fatal("expected error for empty root")
	}
}
