package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// Fork extension derived from the upstream forwarding scaffold.
// No conversion to or from the OpenAI embeddings contract takes place here.
func (s *OpenAIGatewayService) ForwardArkMultimodalEmbeddings(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	defaultMappedModel string,
) (*OpenAIForwardResult, error) {
	if account == nil || account.Platform != PlatformArkMultimodal || account.Type != AccountTypeAPIKey {
		return nil, fmt.Errorf("Ark multimodal requires an API-key account on its dedicated platform")
	}
	if err := ValidateArkMultimodalEmbeddingsRequest(body); err != nil {
		writeOpenAIEmbeddingsError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}
	startTime := time.Now()

	originalModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if originalModel == "" {
		writeOpenAIEmbeddingsError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}

	billingModel := resolveOpenAIForwardModel(account, originalModel, defaultMappedModel)
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)
	SetOpsUpstreamModel(c, upstreamModel)
	upstreamBody := body
	if upstreamModel != originalModel {
		upstreamBody = ReplaceModelInBody(body, upstreamModel)
	}

	logger.L().Debug("ark multimodal embeddings: forwarding",
		zap.Int64("account_id", account.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
	)

	apiKey := strings.TrimSpace(account.GetCredential("api_key"))
	if apiKey == "" {
		return nil, fmt.Errorf("account %d missing api_key", account.ID)
	}
	baseURL, err := arkMultimodalBaseURL(account)
	if err != nil {
		return nil, err
	}
	targetURL := baseURL + "/embeddings/multimodal"
	SetActualOpenAIUpstreamEndpoint(c, "/api/v3/embeddings/multimodal")

	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	defer releaseUpstreamCtx()
	upstreamReq, err := http.NewRequestWithContext(upstreamCtx, http.MethodPost, targetURL, bytes.NewReader(upstreamBody))
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}
	upstreamReq = upstreamReq.WithContext(WithHTTPUpstreamProfile(upstreamReq.Context(), HTTPUpstreamProfileOpenAI))
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Authorization", "Bearer "+apiKey)
	upstreamReq.Header.Set("Accept", "application/json")
	for key, values := range c.Request.Header {
		lowerKey := strings.ToLower(key)
		if openaiCCRawAllowedHeaders[lowerKey] {
			for _, v := range values {
				upstreamReq.Header.Add(key, v)
			}
		}
	}
	if customUA := account.GetOpenAIUserAgent(); customUA != "" {
		upstreamReq.Header.Set("user-agent", customUA)
	}

	// 账号级请求头覆写（仅 openai api_key 账号启用时生效）
	account.ApplyHeaderOverrides(upstreamReq.Header)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.doOpenAIUpstream(upstreamReq, proxyURL, account)
	if err != nil {
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		setOpsUpstreamError(c, 0, safeErr, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			ProxyID:            opsUpstreamProxyID(account),
			ProxyName:          opsUpstreamProxyName(account),
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: 0,
			Kind:               "request_error",
			Message:            safeErr,
		})
		writeOpenAIEmbeddingsError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
		return nil, fmt.Errorf("upstream request failed: %s", safeErr)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody := s.readUpstreamErrorBody(resp)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))

		upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
		upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
		if s.shouldFailoverOpenAIUpstreamResponse(account, resp.StatusCode, upstreamMsg, respBody) {
			upstreamDetail := ""
			if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
				maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
				if maxBytes <= 0 {
					maxBytes = 2048
				}
				upstreamDetail = truncateString(string(respBody), maxBytes)
			}
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				ProxyID:            opsUpstreamProxyID(account),
				ProxyName:          opsUpstreamProxyName(account),
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.Header.Get("x-request-id"),
				Kind:               "failover",
				Message:            upstreamMsg,
				Detail:             upstreamDetail,
			})
			shouldDisable := s.handleOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, upstreamModel)
			retryableOnSameAccount := !shouldDisable && account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode)
			if account.IsOpenAIOAuth() && resp.StatusCode == http.StatusTooManyRequests {
				return nil, s.newOpenAIAccountFailoverError(account, resp.StatusCode, resp.Header, respBody, upstreamMsg, shouldDisable, retryableOnSameAccount)
			}
			if isOpenAIHTTPUpstreamAccessStateError(resp.StatusCode, upstreamMsg, respBody) {
				return nil, newOpenAIUpstreamFailoverError(resp.StatusCode, resp.Header, respBody, upstreamMsg, retryableOnSameAccount)
			}
			return nil, &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBody, RetryableOnSameAccount: retryableOnSameAccount}
		}
		writeOpenAIEmbeddingsUpstreamResponse(c, resp, respBody, s.responseHeaderFilter)
		return nil, fmt.Errorf("upstream returned status %d", resp.StatusCode)
	}

	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		if !errors.Is(err, ErrUpstreamResponseBodyTooLarge) {
			writeOpenAIEmbeddingsError(c, http.StatusBadGateway, "api_error", "Failed to read upstream response")
		}
		return nil, fmt.Errorf("read upstream body: %w", err)
	}

	if !gjson.ValidBytes(respBody) || !gjson.GetBytes(respBody, "data.embedding").IsArray() || len(gjson.GetBytes(respBody, "data.embedding").Array()) == 0 {
		writeOpenAIEmbeddingsError(c, http.StatusBadGateway, "upstream_error", "Ark returned an invalid multimodal embedding response")
		return nil, fmt.Errorf("Ark response is missing data.embedding")
	}
	usage := extractOpenAIEmbeddingsUsage(respBody)
	if gjson.GetBytes(respBody, "usage.prompt_tokens").Type != gjson.Number || usage.InputTokens <= 0 ||
		usage.ImageInputTokens > usage.InputTokens {
		writeOpenAIEmbeddingsError(c, http.StatusBadGateway, "upstream_error", "Ark returned no valid multimodal token usage")
		return nil, fmt.Errorf("Ark response is missing valid prompt token usage")
	}
	writeOpenAIEmbeddingsUpstreamResponse(c, resp, respBody, s.responseHeaderFilter)

	return &OpenAIForwardResult{
		RequestID:       firstNonEmptyString(resp.Header.Get("x-request-id"), resp.Header.Get("request-id"), gjson.GetBytes(respBody, "id").String()),
		UpstreamHeaders: resp.Header,
		Usage:           usage,
		Model:           originalModel,
		BillingModel:    billingModel,
		UpstreamModel:   upstreamModel,
		Stream:          false,
		Duration:        time.Since(startTime),
	}, nil
}

