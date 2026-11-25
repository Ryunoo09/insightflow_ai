// Package storage resolves server-side storage paths for dataset files.
// Callers never supply filesystem paths: only opaque IDs, which are validated
// before being joined under the resolved root.
package storage

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var segmentPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Resolver builds all dataset paths under a single root directory.
type Resolver struct {
	root string
}

// NewResolver returns a resolver rooted at root, resolved to an absolute path.
func NewResolver(root string) (*Resolver, error) {
	if root == "" {
		return nil, fmt.Errorf("storage: root must not be empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("storage: resolve root: %w", err)
	}
	return &Resolver{root: abs}, nil
}

// Root returns the absolute storage root.
func (r *Resolver) Root() string { return r.root }

// SourcePath is the original upload: sources/{ws}/{dataset}/{version}/source.{ext}
func (r *Resolver) SourcePath(workspaceID, datasetID string, versionNo int, ext string) (string, error) {
	suffix := strings.TrimPrefix(strings.ToLower(ext), ".")
	if suffix == "" {
		return "", fmt.Errorf("storage: extension must not be empty")
	}
	return r.build("sources", workspaceID, datasetID, strconv.Itoa(versionNo), "source."+suffix)
}

// ParquetPath is the normalized table: parquet/{ws}/{dataset}/{version}/data.parquet
func (r *Resolver) ParquetPath(workspaceID, datasetID string, versionNo int) (string, error) {
	return r.build("parquet", workspaceID, datasetID, strconv.Itoa(versionNo), "data.parquet")
}

// TempPath is a scratch file: temp/{name}
func (r *Resolver) TempPath(name string) (string, error) {
	return r.build("temp", name)
}

func (r *Resolver) build(parts ...string) (string, error) {
	for _, p := range parts {
		if !isSafeSegment(p) {
			return "", fmt.Errorf("storage: unsafe path segment %q", p)
		}
	}

	joined := filepath.Join(append([]string{r.root}, parts...)...)
	rel, err := filepath.Rel(r.root, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("storage: path escapes root: %q", joined)
	}
	return joined, nil
}

func isSafeSegment(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	if strings.ContainsAny(s, `/\`) {
		return false
	}
	return segmentPattern.MatchString(s)
}
