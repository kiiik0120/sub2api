//go:build unit

package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestArkMultimodalHandlerSchedulesOnlyDedicatedAccountsAndRecordsUsage(t *testing.T) {
	for _, groupPlatform := range []string{service.PlatformArkMultimodal, service.PlatformComposite} {
		t.Run(groupPlatform, func(t *testing.T) {
			accounts := []service.Account{
				{ID: 1, Platform: service.PlatformOpenAI},
				{ID: 2, Platform: service.PlatformArkMultimodal},
			}
			for i := range accounts {
				accounts[i].Type = service.AccountTypeAPIKey
				accounts[i].Status = service.StatusActive
				accounts[i].Schedulable = true
				accounts[i].Concurrency = 10
				accounts[i].GroupIDs = []int64{24}
				accounts[i].Credentials = map[string]any{"api_key": "test-ark-key", "base_url": service.DefaultArkMultimodalBaseURL}
			}
			repo := openAIImagesFailoverAccountRepo{accounts: accounts}
			slots := &grokMediaSlotsCache{accounts: map[string]int64{}, users: map[string]int64{}}
			concurrency := service.NewConcurrencyService(slots)
			usage := &compositeMediaUsageRepo{logs: map[string]*service.UsageLog{}}
			upstream := &grokMediaSlotUpstream{call: func(req *http.Request, accountID int64) (*http.Response, error) {
				require.Equal(t, int64(2), accountID, "generic OpenAI account must not handle the native endpoint")
				require.Equal(t, "/api/v3/embeddings/multimodal", req.URL.Path)
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"native-request","model":"doubao-embedding-vision-251215","data":{"embedding":[0.1,0.2]},"usage":{"prompt_tokens":100,"prompt_tokens_details":{"text_tokens":60,"image_tokens":40}}}`))}, nil
			}}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			pricing := &service.BillingService{}
			gateway := service.NewOpenAIGatewayService(repo, usage, nil, nil, nil, nil, nil, cfg, nil, concurrency, pricing, nil, nil, upstream, service.NewDeferredService(repo, nil, time.Hour), nil, nil, service.NewModelPricingResolver(nil, pricing), nil, nil, nil, nil)
			billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billing.Stop)
			h := NewOpenAIGatewayHandler(gateway, concurrency, billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
			c, w := grokMediaSlotContext(context.Background(), true)
			c.Request = httptest.NewRequest(http.MethodPost, EndpointArkMultimodalEmbeddings, strings.NewReader(`{"model":"doubao-embedding-vision-251215","input":[{"type":"text","text":"hello"},{"type":"image_url","image_url":{"url":"https://example.test/a.png"}}]}`))
			c.Request = c.Request.WithContext(service.WithResolvedTargetPlatform(c.Request.Context(), service.PlatformArkMultimodal))
			key, _ := middleware.GetAPIKeyFromContext(c)
			key.Group.Platform = groupPlatform
			key.Group.RateMultiplier = 2
			textPrice, imagePrice := 0.1e-6, 0.2e-6
			key.Group.ModelPricing = []service.ChannelModelPricing{{Models: []string{service.ArkMultimodalEmbeddingModel}, BillingMode: service.BillingModeToken, InputPrice: &textPrice, ImageInputPrice: &imagePrice}}
			h.ArkMultimodalEmbeddings(c)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), `"data":{"embedding"`)
			require.Equal(t, groupPlatform, key.Group.Platform)
			slots.assertReleased(t)
			require.Len(t, usage.logs, 1)
			for _, log := range usage.logs {
				require.Equal(t, 100, log.InputTokens)
				require.Equal(t, 40, log.ImageInputTokens)
				require.InDelta(t, (60*textPrice+40*imagePrice)*2, log.ActualCost, 1e-12)
				require.Equal(t, int64(2), log.AccountID)
			}
		})
	}
}
