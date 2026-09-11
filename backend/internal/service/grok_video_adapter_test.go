package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestForwardGrokVideoOpenAIAdapterNormalizesCreate(t *testing.T) {
	t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok-video","prompt":"waves","duration":8,"resolution":"720p","aspect_ratio":"16:9"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	account := &Account{
		ID: 81, Name: "video vendor", Platform: PlatformGrok, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "vendor-key", "base_url": "https://vendor.test/v1",
			"video_adapter": "openai_videos",
			"model_mapping": map[string]any{"grok-video": "vendor-video-v3"},
		},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"video_123","model":"vendor-video-v3","status":"queued"}`)),
	}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}

	result, err := svc.ForwardGrokMedia(context.Background(), c, account, GrokMediaEndpointVideosGenerations, "", body, "application/json")
	require.NoError(t, err)
	require.Equal(t, "https://vendor.test/v1/videos", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer vendor-key", upstream.lastReq.Header.Get("Authorization"))
	require.JSONEq(t, `{"model":"vendor-video-v3","prompt":"waves","seconds":"8","size":"1280x720"}`, string(upstream.lastBody))
	require.JSONEq(t, `{"request_id":"video_123","model":"vendor-video-v3","status":"pending"}`, recorder.Body.String())
	require.Equal(t, "video_123", result.ResponseID)
	require.Equal(t, "grok-video", result.BillingModel)
	require.Equal(t, "vendor-video-v3", result.UpstreamModel)
	require.Equal(t, VideoBillingResolution720P, result.VideoResolution)
	require.Equal(t, 8, result.VideoDurationSeconds)
}

func TestForwardGrokVideoOpenAIAdapterNormalizesStatusForExistingBilling(t *testing.T) {
	t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "https://gateway.test/v1/videos/video_123", nil)
	account := &Account{
		ID: 82, Name: "video vendor", Platform: PlatformGrok, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "vendor-key", "base_url": "https://vendor.test/v1",
			"video_adapter": map[string]any{"type": "openai_videos"},
		},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"video_123","model":"vendor-video-v3","status":"completed","seconds":"12"}`)),
	}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}

	result, err := svc.ForwardGrokMedia(context.Background(), c, account, GrokMediaEndpointVideoStatus, "video_123", nil, "")
	require.NoError(t, err)
	require.Equal(t, "https://vendor.test/v1/videos/video_123", upstream.lastReq.URL.String())
	require.Equal(t, "done", gjson.Get(recorder.Body.String(), "status").String())
	require.Equal(t, "/v1/videos/video_123/content", gjson.Get(recorder.Body.String(), "video.url").String())
	require.Equal(t, int64(12), gjson.Get(recorder.Body.String(), "video.duration").Int())
	require.Equal(t, 1, result.VideoCount)
	require.Equal(t, 12, result.VideoDurationSeconds)
	require.Equal(t, "vendor-video-v3", result.BillingModel)
}

func TestForwardGrokVideoUnknownAdapterFailsBeforeDispatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos/generations", strings.NewReader(`{"model":"grok-video"}`))
	account := &Account{
		Platform: PlatformGrok, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "key", "video_adapter": "unknown"},
	}
	svc := &OpenAIGatewayService{}
	_, err := svc.ForwardGrokMedia(context.Background(), c, account, GrokMediaEndpointVideosGenerations, "", []byte(`{"model":"grok-video"}`), "application/json")
	require.ErrorContains(t, err, "unsupported video_adapter")
}

