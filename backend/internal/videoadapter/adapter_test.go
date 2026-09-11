package videoadapter

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveDefaultsToNativeWithoutEnablingAdapterPath(t *testing.T) {
	adapter, enabled, err := Resolve(nil)
	require.NoError(t, err)
	require.False(t, enabled)
	require.Equal(t, KindNative, adapter.Kind())
}

func TestResolveOpenAIVideosFromStringOrObject(t *testing.T) {
	for _, credentials := range []map[string]any{
		{CredentialKey: "openai_videos"},
		{CredentialKey: map[string]any{"type": "openai_videos"}},
	} {
		adapter, enabled, err := Resolve(credentials)
		require.NoError(t, err)
		require.True(t, enabled)
		require.Equal(t, KindOpenAIVideos, adapter.Kind())
	}
}

func TestResolveVolcengineArk(t *testing.T) {
	adapter, enabled, err := Resolve(map[string]any{CredentialKey: "volcengine_ark"})
	require.NoError(t, err)
	require.True(t, enabled)
	require.Equal(t, KindVolcengineArk, adapter.Kind())
}

func TestResolveCtyunMiniMax(t *testing.T) {
	adapter, enabled, err := Resolve(map[string]any{CredentialKey: "ctyun_minimax"})
	require.NoError(t, err)
	require.True(t, enabled)
	require.Equal(t, KindCtyunMiniMax, adapter.Kind())
}

func TestCtyunMiniMaxConvertsAndNormalizesVideoTask(t *testing.T) {
	adapter := ctyunMiniMaxAdapter{}
	prepared, err := adapter.PrepareRequest(OperationCreate, []byte(`{
		"model":"MiniMax-H3","prompt":"paper boat","duration":5,
		"resolution":"768p","aspect_ratio":"9:16"
	}`), "application/json")
	require.NoError(t, err)
	require.Equal(t, "768p", prepared.VideoResolution)
	require.Equal(t, 5, prepared.VideoDurationSeconds)
	require.JSONEq(t, `{
		"model":"MiniMax-H3","content":[{"type":"text","text":"paper boat"}],
		"resolution":"768P","duration":5,"ratio":"9:16"
	}`, string(prepared.Body))

	created, err := adapter.NormalizeResponse(OperationCreate, []byte(`{"task_id":"task_123"}`), "", "")
	require.NoError(t, err)
	require.JSONEq(t, `{"request_id":"task_123","status":"pending"}`, string(created))

	rawStatus := []byte(`{"task":{"id":"task_123","model":"MiniMax-H3","status":"succeeded","duration":5,"content":{"url":"https://media.ctyun.cn/task_123.mp4?signature=x"}}}`)
	normalized, err := adapter.NormalizeResponse(OperationStatus, rawStatus, "task_123", "/v1/videos/task_123/content")
	require.NoError(t, err)
	require.JSONEq(t, `{"request_id":"task_123","model":"MiniMax-H3","status":"done","video":{"url":"/v1/videos/task_123/content","duration":5}}`, string(normalized))
	content, err := adapter.ResolveContent("https://ai.ctaigw.cn/v1", "task_123", rawStatus)
	require.NoError(t, err)
	require.Equal(t, "https://media.ctyun.cn/task_123.mp4?signature=x", content.URL)
	require.False(t, content.Authenticated)
}

func TestCtyunMiniMaxValidatesRequestAndAcceptsExternalContentHost(t *testing.T) {
	adapter := ctyunMiniMaxAdapter{}
	_, err := adapter.PrepareRequest(OperationCreate, []byte(`{"model":"MiniMax-H3","prompt":"x","duration":3}`), "application/json")
	require.ErrorContains(t, err, "4 to 15")
	_, err = adapter.PrepareRequest(OperationCreate, []byte(`{"model":"MiniMax-H3","prompt":"x","resolution":"1080p"}`), "application/json")
	require.ErrorContains(t, err, "480P, 768P, or 2K")
	content, err := adapter.ResolveContent("https://ai.ctaigw.cn/v1", "task", []byte(`{"task":{"content":{"url":"https://cdn.example.invalid/video.mp4"}}}`))
	require.NoError(t, err)
	require.Equal(t, "https://cdn.example.invalid/video.mp4", content.URL)
}

