package datasets

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"insightflow/api/internal/middleware"
	"insightflow/api/internal/platform/httpx"
)

const uploadNameFieldMax = 4096

var (
	errFileTooLarge  = errors.New("datasets: file exceeds size limit")
	errNoFile        = errors.New("datasets: no file part")
	errQuotaExceeded = errors.New("datasets: workspace quota exceeded")
	errBadMultipart  = errors.New("datasets: malformed multipart body")
)

type streamed struct {
	path   string
	size   int64
	sha256 string
}

// Upload handles POST /workspaces/:workspace_id/datasets (multipart streaming).
// The file is streamed straight to storage while its SHA-256 is computed; nothing
// is buffered whole in memory.
func (h *Handler) Upload(c *gin.Context) {
	principal, ok := middleware.PrincipalFrom(c)
	if !ok {
		httpx.WriteError(c, httpx.Err(http.StatusUnauthorized, "AUTH_REQUIRED", "Autentikasi diperlukan."))
		return
	}
	workspaceID, ok := middleware.WorkspaceID(c)
	if !ok {
		writeNotFound(c)
		return
	}
	if !canEdit(c) {
		httpx.WriteError(c, httpx.Err(http.StatusForbidden, "FORBIDDEN", "Peran kamu tidak dapat mengunggah dataset."))
		return
	}

	// Body cap is the larger format limit plus multipart overhead; the exact
	// per-format limit is enforced while streaming the file part.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.maxBodyBytes())

	mr, err := c.Request.MultipartReader()
	if err != nil {
		writeUploadError(c, errBadMultipart)
		return
	}

	ctx := c.Request.Context()
	datasetID := uuid.New()
	var (
		sourcePath   string
		format       string
		originalName string
		sizeBytes    int64
		checksum     string
		providedName string
		haveFile     bool
	)
	cleanup := func() {
		if sourcePath != "" {
			_ = os.Remove(sourcePath)
		}
	}

	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				cleanup()
				writeUploadError(c, errFileTooLarge)
				return
			}
			cleanup()
			writeUploadError(c, errBadMultipart)
			return
		}
		switch part.FormName() {
		case "name":
			buf, _ := io.ReadAll(io.LimitReader(part, uploadNameFieldMax))
			providedName = strings.TrimSpace(string(buf))
			_ = part.Close()
		case "file":
			if haveFile {
				_ = part.Close()
				continue
			}
			name := part.FileName()
			f, ok := FormatFromFilename(name)
			if !ok {
				_ = part.Close()
				cleanup()
				writeUploadError(c, ErrUnsupportedFormat)
				return
			}
			format = f
			originalName = SanitizeFilename(name)
			res, err := h.streamPart(part, workspaceID, datasetID, format, h.limitBytes(format))
			_ = part.Close()
			if err != nil {
				cleanup()
				writeUploadError(c, err)
				return
			}
			sourcePath, sizeBytes, checksum, haveFile = res.path, res.size, res.sha256, true
		default:
			_ = part.Close()
		}
	}

	if !haveFile {
		writeUploadError(c, errNoFile)
		return
	}

	if format == FormatXLSX {
		if err := CheckXLSXArchive(sourcePath); err != nil {
			cleanup()
			writeUploadError(c, err)
			return
		}
	}

	exceeded, err := h.svc.QuotaExceeded(ctx, workspaceID)
	if err != nil {
		cleanup()
		httpx.WriteError(c, err)
		return
	}
	if exceeded {
		cleanup()
		writeUploadError(c, errQuotaExceeded)
		return
	}

	name := providedName
	if name == "" {
		name = strings.TrimSpace(strings.TrimSuffix(originalName, filepath.Ext(originalName)))
	}

	dataset, err := h.svc.Create(ctx, CreateInput{
		DatasetID:      datasetID,
		WorkspaceID:    workspaceID,
		UserID:         principal.ID,
		Name:           name,
		Format:         format,
		OriginalName:   originalName,
		SizeBytes:      sizeBytes,
		ChecksumSHA256: checksum,
		SourcePath:     sourcePath,
	})
	if err != nil {
		cleanup()
		writeUploadError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, datasetBody(dataset))
}

