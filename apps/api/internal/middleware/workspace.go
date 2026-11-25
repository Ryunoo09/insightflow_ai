package middleware

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"insightflow/api/internal/platform/httpx"
)

// WorkspaceLookup resolves the caller's role in a workspace.
type WorkspaceLookup interface {
	MemberRole(ctx context.Context, workspaceID, userID uuid.UUID) (string, error)
}

// Context keys set by WorkspaceMember.
const (
	WorkspaceIDKey   = "workspace_id"
	WorkspaceRoleKey = "workspace_role"
)

// WorkspaceMember ensures the caller is a member of the workspace in the path.
// Non-members receive the same 404 as a missing workspace, so tenant boundaries
// cannot be probed.
func WorkspaceMember(lookup WorkspaceLookup) gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, ok := PrincipalFrom(c)
		if !ok {
			abortUnauthenticated(c)
			return
		}
		workspaceID, err := uuid.Parse(c.Param("workspace_id"))
		if err != nil {
			abortWorkspaceNotFound(c)
			return
		}
		role, err := lookup.MemberRole(c.Request.Context(), workspaceID, principal.ID)
		if err != nil {
			abortWorkspaceNotFound(c)
			return
		}
		c.Set(WorkspaceIDKey, workspaceID)
		c.Set(WorkspaceRoleKey, role)
		c.Next()
	}
}

// WorkspaceID returns the workspace id validated by WorkspaceMember.
func WorkspaceID(c *gin.Context) (uuid.UUID, bool) {
	v, ok := c.Get(WorkspaceIDKey)
	if !ok {
		return uuid.Nil, false
	}
	id, ok := v.(uuid.UUID)
	return id, ok
}

func abortWorkspaceNotFound(c *gin.Context) {
	c.Abort()
	httpx.WriteError(c, httpx.Err(http.StatusNotFound, "NOT_FOUND", "Resource tidak ditemukan."))
}