const PlatformArkMultimodal = domain.PlatformArkMultimodal

const ArkMultimodalEmbeddingModel = "doubao-embedding-vision-251215"
const DefaultArkMultimodalBaseURL = "https://ark.cn-beijing.volces.com/api/v3"

// ValidateArkMultimodalEmbeddingsRequest accepts Ark's content-item array,
// including mixed modalities. It deliberately does not reinterpret it as a batch.
func ValidateArkMultimodalEmbeddingsRequest(body []byte) error {
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return fmt.Errorf("request body must be a JSON object")
	}
	model := gjson.GetBytes(body, "model")
	if model.Type != gjson.String || strings.TrimSpace(model.String()) == "" {
		return fmt.Errorf("model is required")
	}
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() || len(input.Array()) == 0 {
		return fmt.Errorf("input must be a non-empty Ark multimodal content array")
	}
	for _, item := range input.Array() {
		if !item.IsObject() || item.Get("type").Type != gjson.String || strings.TrimSpace(item.Get("type").String()) == "" {
			return fmt.Errorf("each input item must be an object with a type")
		}
	}
	return nil
}

// Read this platform's own credentials, never OpenAI's endpoint defaults.
func arkMultimodalBaseURL(account *Account) (string, error) {
	raw := strings.TrimRight(strings.TrimSpace(account.GetCredential("base_url")), "/")
	if raw == "" {
		raw = DefaultArkMultimodalBaseURL
	}
	base, err := url.Parse(raw)
	if err != nil || base.Scheme != "https" || !strings.EqualFold(base.Host, "ark.cn-beijing.volces.com") || base.Path != "/api/v3" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return "", fmt.Errorf("Ark multimodal base URL must be %s", DefaultArkMultimodalBaseURL)
	}
	return DefaultArkMultimodalBaseURL, nil
}
