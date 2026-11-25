package routes

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestArkMultimodalScopePassesExistingPlatformsThrough(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformHeyGen, service.PlatformGrok, service.PlatformComposite} {
		t.Run(platform, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			body := `{"model":"existing-model","input":"original text"}`
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
			request := c.Request
			c.Set("api_key", &service.APIKey{Group: &service.Group{Platform: platform}})
			called := false
			withArkMultimodalPlatformScope(func(got *gin.Context) {
				called = true
				require.Same(t, request, got.Request)
				gotBody, err := io.ReadAll(got.Request.Body)
				require.NoError(t, err)
				require.Equal(t, body, string(gotBody))
			})(c)
			require.True(t, called)
			require.False(t, c.IsAborted())
		})
	}
}

func TestArkMultimodalRoutesAreIndependent(t *testing.T) {
	body := `{"model":"doubao-embedding-vision-251215","input":[{"type":"text","text":"hello"}]}`
	for _, platform := range []string{service.PlatformArkMultimodal, service.PlatformComposite, service.PlatformOpenAI, service.PlatformHeyGen} {
		for _, prefix := range []string{"/v1", "/api/v3"} {
			router := newGatewayRoutesTestRouter(platform)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, prefix+"/embeddings/multimodal", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)
			want := http.StatusForbidden
			if platform == service.PlatformArkMultimodal || platform == service.PlatformComposite {
				want = http.StatusUnauthorized
			}
			require.Equal(t, want, w.Code, "%s %s: %s", platform, prefix, w.Body.String())
		}
	}
	for _, path := range []string{"/v1/embeddings", "/embeddings", "/v1/chat/completions", "/chat/completions", "/v1/responses", "/backend-api/codex/responses", "/v1/videos", "/v1/images/generations"} {
		router := newGatewayRoutesTestRouter(service.PlatformArkMultimodal)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		require.Equal(t, http.StatusNotFound, w.Code, "%s: %s", path, w.Body.String())
	}
}

func TestArkMultimodalModelListAndEndpointIdentity(t *testing.T) {
	router := newGatewayRoutesTestRouter(service.PlatformArkMultimodal)
	for _, path := range []string{"/v1/models", "/models"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), service.ArkMultimodalEmbeddingModel)
	}
	for _, prefix := range []string{"/v1", "/api/v3"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, prefix+"/embeddings/multimodal", nil)
		handler.ArkMultimodalEndpointContext(c)
		require.Equal(t, handler.EndpointArkMultimodalEmbeddings, handler.GetInboundEndpoint(c))
	}
	require.Equal(t, handler.EndpointEmbeddings, handler.NormalizeInboundEndpoint("/v1/embeddings"))
}
