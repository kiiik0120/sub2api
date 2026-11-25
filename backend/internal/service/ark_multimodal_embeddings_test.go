package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func arkMultimodalTestAccount() *Account {
	return &Account{ID: 41, Platform: PlatformArkMultimodal, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"api_key": "test-ark-key", "base_url": DefaultArkMultimodalBaseURL,
		"model_mapping": map[string]any{"my-multimodal-vector": ArkMultimodalEmbeddingModel},
	}}
}

func TestArkMultimodalForwardPreservesNativeContractAndUsage(t *testing.T) {
	body := []byte(`{"model":"my-multimodal-vector","input":[{"type":"text","text":"hello"},{"type":"image_url","image_url":{"url":"https://example.test/a.png"}}],"dimensions":1024}`)
	responseBody := `{"id":"ark-request-1","model":"doubao-embedding-vision-251215","object":"list","data":{"object":"embedding","embedding":[0.1,0.2]},"usage":{"prompt_tokens":100,"prompt_tokens_details":{"text_tokens":60,"image_tokens":40},"total_tokens":100}}`
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(responseBody))}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/embeddings/multimodal", bytes.NewReader(body))
	c.Request.Header.Set("Authorization", "Bearer client-key-must-not-reach-ark")
	result, err := svc.ForwardArkMultimodalEmbeddings(context.Background(), c, arkMultimodalTestAccount(), body, "")
	require.NoError(t, err)
	require.Equal(t, DefaultArkMultimodalBaseURL+"/embeddings/multimodal", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer test-ark-key", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, ArkMultimodalEmbeddingModel, gjson.GetBytes(upstream.lastBody, "model").String())
	require.JSONEq(t, gjson.GetBytes(body, "input").Raw, gjson.GetBytes(upstream.lastBody, "input").Raw)
	require.Equal(t, int64(1024), gjson.GetBytes(upstream.lastBody, "dimensions").Int())
	require.JSONEq(t, responseBody, w.Body.String())
	require.False(t, gjson.Get(w.Body.String(), "data").IsArray())
	require.Equal(t, "ark-request-1", result.RequestID)
	require.Equal(t, 100, result.Usage.InputTokens)
	require.Equal(t, 40, result.Usage.ImageInputTokens)
	require.Zero(t, result.Usage.OutputTokens)
	require.Equal(t, "my-multimodal-vector", result.Model)
	require.Equal(t, ArkMultimodalEmbeddingModel, result.BillingModel)
	require.Empty(t, arkMultimodalTestAccount().GetOpenAIBaseURL())
}

func TestArkMultimodalRejectsOpenAIInputAndWrongAccounts(t *testing.T) {
	for _, body := range []string{`{}`, `[]`, `{"model":"m","input":"text"}`, `{"model":"m","input":["a","b"]}`, `{"model":"m","input":[]}`, `{"model":"m","input":[{}]}`} {
		require.Error(t, ValidateArkMultimodalEmbeddingsRequest([]byte(body)), body)
	}
	valid := []byte(`{"model":"m","input":[{"type":"text","text":"text"}]}`)
	for _, platform := range []string{PlatformOpenAI, PlatformHeyGen, PlatformGrok} {
		account := arkMultimodalTestAccount()
		account.Platform = platform
		svc := &OpenAIGatewayService{}
		_, err := svc.ForwardArkMultimodalEmbeddings(context.Background(), nil, account, valid, "")
		require.Error(t, err)
	}
	for _, bad := range []string{"http://ark.cn-beijing.volces.com/api/v3", "https://example.test/api/v3", DefaultArkMultimodalBaseURL + "?key=bad", DefaultArkMultimodalBaseURL + "/embeddings/multimodal"} {
		account := arkMultimodalTestAccount()
		account.Credentials["base_url"] = bad
		_, err := arkMultimodalBaseURL(account)
		require.Error(t, err)
	}
}