func TestForwardGrokVideoOpenAIAdapterProxiesContentAndReturnsBillableResult(t *testing.T) {
	t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/video_123/content", nil)
	c.Request.Header.Set("Range", "bytes=0-3")
	account := &Account{
		ID: 83, Name: "video vendor", Platform: PlatformGrok, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "vendor-key", "base_url": "https://vendor.test/v1",
			"video_adapter": "openai_videos",
		},
	}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"video_123","model":"vendor-video-v3","status":"completed","seconds":"8"}`)),
		},
		{
			StatusCode: http.StatusPartialContent,
			Header: http.Header{
				"Content-Type":  []string{"video/mp4"},
				"Content-Range": []string{"bytes 0-3/8"},
			},
			Body: io.NopCloser(strings.NewReader("MP4!")),
		},
	}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}

	result, err := svc.ForwardGrokMedia(context.Background(), c, account, GrokMediaEndpointVideoContent, "video_123", nil, "")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "https://vendor.test/v1/videos/video_123", upstream.requests[0].URL.String())
	require.Equal(t, "https://vendor.test/v1/videos/video_123/content", upstream.requests[1].URL.String())
	require.Equal(t, "bytes=0-3", upstream.requests[1].Header.Get("Range"))
	require.Equal(t, http.StatusPartialContent, recorder.Code)
	require.Equal(t, "MP4!", recorder.Body.String())
	require.Equal(t, "video_123", result.ResponseID)
	require.Equal(t, 1, result.VideoCount)
	require.Equal(t, 8, result.VideoDurationSeconds)
}

func TestForwardGrokVideoVolcengineArkAdapterSupportsSeedanceLiteT2VContract(t *testing.T) {
	t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{
		"model":"grok-imagine-video","prompt":"一只柯基在海边奔跑",
		"duration":5,"resolution":"720p","aspect_ratio":"16:9"
	}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	account := &Account{
		ID: 84, Name: "volcengine ark", Platform: PlatformGrok, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "ark-key", "base_url": "https://ark.cn-beijing.volces.com/api/v3",
			"video_adapter": "volcengine_ark",
			"model_mapping": map[string]any{
				"grok-imagine-video": "doubao-seedance-1-0-lite-t2v-250428",
			},
		},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"ark-rid"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"task_123","status":"queued"}`)),
	}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}

	result, err := svc.ForwardGrokMedia(context.Background(), c, account, GrokMediaEndpointVideosGenerations, "", body, "application/json")
	require.NoError(t, err)
	require.Equal(t, "https://ark.cn-beijing.volces.com/api/v3/contents/generations/tasks", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer ark-key", upstream.lastReq.Header.Get("Authorization"))
	require.JSONEq(t, `{
		"model":"doubao-seedance-1-0-lite-t2v-250428",
		"content":[{"type":"text","text":"一只柯基在海边奔跑 --ratio 16:9 --resolution 720p --duration 5"}]
	}`, string(upstream.lastBody))
	require.JSONEq(t, `{"request_id":"task_123","status":"pending"}`, recorder.Body.String())
	require.Equal(t, "task_123", result.ResponseID)
	require.Equal(t, "grok-imagine-video", result.BillingModel)
	require.Equal(t, "doubao-seedance-1-0-lite-t2v-250428", result.UpstreamModel)
	require.Equal(t, VideoBillingResolution720P, result.VideoResolution)
	require.Equal(t, 5, result.VideoDurationSeconds)
}

func TestForwardGrokVideoVolcengineArkAdapterProxiesSignedContentWithoutCredential(t *testing.T) {
	t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/task_123/content", nil)
	account := &Account{
		ID: 85, Name: "volcengine ark", Platform: PlatformGrok, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "ark-key", "base_url": "https://ark.cn-beijing.volces.com/api/v3",
			"video_adapter": "volcengine_ark",
		},
	}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{
				"id":"task_123","model":"doubao-seedance-1-0-lite-t2v-250428",
				"status":"succeeded","duration":5,
				"content":{"video_url":"https://ark-content.volces.com/video/task_123.mp4?signature=test"}
			}`)),
		},
		{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"video/mp4"}},
			Body: io.NopCloser(strings.NewReader("MP4!")),
		},
	}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}

	result, err := svc.ForwardGrokMedia(context.Background(), c, account, GrokMediaEndpointVideoContent, "task_123", nil, "")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "https://ark.cn-beijing.volces.com/api/v3/contents/generations/tasks/task_123", upstream.requests[0].URL.String())
	require.Equal(t, "Bearer ark-key", upstream.requests[0].Header.Get("Authorization"))
	require.Equal(t, "https://ark-content.volces.com/video/task_123.mp4?signature=test", upstream.requests[1].URL.String())
	require.Empty(t, upstream.requests[1].Header.Get("Authorization"))
	require.Equal(t, "MP4!", recorder.Body.String())
	require.Equal(t, "task_123", result.ResponseID)
	require.Equal(t, 1, result.VideoCount)
	require.Equal(t, 5, result.VideoDurationSeconds)
}