func TestCtyunMiniMaxRejectsUnsafeContentURL(t *testing.T) {
	adapter := ctyunMiniMaxAdapter{}
	_, err := adapter.ResolveContent("https://ai.ctaigw.cn/v1", "task", []byte(`{
		"task":{"content":{"url":"http://cdn.example.invalid/video.mp4"}}
	}`))
	require.ErrorContains(t, err, "unsupported video content URL")
}

func TestOpenAIVideosConvertsGrokCreateRequest(t *testing.T) {
	adapter := openAIVideosAdapter{}
	prepared, err := adapter.PrepareRequest(OperationCreate, []byte(`{
		"model":"sora-2-pro","prompt":"waves","duration":8,
		"resolution":"1080p","aspect_ratio":"9:16",
		"image":{"image_url":"https://example.com/ref.png"}
	}`), "application/json")
	require.NoError(t, err)
	require.Equal(t, "application/json", prepared.ContentType)
	require.Equal(t, "1080p", prepared.VideoResolution)
	require.Equal(t, 8, prepared.VideoDurationSeconds)
	require.JSONEq(t, `{
		"model":"sora-2-pro","prompt":"waves","seconds":"8",
		"size":"1024x1792","input_reference":{"image_url":"https://example.com/ref.png"}
	}`, string(prepared.Body))
}

