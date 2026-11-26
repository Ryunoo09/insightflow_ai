package datasets

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"insightflow/api/internal/middleware"
	"insightflow/api/internal/modules/users"
	"insightflow/api/internal/platform/dbgen"
	"insightflow/api/internal/platform/pgconv"
)

// uploadHarness wires the upload handler behind a stub auth middleware that
// injects a principal and workspace role, so the HTTP layer can be exercised.
func uploadHarness(t *testing.T, maxCSV, maxXLSX int64) (*Handler, *Service, *dbgen.Queries, uuid.UUID, uuid.UUID, *gin.Engine) {
	t.Helper()
	svc, q, wsID, userID := testDeps(t)
	h := NewHandler(svc, nil, maxCSV, maxXLSX)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/ws/:workspace_id/datasets", func(c *gin.Context) {
		c.Set("auth_principal", users.Principal{ID: userID, Email: "t@example.com"})
		c.Set(middleware.WorkspaceIDKey, wsID)
		c.Set(middleware.WorkspaceRoleKey, "owner")
		c.Next()
	}, h.Upload)
	return h, svc, q, wsID, userID, r
}

func multipartBody(t *testing.T, name, filename string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	if name != "" {
		if err := w.WriteField("name", name); err != nil {
			t.Fatalf("write field: %v", err)
		}
	}
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create file part: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return buf, w.FormDataContentType()
}

func doUpload(r *gin.Engine, body *bytes.Buffer, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/ws/x/datasets", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestUploadCSVCreatesDatasetAndStoresFile(t *testing.T) {
	_, svc, q, wsID, _, r := uploadHarness(t, 1<<20, 1<<20)
	data := []byte("a,b\n1,2\n")
	body, ct := multipartBody(t, "Penjualan", "penjualan.csv", data)

	rec := doUpload(r, body, ct)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	id, err := uuid.Parse(resp["id"].(string))
	if err != nil {
		t.Fatalf("bad id: %v", err)
	}

	source, err := svc.Resolver().SourcePath(wsID.String(), id.String(), 1, FormatCSV)
	if err != nil {
		t.Fatalf("source path: %v", err)
	}
	got, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("source file missing: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("stored bytes differ from upload")
	}

	ver, err := q.GetDatasetVersionByNo(context.Background(), dbgen.GetDatasetVersionByNoParams{DatasetID: pgconv.UUID(id), VersionNo: 1})
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	sum := sha256.Sum256(data)
	if ver.ChecksumSha256.String != hex.EncodeToString(sum[:]) {
		t.Fatalf("checksum = %q, want %x", ver.ChecksumSha256.String, sum)
	}
}

func TestUploadXLSXAccepted(t *testing.T) {
	_, _, _, _, _, r := uploadHarness(t, 1<<20, 1<<20)
	body, ct := multipartBody(t, "Buku", "buku.xlsx", xlsxBytes(t))
	rec := doUpload(r, body, ct)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
}

func TestUploadTooLargeLeavesNoFileOrRow(t *testing.T) {
	_, svc, q, wsID, _, r := uploadHarness(t, 10, 10)
	before, _ := q.CountWorkspaceDatasets(context.Background(), pgconv.UUID(wsID))
	body, ct := multipartBody(t, "Big", "big.csv", []byte("0123456789ABCDEF"))

	rec := doUpload(r, body, ct)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413: %s", rec.Code, rec.Body.String())
	}
	after, _ := q.CountWorkspaceDatasets(context.Background(), pgconv.UUID(wsID))
	if after != before {
		t.Fatalf("rejected upload left a row: before=%d after=%d", before, after)
	}
	if files := countFiles(t, svc.Resolver().Root()); files != 0 {
		t.Fatalf("rejected upload left %d file(s) behind", files)
	}
}

func TestUploadRejectsZipBomb(t *testing.T) {
	_, _, _, _, _, r := uploadHarness(t, 1<<20, 1<<20)
	body, ct := multipartBody(t, "Bomb", "bomb.xlsx", zipBombBytes(t))
	rec := doUpload(r, body, ct)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

func TestUploadRejectsNULSignature(t *testing.T) {
	_, _, _, _, _, r := uploadHarness(t, 1<<20, 1<<20)
	body, ct := multipartBody(t, "Bad", "bad.csv", []byte("a,b\x00c\n"))
	rec := doUpload(r, body, ct)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

func TestUploadSanitizesTraversalFilename(t *testing.T) {
	_, svc, q, wsID, _, r := uploadHarness(t, 1<<20, 1<<20)
	body, ct := multipartBody(t, "", "../evil.csv", []byte("a\n1\n"))
	rec := doUpload(r, body, ct)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	id, _ := uuid.Parse(resp["id"].(string))

	ver, err := q.GetDatasetVersionByNo(context.Background(), dbgen.GetDatasetVersionByNoParams{DatasetID: pgconv.UUID(id), VersionNo: 1})
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if strings.ContainsAny(ver.OriginalName, "/\\\x00") {
		t.Fatalf("original name not sanitized: %q", ver.OriginalName)
	}
	source, err := svc.Resolver().SourcePath(wsID.String(), id.String(), 1, FormatCSV)
	if err != nil {
		t.Fatalf("source path: %v", err)
	}
	if !strings.HasPrefix(source, svc.Resolver().Root()) {
		t.Fatalf("source escaped the storage root: %s", source)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(svc.Resolver().Root()), "evil.csv")); !os.IsNotExist(err) {
		t.Fatalf("traversal file was written outside the root")
	}
}

func xlsxBytes(t *testing.T) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	f, err := zw.Create("xl/workbook.xml")
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := f.Write([]byte("<workbook/>")); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// zipBombBytes is a valid ZIP whose highly compressible payload trips the
// decompression-ratio guard (CheckXLSXArchive) before the engine parses it.
func zipBombBytes(t *testing.T) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	f, err := zw.Create("xl/sheet1.xml")
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := f.Write(make([]byte, 64*1024)); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func countFiles(t *testing.T, root string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk storage: %v", err)
	}
	return n
}

func TestRenameForbiddenForViewer(t *testing.T) {
	svc, q, wsID, ownerID := testDeps(t)
	id := mustCreate(t, svc, wsID, ownerID, "Milik")

	viewer, err := q.CreateUser(context.Background(), dbgen.CreateUserParams{
		Email: uuid.NewString() + "@example.com", FullName: "Viewer", Status: "active",
	})
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	if _, err := q.AddWorkspaceMember(context.Background(), dbgen.AddWorkspaceMemberParams{
		WorkspaceID: pgconv.UUID(wsID), UserID: viewer.ID, Role: "viewer",
	}); err != nil {
		t.Fatalf("add viewer: %v", err)
	}

	h := NewHandler(svc, nil, 1<<20, 1<<20)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.PATCH("/datasets/:dataset_id", func(c *gin.Context) {
		c.Set("auth_principal", users.Principal{ID: uuid.UUID(viewer.ID.Bytes)})
		c.Next()
	}, h.Rename)

	req := httptest.NewRequest(http.MethodPatch, "/datasets/"+id.String(), strings.NewReader(`{"name":"Baru"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
}