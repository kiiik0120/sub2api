package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/videoadapter"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// forwardAdaptedGrokVideo is the only seam between the upstream Grok media
// implementation and optional third-party video protocols. Native accounts
// return handled=false and continue through the unchanged xAI path.
func (s *OpenAIGatewayService) forwardAdaptedGrokVideo(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	endpoint GrokMediaEndpoint,
	requestID string,
	body []byte,
	contentType string,
	startTime time.Time,
) (*OpenAIForwardResult, bool, error) {
	operation, isVideo := videoAdapterOperation(endpoint)
	if !isVideo {
		return nil, false, nil
	}
	adapter, enabled, err := videoadapter.Resolve(account.Credentials)
	if err != nil {
		return nil, true, fmt.Errorf("resolve Grok video adapter: %w", err)
	}
	if !enabled {
		return nil, false, nil
	}
	token, _, err := s.getRequestCredential(ctx, c, account)
	if err != nil {
		return nil, true, err
	}
	if operation == videoadapter.OperationContent {
		result, err := s.forwardAdaptedGrokVideoContent(ctx, c, account, adapter, token, requestID, startTime)
		return result, true, err
	}
	result, err := s.forwardAdaptedGrokVideoJSON(ctx, c, account, adapter, token, endpoint, operation, requestID, body, contentType, startTime)
	return result, true, err
}

func (s *OpenAIGatewayService) forwardAdaptedGrokVideoJSON(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	adapter videoadapter.Adapter,
	token string,
	endpoint GrokMediaEndpoint,
	operation videoadapter.Operation,
	requestID string,
	body []byte,
	contentType string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	var err error
	body, contentType, err = normalizeGrokMediaForwardBody(endpoint, body, contentType)
	if err != nil {
		return nil, err
	}
	requestInfo := ParseGrokMediaRequest(contentType, body)
	upstreamModel := requestInfo.Model
	if endpoint.RequiresRequestBody() && gjson.ValidBytes(body) {
		if mappedModel := strings.TrimSpace(account.GetMappedModel(requestInfo.Model)); mappedModel != "" {
			upstreamModel = mappedModel
		}
		if upstreamModel != requestInfo.Model {
			body, err = sjson.SetBytes(body, "model", upstreamModel)
			if err != nil {
				return nil, fmt.Errorf("rewrite adapted Grok video account mapped model: %w", err)
			}
		}
	}
	body, contentType, err = sanitizeGrokMediaForwardBody(endpoint, body, contentType)
	if err != nil {
		return nil, err
	}
	prepared, err := adapter.PrepareRequest(operation, body, contentType)
	if err != nil {
		return nil, fmt.Errorf("prepare %s video request: %w", adapter.Kind(), err)
	}
	body, contentType = prepared.Body, prepared.ContentType
	if strings.TrimSpace(prepared.VideoResolution) != "" {
		requestInfo.Resolution = NormalizeVideoBillingResolutionOrDefault(prepared.VideoResolution)
	}
	if prepared.VideoDurationSeconds > 0 {
		requestInfo.DurationSeconds = NormalizeVideoBillingDurationSecondsOrDefault(prepared.VideoDurationSeconds)
	}
	targetURL, err := s.adaptedGrokVideoURL(account, adapter, operation, requestID)
	if err != nil {
		return nil, err
	}

	var bodyReader io.Reader
	if endpoint.RequiresRequestBody() {
		bodyReader = bytes.NewReader(body)
	}
	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	defer releaseUpstreamCtx()
	upstreamReq, err := http.NewRequestWithContext(upstreamCtx, endpoint.httpMethod(), targetURL, bodyReader)
	if err != nil {
		return nil, err
	}
	applyAdaptedGrokVideoHeaders(upstreamReq, account, token, contentType, endpoint.RequiresRequestBody())
	resp, err := s.doAdaptedGrokVideoRequest(ctx, c, account, upstreamReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	requestIDHeader := firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id"))
	if resp.StatusCode >= 400 {
		return s.handleGrokMediaErrorResponse(ctx, resp, c, account, requestIDHeader, requestInfo.Model)
	}

	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	proxyURL := ""
	if operation == videoadapter.OperationStatus {
		proxyURL = grokMediaContentProxyURL(c, requestID)
	}
	respBody, err = adapter.NormalizeResponse(operation, respBody, requestID, proxyURL)
	if err != nil {
		return nil, fmt.Errorf("normalize %s video response: %w", adapter.Kind(), err)
	}
	s.updateGrokUsageFromResponse(withGrokTeamRateLimitModel(ctx, requestInfo.Model), account, resp.Header, resp.StatusCode)
	writeGrokMediaResponse(c, resp, respBody, s.responseHeaderFilter)

	usage := grokMediaUsageFromResponse(endpoint, requestInfo, respBody)
	resultModel := requestInfo.Model
	resultBillingModel := requestInfo.Model
	if endpoint == GrokMediaEndpointVideoStatus {
		if model := strings.TrimSpace(usage.Model); model != "" {
			resultModel = model
		}
		if model := strings.TrimSpace(usage.BillingModel); model != "" {
			resultBillingModel = model
		}
	}
	return &OpenAIForwardResult{
		RequestID: requestIDHeader, UpstreamHeaders: resp.Header, ResponseID: usage.ResponseID,
		Usage: usage.Usage, Model: resultModel, BillingModel: resultBillingModel,
		UpstreamModel: upstreamModel, ResponseHeaders: resp.Header.Clone(), Duration: time.Since(startTime),
		VideoCount: usage.VideoCount, VideoResolution: usage.VideoResolution,
		VideoDurationSeconds: usage.VideoDurationSeconds,
	}, nil
}

func (s *OpenAIGatewayService) forwardAdaptedGrokVideoContent(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	adapter videoadapter.Adapter,
	token, requestID string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	defer releaseUpstreamCtx()
	statusURL, err := s.adaptedGrokVideoURL(account, adapter, videoadapter.OperationStatus, requestID)
	if err != nil {
		return nil, err
	}
	statusReq, err := http.NewRequestWithContext(WithHTTPUpstreamRedirectsDisabled(upstreamCtx), http.MethodGet, statusURL, nil)
	if err != nil {
		return nil, err
	}
	applyAdaptedGrokVideoHeaders(statusReq, account, token, "", false)
	statusResp, err := s.doAdaptedGrokVideoRequest(ctx, c, account, statusReq)
	if err != nil {
		return nil, err
	}
	statusRequestID := firstNonEmpty(statusResp.Header.Get("x-request-id"), statusResp.Header.Get("xai-request-id"))
	if statusResp.StatusCode >= 300 {
		defer func() { _ = statusResp.Body.Close() }()
		if statusResp.StatusCode < 400 {
			return nil, fmt.Errorf("adapted Grok video status redirect is not allowed")
		}
		return s.handleGrokMediaErrorResponse(ctx, statusResp, c, account, statusRequestID, "")
	}
	statusBody, err := ReadUpstreamResponseBody(statusResp.Body, s.cfg, c, openAITooLargeError)
	_ = statusResp.Body.Close()
	if err != nil {
		return nil, err
	}
	statusBody, err = adapter.NormalizeResponse(videoadapter.OperationStatus, statusBody, requestID, grokMediaContentProxyURL(c, requestID))
	if err != nil {
		return nil, fmt.Errorf("normalize %s video status: %w", adapter.Kind(), err)
	}
	contentURL, err := s.adaptedGrokVideoURL(account, adapter, videoadapter.OperationContent, requestID)
	if err != nil {
		return nil, err
	}
	contentReq, err := http.NewRequestWithContext(WithHTTPUpstreamRedirectsDisabled(upstreamCtx), http.MethodGet, contentURL, nil)
	if err != nil {
		return nil, err
	}
	applyAdaptedGrokVideoHeaders(contentReq, account, token, "", false)
	contentReq.Header.Set("Accept", "*/*")
	if c != nil {
		if rangeHeader := strings.TrimSpace(c.GetHeader("Range")); rangeHeader != "" {
			contentReq.Header.Set("Range", rangeHeader)
		}
	}
	contentResp, err := s.doAdaptedGrokVideoRequest(ctx, c, account, contentReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = contentResp.Body.Close() }()
	contentRequestID := firstNonEmpty(contentResp.Header.Get("x-request-id"), contentResp.Header.Get("xai-request-id"), statusRequestID)
	if contentResp.StatusCode >= 300 && contentResp.StatusCode < 400 {
		return nil, fmt.Errorf("adapted Grok video content redirect is not allowed")
	}
	if contentResp.StatusCode >= 400 && contentResp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		return s.handleGrokMediaErrorResponse(ctx, contentResp, c, account, contentRequestID, "")
	}
	s.updateGrokUsageFromResponse(withGrokTeamRateLimitModel(ctx, ""), account, contentResp.Header, contentResp.StatusCode)
	if err := writeGrokMediaContentResponse(c, contentResp); err != nil {
		return nil, err
	}
	result := &OpenAIForwardResult{RequestID: contentRequestID, UpstreamHeaders: contentResp.Header, ResponseHeaders: contentResp.Header.Clone(), Duration: time.Since(startTime)}
	if billed := ExtractGrokVideoBillingFromStatusBody(statusBody, nil, requestID); billed != nil {
		result.ResponseID = firstNonEmpty(billed.ResponseID, strings.TrimSpace(requestID))
		result.Model, result.BillingModel, result.UpstreamModel = billed.Model, billed.BillingModel, billed.UpstreamModel
		result.VideoCount, result.VideoResolution, result.VideoDurationSeconds = billed.VideoCount, billed.VideoResolution, billed.VideoDurationSeconds
	}
	return result, nil
}