func TestArkMultimodalUpstreamErrorsAreNotBillable(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{
		{400, `{"error":{"code":"InvalidParameter","message":"invalid content"}}`},
		{200, `{"data":[],"usage":{"prompt_tokens":100}}`},
		{200, `{"data":{"embedding":[]}}`},
		{200, `{"data":{"embedding":[0.1]}}`},
		{200, `{"data":{"embedding":[0.1]},"usage":{"prompt_tokens":1,"prompt_tokens_details":{"image_tokens":2}}}`},
	} {
		upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: tc.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(tc.body))}}
		svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		body := []byte(`{"model":"doubao-embedding-vision-251215","input":[{"type":"text","text":"hi"}]}`)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/embeddings/multimodal", bytes.NewReader(body))
		result, err := svc.ForwardArkMultimodalEmbeddings(context.Background(), c, arkMultimodalTestAccount(), body, "")
		require.Error(t, err)
		require.Nil(t, result)
		if tc.status == 400 {
			require.Equal(t, 400, w.Code)
		} else {
			require.Equal(t, 502, w.Code)
		}
	}
}

func TestArkMultimodalRecordsTextAndImagePricesThroughExistingSettlement(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	svc.resolver = NewModelPricingResolver(nil, svc.billingService)
	textPrice, imagePrice := 0.1e-6, 0.2e-6
	group := &Group{ID: 24, Platform: PlatformArkMultimodal, RateMultiplier: 2, ModelPricing: []ChannelModelPricing{{
		Models: []string{ArkMultimodalEmbeddingModel}, BillingMode: BillingModeToken, InputPrice: &textPrice, ImageInputPrice: &imagePrice,
	}}}
	user := &User{ID: 1}
	key := &APIKey{ID: 3, GroupID: &group.ID, Group: group, User: user, Quota: 100}
	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{RequestID: "ark-billing-1", Model: ArkMultimodalEmbeddingModel, BillingModel: ArkMultimodalEmbeddingModel, Usage: OpenAIUsage{InputTokens: 100, ImageInputTokens: 40}},
		APIKey: key, User: user, Account: arkMultimodalTestAccount(), APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{}, QuotaPlatform: PlatformArkMultimodal,
	})
	require.NoError(t, err)
	require.Equal(t, 1, billingRepo.calls)
	require.InDelta(t, (60*textPrice+40*imagePrice)*2, billingRepo.lastCmd.BalanceCost, 1e-12)
	require.Equal(t, 100, usageRepo.lastLog.InputTokens)
	require.Equal(t, 40, usageRepo.lastLog.ImageInputTokens)
}

// Explicit opt-in; credentials are supplied only in the process environment.
// Uses the exact production forwarder, not an independent curl implementation.
func TestArkMultimodalLive(t *testing.T) {
	key := os.Getenv("ARK_MULTIMODAL_TEST_API_KEY")
	if key == "" {
		t.Skip("set ARK_MULTIMODAL_TEST_API_KEY for paid provider verification")
	}
	picture := image.NewRGBA(image.Rect(0, 0, 256, 256))
	draw.Draw(picture, picture.Bounds(), image.NewUniform(color.RGBA{R: 30, G: 120, B: 210, A: 255}), image.Point{}, draw.Src)
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, picture))
	for _, mixed := range []bool{false, true} {
		name := "text"
		if mixed {
			name = "text-and-image"
		}
		t.Run(name, func(t *testing.T) {
			input := []map[string]any{{"type": "text", "text": "native multimodal embedding verification"}}
			if mixed {
				input = append(input, map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())}})
			}
			body, err := json.Marshal(map[string]any{"model": ArkMultimodalEmbeddingModel, "input": input})
			require.NoError(t, err)
			account := arkMultimodalTestAccount()
			account.Credentials["api_key"] = key
			client := &http.Client{Timeout: 40 * time.Second}
			upstream := &arkLiveHTTPUpstream{do: client.Do}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/embeddings/multimodal", bytes.NewReader(body))
			result, err := svc.ForwardArkMultimodalEmbeddings(context.Background(), c, account, body, "")
			require.NoError(t, err, "HTTP %d: %s", w.Code, w.Body.String())
			dimensions := len(gjson.Get(w.Body.String(), "data.embedding").Array())
			require.Positive(t, dimensions)
			require.Positive(t, result.Usage.InputTokens)
			if mixed {
				require.Positive(t, result.Usage.ImageInputTokens)
			}
			t.Logf("native response: dimensions=%d, input_tokens=%d, image_tokens=%d", dimensions, result.Usage.InputTokens, result.Usage.ImageInputTokens)
		})
	}
}

type arkLiveHTTPUpstream struct {
	HTTPUpstream
	do func(*http.Request) (*http.Response, error)
}

func (u *arkLiveHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.do(req)
}
