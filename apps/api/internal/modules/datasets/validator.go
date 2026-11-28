package datasets

import (
	"archive/zip"
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"unicode"
)

// Supported upload formats (PRD 27.1).
const (
	FormatCSV  = "csv"
	FormatXLSX = "xlsx"
)

var (
	// ErrUnsupportedFormat maps to 415 DATASET_UNSUPPORTED_FORMAT.
	ErrUnsupportedFormat = errors.New("datasets: unsupported format")
	// ErrInvalidFormat maps to 422 DATASET_INVALID_FORMAT.
	ErrInvalidFormat = errors.New("datasets: invalid or corrupt file")
)

// FormatFromFilename returns the canonical format for a supported extension.
func FormatFromFilename(name string) (string, bool) {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(name), ".")) {
	case FormatCSV:
		return FormatCSV, true
	case FormatXLSX:
		return FormatXLSX, true
	default:
		return "", false
	}
}

// SanitizeFilename keeps only the base name, strips control characters, and
// bounds the length. User file names are metadata only: they never become paths.
func SanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" {
		return "dataset"
	}
	if len(name) > 255 {
		name = name[:255]
	}
	return name
}

// ValidateSignature checks the leading bytes of a part before it is stored.
// CSV: no NUL bytes. XLSX: ZIP local file header.
func ValidateSignature(format string, head []byte) error {
	switch format {
	case FormatXLSX:
		if len(head) < 4 || !bytes.Equal(head[:4], []byte{'P', 'K', 3, 4}) {
			return ErrInvalidFormat
		}
	case FormatCSV:
		if bytes.IndexByte(head, 0) >= 0 {
			return ErrInvalidFormat
		}
	default:
		return ErrUnsupportedFormat
	}
	return nil
}

const (
	maxArchiveBytes   = 400 << 20 // 400 MB uncompressed ceiling (PRD 37.1)
	maxArchiveRatio   = 20        // decompression ratio ceiling
	maxArchiveEntries = 10_000
)

// CheckXLSXArchive rejects zip bombs and tiny archive anomalies before the
// engine parses the workbook.
func CheckXLSXArchive(path string) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return ErrInvalidFormat
	}
	defer func() { _ = zr.Close() }()

	if len(zr.File) > maxArchiveEntries {
		return ErrInvalidFormat
	}
	var total uint64
	for _, f := range zr.File {
		total += f.UncompressedSize64
		if total > maxArchiveBytes {
			return ErrInvalidFormat
		}
		if f.CompressedSize64 > 0 && f.UncompressedSize64/f.CompressedSize64 > maxArchiveRatio {
			return ErrInvalidFormat
		}
	}
	return nil
}