func (s *OpenAIGatewayService) adaptedGrokVideoURL(account *Account, adapter videoadapter.Adapter, operation videoadapter.Operation, requestID string) (string, error) {
	validator, err := grokBaseURLValidator(account, s.cfg)
	if err != nil {
		return "", err
	}
	baseURL, err := validator(account.GetGrokMediaBaseURL())
	if err != nil {
		return "", err
	}
	return adapter.BuildURL(baseURL, requestID, operation)
}

func (s *OpenAIGatewayService) doAdaptedGrokVideoRequest(ctx context.Context, c *gin.Context, account *Account, req *http.Request) (*http.Response, error) {
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	upstreamStart := time.Now()
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
	if err != nil {
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
	}
	return resp, nil
}

func applyAdaptedGrokVideoHeaders(req *http.Request, account *Account, token, contentType string, hasBody bool) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if hasBody {
		if strings.TrimSpace(contentType) == "" {
			contentType = "application/json"
		}
		req.Header.Set("Content-Type", contentType)
	}
	account.ApplyHeaderOverrides(req.Header)
}

func videoAdapterOperation(endpoint GrokMediaEndpoint) (videoadapter.Operation, bool) {
	switch endpoint {
	case GrokMediaEndpointVideosGenerations:
		return videoadapter.OperationCreate, true
	case GrokMediaEndpointVideosEdits:
		return videoadapter.OperationEdit, true
	case GrokMediaEndpointVideosExtensions:
		return videoadapter.OperationExtension, true
	case GrokMediaEndpointVideoStatus:
		return videoadapter.OperationStatus, true
	case GrokMediaEndpointVideoContent:
		return videoadapter.OperationContent, true
	default:
		return "", false
	}
}