func TestOpenAIVideosPinsGrokDefaultsForCorrectBilling(t *testing.T) {
	adapter := openAIVideosAdapter{}
	prepared, err := adapter.PrepareRequest(OperationCreate, []byte(`{"model":"vendor-video","prompt":"waves"}`), "application/json")
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"vendor-video","prompt":"waves","seconds":"8","size":"1280x720"}`, string(prepared.Body))
	require.Equal(t, "720p", prepared.VideoResolution)
	require.Equal(t, 8, prepared.VideoDurationSeconds)
}

func TestOpenAIVideosNormalizesCompletedStatusToGrok(t *testing.T) {
	adapter := openAIVideosAdapter{}
	body, err := adapter.NormalizeResponse(OperationStatus, []byte(`{
		"id":"video_123","model":"sora-2","status":"completed","seconds":"8"
	}`), "video_123", "https://gateway.test/v1/videos/video_123/content")
	require.NoError(t, err)
	require.JSONEq(t, `{
		"request_id":"video_123","model":"sora-2","status":"done",
		"video":{"url":"https://gateway.test/v1/videos/video_123/content","duration":8}
	}`, string(body))
}

func TestOpenAIVideosNormalizesQueuedAndFailedStatus(t *testing.T) {
	adapter := openAIVideosAdapter{}
	pending, err := adapter.NormalizeResponse(OperationStatus, []byte(`{"id":"v1","status":"in_progress"}`), "v1", "proxy")
	require.NoError(t, err)
	require.JSONEq(t, `{"request_id":"v1","status":"pending"}`, string(pending))

	failed, err := adapter.NormalizeResponse(OperationStatus, []byte(`{"id":"v2","status":"failed","error":{"message":"nope"}}`), "v2", "proxy")
	require.NoError(t, err)
	require.JSONEq(t, `{"request_id":"v2","status":"failed","error":{"message":"nope"}}`, string(failed))
}

func TestBuildURLEscapesOpaqueRequestID(t *testing.T) {
	adapter := openAIVideosAdapter{}
	got, err := adapter.BuildURL("https://vendor.test/v1/", "folder/id", OperationContent)
	require.NoError(t, err)
	require.Equal(t, "https://vendor.test/v1/videos/folder%2Fid/content", got)
}

func TestResolveRejectsUnknownAdapter(t *testing.T) {
	_, _, err := Resolve(map[string]any{CredentialKey: "mystery"})
	require.ErrorContains(t, err, "unsupported video_adapter")
}

func TestVolcengineArkConvertsRetiredSeedanceLiteT2VRequest(t *testing.T) {
	adapter := volcengineArkAdapter{}
	prepared, err := adapter.PrepareRequest(OperationCreate, []byte(`{
		"model":"doubao-seedance-1-0-lite-t2v-250428",
		"prompt":"一只柯基在海边奔跑","duration":5,
		"resolution":"720p","aspect_ratio":"16:9"
	}`), "application/json")
	require.NoError(t, err)
	require.Equal(t, "720p", prepared.VideoResolution)
	require.Equal(t, 5, prepared.VideoDurationSeconds)
	require.JSONEq(t, `{
		"model":"doubao-seedance-1-0-lite-t2v-250428",
		"content":[{"type":"text","text":"一只柯基在海边奔跑 --ratio 16:9 --resolution 720p --duration 5"}]
	}`, string(prepared.Body))
}

func TestVolcengineArkPinsAndValidatesRetiredModelLimits(t *testing.T) {
	adapter := volcengineArkAdapter{}
	prepared, err := adapter.PrepareRequest(OperationCreate, []byte(`{
		"model":"doubao-seedance-1-0-lite-t2v-250428","prompt":"waves"
	}`), "application/json")
	require.NoError(t, err)
	require.Equal(t, "480p", prepared.VideoResolution)
	require.Equal(t, 5, prepared.VideoDurationSeconds)
	require.JSONEq(t, `{
		"model":"doubao-seedance-1-0-lite-t2v-250428",
		"content":[{"type":"text","text":"waves --resolution 480p --duration 5"}]
	}`, string(prepared.Body))

	_, err = adapter.PrepareRequest(OperationCreate, []byte(`{
		"model":"doubao-seedance-1-0-lite-t2v-250428","prompt":"waves","duration":8
	}`), "application/json")
	require.ErrorContains(t, err, "only 5 or 10 second")
	_, err = adapter.PrepareRequest(OperationCreate, []byte(`{
		"model":"doubao-seedance-1-0-lite-t2v-250428","prompt":"waves","resolution":"1080p"
	}`), "application/json")
	require.ErrorContains(t, err, "only 480p or 720p")
}

func TestVolcengineArkNormalizesStatusAndResolvesSignedContent(t *testing.T) {
	adapter := volcengineArkAdapter{}
	raw := []byte(`{
		"id":"task_123","model":"doubao-seedance-1-0-lite-t2v-250428",
		"status":"succeeded","duration":5,
		"content":{"video_url":"https://ark-content.volces.com/video/task_123.mp4?signature=test"}
	}`)
	normalized, err := adapter.NormalizeResponse(OperationStatus, raw, "task_123", "/v1/videos/task_123/content")
	require.NoError(t, err)
	require.JSONEq(t, `{
		"request_id":"task_123","model":"doubao-seedance-1-0-lite-t2v-250428",
		"status":"done","video":{"url":"/v1/videos/task_123/content","duration":5}
	}`, string(normalized))
	content, err := adapter.ResolveContent("https://ark.cn-beijing.volces.com/api/v3", "task_123", raw)
	require.NoError(t, err)
	require.Equal(t, "https://ark-content.volces.com/video/task_123.mp4?signature=test", content.URL)
	require.False(t, content.Authenticated)
}

func TestVolcengineArkAcceptsExternalSignedContentURL(t *testing.T) {
	adapter := volcengineArkAdapter{}
	content, err := adapter.ResolveContent("https://ark.cn-beijing.volces.com/api/v3", "task_123", []byte(`{
		"status":"succeeded","content":{"video_url":"https://attacker.invalid/video.mp4"}
	}`))
	require.NoError(t, err)
	require.Equal(t, "https://attacker.invalid/video.mp4", content.URL)
}
