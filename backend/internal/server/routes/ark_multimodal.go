package routes

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Reusing the scheduler's gateway family does not enable text, image or video
// APIs for this dedicated platform. Existing platforms pass through unchanged.
func arkMultimodalPlatformScope() gin.HandlerFunc {
	return withArkMultimodalPlatformScope(func(c *gin.Context) { c.Next() })
}

func scopeArkMultimodalRoute(method, path string, next gin.HandlerFunc) gin.HandlerFunc {
	// The model list already dispatches this platform through its own handler.
	// Keep the exact shared handler identity for the existing /models aliases.
	if method == http.MethodGet && path == "/models" {
		return next
	}
	return withArkMultimodalPlatformScope(next)
}

func withArkMultimodalPlatformScope(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if getGroupPlatform(c) != service.PlatformArkMultimodal {
			next(c)
			return
		}
		path := c.Request.URL.Path
		if c.Request.Method == http.MethodPost && strings.HasSuffix(strings.TrimRight(path, "/"), "/embeddings/multimodal") ||
			c.Request.Method == http.MethodGet && (path == "/v1/models" || path == "/models") {
			next(c)
			return
		}
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": gin.H{
			"type": "not_found_error", "message": "This platform supports only Ark multimodal embeddings",
		}})
	}
}

func withArkMultimodalModels(next gin.HandlerFunc, h *handler.OpenAIGatewayHandler) gin.HandlerFunc {
	return func(c *gin.Context) {
		if getGroupPlatform(c) == service.PlatformArkMultimodal {
			h.ArkMultimodalModels(c)
			return
		}
		next(c)
	}
}

// All native aliases reuse the existing authenticated root-route registrar.
// Only this extension needs to know its endpoint names and context override.
func registerArkMultimodalRoutes(
	register func(string, string, gin.HandlerFunc, gin.HandlerFunc),
	limit gin.HandlerFunc,
	h *handler.OpenAIGatewayHandler,
) {
	for _, prefix := range []string{"/v1", "/api/v3"} {
		register(http.MethodPost, prefix+"/embeddings/multimodal", limit, func(c *gin.Context) {
			handler.ArkMultimodalEndpointContext(c)
			if !c.IsAborted() {
				h.ArkMultimodalEmbeddings(c)
			}
		})
	}
}