// streamPart writes a file part to a temp file computing its SHA-256, then
// renames it into the final source location. The engine re-reads it later.
func (h *Handler) streamPart(part *multipart.Part, workspaceID, datasetID uuid.UUID, format string, limit int64) (streamed, error) {
	br := bufio.NewReaderSize(part, 8192)
	head, _ := br.Peek(8192)
	if err := ValidateSignature(format, head); err != nil {
		return streamed{}, err
	}

	resolver := h.svc.Resolver()
	tempPath, err := resolver.TempPath(datasetID.String() + ".upload")
	if err != nil {
		return streamed{}, err
	}
	finalPath, err := resolver.SourcePath(workspaceID.String(), datasetID.String(), 1, format)
	if err != nil {
		return streamed{}, err
	}
	if err := os.MkdirAll(filepath.Dir(tempPath), 0o755); err != nil {
		return streamed{}, err
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return streamed{}, err
	}

	f, err := os.Create(tempPath)
	if err != nil {
		return streamed{}, err
	}
	hasher := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, hasher), io.LimitReader(br, limit+1))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(tempPath)
		return streamed{}, errBadMultipart
	}
	if n > limit {
		_ = os.Remove(tempPath)
		return streamed{}, errFileTooLarge
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		_ = os.Remove(tempPath)
		return streamed{}, err
	}
	return streamed{path: finalPath, size: n, sha256: hex.EncodeToString(hasher.Sum(nil))}, nil
}

func (h *Handler) maxBodyBytes() int64 {
	if h.maxCSV > h.maxXLSX {
		return h.maxCSV + (1 << 20)
	}
	return h.maxXLSX + (1 << 20)
}

func (h *Handler) limitBytes(format string) int64 {
	if format == FormatXLSX {
		return h.maxXLSX
	}
	return h.maxCSV
}

func canEdit(c *gin.Context) bool {
	v, ok := c.Get(middleware.WorkspaceRoleKey)
	if !ok {
		return false
	}
	role, _ := v.(string)
	return role == "owner" || role == "editor"
}

func writeUploadError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrUnsupportedFormat):
		httpx.WriteError(c, httpx.Err(http.StatusUnsupportedMediaType, "DATASET_UNSUPPORTED_FORMAT", "Format tidak didukung. Unggah file CSV atau XLSX."))
	case errors.Is(err, ErrInvalidFormat):
		httpx.WriteError(c, httpx.Err(http.StatusUnprocessableEntity, "DATASET_INVALID_FORMAT", "File tidak valid atau rusak."))
	case errors.Is(err, errFileTooLarge):
		httpx.WriteError(c, httpx.Err(http.StatusRequestEntityTooLarge, "DATASET_FILE_TOO_LARGE", "Ukuran file melebihi batas."))
	case errors.Is(err, errNoFile):
		httpx.WriteError(c, httpx.Err(http.StatusUnprocessableEntity, "DATASET_FILE_REQUIRED", "Tidak ada file yang diunggah."))
	case errors.Is(err, errQuotaExceeded):
		httpx.WriteError(c, httpx.Err(http.StatusConflict, "DATASET_QUOTA_EXCEEDED", "Kuota dataset ruang kerja tercapai."))
	case errors.Is(err, ErrNameTaken):
		httpx.WriteError(c, httpx.Err(http.StatusConflict, "DATASET_NAME_TAKEN", "Nama dataset sudah dipakai di ruang kerja ini."))
	case errors.Is(err, errBadMultipart):
		httpx.WriteError(c, httpx.Err(http.StatusBadRequest, "DATASET_UPLOAD_INVALID", "Permintaan unggah tidak valid."))
	default:
		httpx.WriteError(c, err)
	}
}
