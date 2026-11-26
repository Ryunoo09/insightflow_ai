// Package httpx provides the standard JSON error envelope and request metadata
// helpers shared by every HTTP handler.
package httpx

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

// Gin context keys for request metadata. Only correlation and routing metadata
// is ever logged; credentials, headers, query, and bodies never are.
const (
	RequestIDKey = "request_id"
	ErrorCodeKey = "error_code"
)

// APIError is a safe, client-facing error. Internal details never leak because
// callers construct these with a fixed code and a friendly message.
type APIError struct {
	Status  int
	Code    string
	Message string
	Details any
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

// Err builds an APIError.
func Err(status int, code, message string) *APIError {
	return &APIError{Status: status, Code: code, Message: message}
}

// WithDetails returns a copy carrying machine-readable details.
func (e *APIError) WithDetails(details any) *APIError {
	clone := *e
	clone.Details = details
	return &clone
}

// RequestID returns the current request identifier.
func RequestID(c *gin.Context) string {
	v, _ := c.Get(RequestIDKey)
	s, _ := v.(string)
	return s
}

// ErrorCode returns the stable error code recorded for this request, if any.
func ErrorCode(c *gin.Context) string {
	v, _ := c.Get(ErrorCodeKey)
	s, _ := v.(string)
	return s
}

type errorEnvelope struct {
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Details   any    `json:"details,omitempty"`
	RequestID string `json:"request_id"`
}

// WriteError maps err onto the standard envelope. Errors that are not APIError
// become an opaque 500 so stack traces and driver messages never reach clients;
// the underlying error is logged server-side instead (redacted from the client).
func WriteError(c *gin.Context, err error) {
	apiErr := &APIError{
		Status:  http.StatusInternalServerError,
		Code:    "INTERNAL_ERROR",
		Message: "Terjadi kesalahan pada server.",
	}
	var mapped *APIError
	if errors.As(err, &mapped) {
		apiErr = mapped
	} else {
		// Unexpected failure: keep the detail in the log, never in the response.
		slog.Error("unhandled error", "request_id", RequestID(c), "error", err)
	}
	c.Set(ErrorCodeKey, apiErr.Code)
	c.AbortWithStatusJSON(apiErr.Status, errorEnvelope{Error: errorPayload{
		Code:      apiErr.Code,
		Message:   apiErr.Message,
		Details:   apiErr.Details,
		RequestID: RequestID(c),
	}})
}

// ValidationDetails is the shape of a field-level validation failure.
type ValidationDetails struct {
	Field string `json:"field"`
	Issue string `json:"issue"`
}

// ValidationFailed reports a 422 with per-field details.
func ValidationFailed(c *gin.Context, details ...ValidationDetails) {
	WriteError(c, Err(http.StatusUnprocessableEntity, "VALIDATION_FAILED", "Data yang dikirim tidak valid.").WithDetails(details))
}

// WriteBindError converts a gin binding error into a 422 envelope with the
// failing field names. Non-validation errors fall back to a generic 422.
func WriteBindError(c *gin.Context, err error) {
	var verrs validator.ValidationErrors
	if errors.As(err, &verrs) {
		details := make([]ValidationDetails, 0, len(verrs))
		for _, fe := range verrs {
			details = append(details, ValidationDetails{Field: fe.Field(), Issue: fe.Tag()})
		}
		ValidationFailed(c, details...)
		return
	}
	ValidationFailed(c)
}
