package koxbilling

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }
func (h *Handler) Authorize() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.service == nil || !h.service.Authorize(c.GetHeader("Authorization")) {
			response.Error(c, http.StatusUnauthorized, "invalid internal billing credentials")
			c.Abort()
			return
		}
		c.Next()
	}
}
func (h *Handler) CreateKey(c *gin.Context) {
	var in CreateKeyInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "invalid request")
		return
	}
	key, plain, err := h.service.CreateKey(c.Request.Context(), in)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	response.Created(c, gin.H{"api_key_id": key.ID, "api_key": plain, "key_fingerprint": key.Fingerprint, "account_id": key.AccountID, "kox_user_id": key.KoxUserID})
}
func (h *Handler) ListKeys(c *gin.Context) {
	keys, err := h.service.ListKeys(c.Request.Context(), strings.TrimSpace(c.Query("account_id")), strings.TrimSpace(c.Query("kox_user_id")))
	if err != nil {
		response.Error(c, 500, "list keys failed")
		return
	}
	response.Success(c, gin.H{"items": keys})
}
func (h *Handler) Credential(c *gin.Context) {
	key, plain, err := h.service.Credential(c.Request.Context(), c.Param("api_key_id"))
	if err == sql.ErrNoRows {
		response.Error(c, 404, "api key not found")
		return
	}
	if err != nil {
		response.Error(c, 500, "load api key credential failed")
		return
	}
	response.Success(c, gin.H{"api_key_id": key.ID, "api_key": plain, "key_fingerprint": key.Fingerprint, "account_id": key.AccountID})
}
func (h *Handler) Rotate(c *gin.Context) {
	key, plain, err := h.service.Rotate(c.Request.Context(), c.Param("api_key_id"))
	if err == sql.ErrNoRows {
		response.Error(c, 404, "api key not found")
		return
	}
	if err != nil {
		response.Error(c, 500, "rotate key failed")
		return
	}
	response.Success(c, gin.H{"api_key_id": key.ID, "api_key": plain, "key_fingerprint": key.Fingerprint, "account_id": key.AccountID})
}
func (h *Handler) Disable(c *gin.Context) {
	err := h.service.Disable(c.Request.Context(), c.Param("api_key_id"))
	if err == sql.ErrNoRows {
		response.Error(c, 404, "api key not found")
		return
	}
	if err != nil {
		response.Error(c, 500, "disable key failed")
		return
	}
	response.Success(c, gin.H{"api_key_id": c.Param("api_key_id"), "status": "disabled"})
}
func (h *Handler) Usage(c *gin.Context) {
	from, err := parseRFC3339(c.Query("from"))
	if err != nil {
		response.BadRequest(c, "from must be RFC3339")
		return
	}
	to, err := parseRFC3339(c.Query("to"))
	if err != nil {
		response.BadRequest(c, "to must be RFC3339")
		return
	}
	requestID, keyID := strings.TrimSpace(c.Query("request_id")), strings.TrimSpace(c.Query("api_key_id"))
	if requestID == "" && keyID == "" {
		response.BadRequest(c, "request_id or api_key_id is required")
		return
	}
	if keyID != "" && (from.IsZero() || to.IsZero()) {
		response.BadRequest(c, "from and to are required with api_key_id")
		return
	}
	items, err := h.service.Usage(c.Request.Context(), requestID, keyID, from, to)
	if err != nil {
		response.Error(c, 500, "query usage failed")
		return
	}
	response.Success(c, gin.H{"items": items, "pending": len(items) == 0 && requestID != ""})
}

// RecordUsage is for the trusted gateway completion path. It intentionally is
// not exposed through the public OpenAI-compatible routes.
func (h *Handler) RecordUsage(c *gin.Context) {
	var in UsageInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "invalid usage record")
		return
	}
	usageLogID, err := h.service.RecordUsage(c.Request.Context(), in)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	response.Created(c, gin.H{"usage_log_id": usageLogID})
}
func (h *Handler) Replay(c *gin.Context) {
	if err := h.service.Replay(c.Request.Context(), c.Param("event_id")); err != nil {
		response.Error(c, 500, "replay failed")
		return
	}
	response.Success(c, gin.H{"event_id": c.Param("event_id"), "status": "pending"})
}
func parseRFC3339(v string) (time.Time, error) {
	if strings.TrimSpace(v) == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, v)
}
