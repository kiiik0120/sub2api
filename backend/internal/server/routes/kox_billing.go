package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/koxbilling"
	"github.com/gin-gonic/gin"
)

// RegisterKoxBillingRoutes exposes only service-to-service billing endpoints.
func RegisterKoxBillingRoutes(r *gin.Engine, h *koxbilling.Handler) {
	if h == nil {
		return
	}
	g := r.Group("/internal/v1/kox", h.Authorize())
	g.POST("/api-keys", h.CreateKey)
	g.GET("/api-keys", h.ListKeys)
	g.GET("/api-keys/:api_key_id/credential", h.Credential)
	g.POST("/api-keys/:api_key_id/rotate", h.Rotate)
	g.POST("/api-keys/:api_key_id/disable", h.Disable)
	g.POST("/usage", h.RecordUsage)
	g.GET("/usage", h.Usage)
	g.POST("/outbox/:event_id/replay", h.Replay)
}
