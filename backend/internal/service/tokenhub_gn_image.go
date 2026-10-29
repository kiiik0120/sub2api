package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const tokenHubGNImageEndpoint = "/v1/wand/gn-image/generation"

var tokenHubGNImageModels = map[string]struct{}{
	"custom-model-gn-v2.5":      {},
	"custom-model-gn-v3.0":      {},
	"custom-model-gn-v3.1":      {},
	"custom-model-gn-v3.1-lite": {},
	"custom-model-gn-v3.6":      {},
}

var tokenHubOGImageModels = map[string]struct{}{
	"custom-model-og-v2":     {},
	"custom-model-og-v2.5-f": {},
	"custom-model-og-v2.5-s": {},
}

func isTokenHubGNImageModel(model string) bool {
	_, ok := tokenHubGNImageModels[strings.ToLower(strings.TrimSpace(model))]
	return ok
}

func isTokenHubOGImageModel(model string) bool {
	_, ok := tokenHubOGImageModels[strings.ToLower(strings.TrimSpace(model))]
	return ok
}

func isTokenHubImageModel(model string) bool {
	return isTokenHubGNImageModel(model) || isTokenHubOGImageModel(model)
}

func isTokenHubImageAccount(account *Account) bool {
	if account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeAPIKey {
		return false
	}
	baseURL, err := url.Parse(strings.TrimSpace(account.GetOpenAIBaseURL()))
	return err == nil && strings.EqualFold(baseURL.Hostname(), "tokenhub.tencentmaas.com")
}

type tokenHubGNRequest struct {
	Model            string              `json:"model"`
	Contents         []tokenHubGNContent `json:"contents"`
	GenerationConfig tokenHubGNGenConfig `json:"generationConfig"`
	ResponseFormat   string              `json:"responseFormat,omitempty"`
	LogoAdd          int                 `json:"logoAdd,omitempty"`
}

type tokenHubGNContent struct {
	Role  string           `json:"role"`
	Parts []tokenHubGNPart `json:"parts"`
}

type tokenHubGNPart struct {
	Text       string                `json:"text,omitempty"`
	InlineData *tokenHubGNInlineData `json:"inlineData,omitempty"`
}

type tokenHubGNInlineData struct {
	MIMEType string `json:"mimeType,omitempty"`
	Data     string `json:"data"`
}

type tokenHubGNGenConfig struct {
	ResponseModalities []string              `json:"responseModalities"`
	ResponseFormat     tokenHubGNImageFormat `json:"responseFormat"`
}

type tokenHubGNImageFormat struct {
	Image tokenHubGNImageOptions `json:"image"`
}

type tokenHubGNImageOptions struct {
	AspectRatio string `json:"aspectRatio,omitempty"`
	ImageSize   string `json:"imageSize,omitempty"`
}

type tokenHubGNResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				InlineData *struct {
					Data     string `json:"data"`
					MIMEType string `json:"mimeType"`
				} `json:"inlineData"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	ModelVersion  string `json:"modelVersion"`
	ResponseID    string `json:"responseId"`
	RequestID     string `json:"request_id"`
	TokenHubUsage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"tokenhub_usage"`
}

func (s *OpenAIGatewayService) forwardTokenHubGNImage(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	parsed *OpenAIImagesRequest,
	channelMappedModel string,
) (*OpenAIForwardResult, error) {
	model := strings.TrimSpace(parsed.Model)
	if mapped := strings.TrimSpace(channelMappedModel); mapped != "" {
		model = mapped
	}
	if mapped := strings.TrimSpace(account.GetMappedModel(model)); mapped != "" {
		model = mapped
	}
	if !isTokenHubGNImageModel(model) {
		return nil, fmt.Errorf("unsupported TokenHub GN image model %q", model)
	}
	if parsed.Stream || parsed.N != 1 || parsed.HasMask || parsed.HasNativeOptions {
		return nil, &OpenAIImagesUpstreamError{
			StatusCode: http.StatusBadRequest,
			ErrorType:  "invalid_request_error",
			Code:       "unsupported_image_option",
			Message:    "TokenHub GN images supports one non-streaming image without mask or OpenAI-specific image options",
		}
	}
	if parsed.Prompt == "" {
		return nil, &OpenAIImagesUpstreamError{StatusCode: http.StatusBadRequest, ErrorType: "invalid_request_error", Code: "prompt_required", Message: "prompt is required"}
	}
	if parsed.IsEdits() && len(parsed.InputImageURLs)+len(parsed.Uploads) == 0 {
		return nil, &OpenAIImagesUpstreamError{StatusCode: http.StatusBadRequest, ErrorType: "invalid_request_error", Code: "image_required", Message: "at least one input image is required"}
	}

	requestBody, responseFormat, err := buildTokenHubGNImageRequest(model, parsed)
	if err != nil {
		return nil, &OpenAIImagesUpstreamError{StatusCode: http.StatusBadRequest, ErrorType: "invalid_request_error", Code: "invalid_image_request", Message: err.Error()}
	}
	encoded, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("marshal TokenHub GN image request: %w", err)
	}

	upstreamCtx, release := detachUpstreamContext(ctx)
	defer release()
	request, err := http.NewRequestWithContext(upstreamCtx, http.MethodPost, "https://tokenhub.tencentmaas.com"+tokenHubGNImageEndpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("build TokenHub GN image request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(account.GetCredential("api_key")))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	SetActualOpenAIUpstreamEndpoint(c, tokenHubGNImageEndpoint)
	SetOpsUpstreamModel(c, model)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	startedAt := time.Now()
	resp, err := s.doOpenAIUpstream(request, proxyURL, account)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(startedAt).Milliseconds())
	if err != nil {
		return nil, fmt.Errorf("TokenHub GN image request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body := s.readUpstreamErrorBody(resp)
		requestID := firstNonEmpty(resp.Header.Get("request_id"), resp.Header.Get("x-request-id"), gjson.GetBytes(body, "request_id").String())
		message := firstNonEmpty(extractUpstreamErrorMessage(body), http.StatusText(resp.StatusCode))
		return nil, &OpenAIImagesUpstreamError{
			StatusCode:        resp.StatusCode,
			ErrorType:         "upstream_error",
			Code:              "tokenhub_image_error",
			Message:           sanitizeUpstreamErrorMessage(message),
			UpstreamRequestID: requestID,
		}
	}
	body, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	var upstream tokenHubGNResponse
	if err := json.Unmarshal(body, &upstream); err != nil {
		return nil, fmt.Errorf("parse TokenHub GN image response: %w", err)
	}
	images := make([]map[string]string, 0, len(upstream.Candidates))
	for _, candidate := range upstream.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.InlineData == nil || strings.TrimSpace(part.InlineData.Data) == "" {
				continue
			}
			value := strings.TrimSpace(part.InlineData.Data)
			if strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://") {
				images = append(images, map[string]string{"url": value})
			} else if responseFormat == "url" {
				images = append(images, map[string]string{"b64_json": value})
			} else {
				images = append(images, map[string]string{"b64_json": value})
			}
		}
	}
	if len(images) == 0 {
		return nil, &OpenAIImagesUpstreamError{
			StatusCode:        http.StatusBadGateway,
			ErrorType:         "upstream_error",
			Code:              "image_generation_unavailable",
			Message:           "TokenHub returned no image output",
			UpstreamRequestID: upstream.RequestID,
		}
	}
	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	c.JSON(http.StatusOK, gin.H{"created": time.Now().Unix(), "data": images})

	size := NormalizeImageBillingTierOrDefault(parsed.SizeTier)
	return &OpenAIForwardResult{
		RequestID:             firstNonEmpty(upstream.RequestID, upstream.ResponseID),
		ResponseID:            upstream.ResponseID,
		UpstreamHeaders:       resp.Header,
		Usage:                 OpenAIUsage{OutputTokens: upstream.TokenHubUsage.TotalTokens},
		Model:                 parsed.Model,
		BillingModel:          model,
		UpstreamModel:         model,
		UpstreamResponseModel: upstream.ModelVersion,
		ResponseHeaders:       resp.Header.Clone(),
		Duration:              time.Since(startedAt),
		ImageCount:            len(images),
		ImageEndpoint:         true,
		ImageSize:             size,
		ImageInputSize:        parsed.Size,
		ForceTokenBilling:     true,
	}, nil
}

func buildTokenHubGNImageRequest(model string, parsed *OpenAIImagesRequest) (*tokenHubGNRequest, string, error) {
	responseFormat := strings.ToLower(strings.TrimSpace(parsed.ResponseFormat))
	if responseFormat == "" {
		responseFormat = "url"
	}
	if responseFormat != "url" && responseFormat != "b64_json" {
		return nil, "", fmt.Errorf("response_format must be url or b64_json")
	}
	imageSize := NormalizeImageBillingTierOrDefault(parsed.SizeTier)
	if imageSize != "1K" && imageSize != "2K" && imageSize != "4K" {
		imageSize = "1K"
	}
	parts := []tokenHubGNPart{{Text: parsed.Prompt}}
	for _, imageURL := range parsed.InputImageURLs {
		inline, err := tokenHubGNInlineDataFromInput(imageURL, "")
		if err != nil {
			return nil, "", err
		}
		parts = append(parts, tokenHubGNPart{InlineData: inline})
	}
	for _, upload := range parsed.Uploads {
		contentType := strings.TrimSpace(upload.ContentType)
		if contentType == "" {
			contentType = http.DetectContentType(upload.Data)
		}
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || !strings.HasPrefix(strings.ToLower(mediaType), "image/") {
			return nil, "", fmt.Errorf("uploaded image has an invalid image media type")
		}
		parts = append(parts, tokenHubGNPart{InlineData: &tokenHubGNInlineData{
			MIMEType: mediaType,
			Data:     base64.StdEncoding.EncodeToString(upload.Data),
		}})
	}
	aspectRatio := tokenHubGNAspectRatio(parsed.Size)
	request := &tokenHubGNRequest{
		Model:    model,
		Contents: []tokenHubGNContent{{Role: "user", Parts: parts}},
		GenerationConfig: tokenHubGNGenConfig{
			ResponseModalities: []string{"IMAGE"},
			ResponseFormat: tokenHubGNImageFormat{Image: tokenHubGNImageOptions{
				AspectRatio: aspectRatio,
				ImageSize:   imageSize,
			}},
		},
		ResponseFormat: responseFormat,
	}
	if parsed.LogoAdd != nil {
		if *parsed.LogoAdd != 0 && *parsed.LogoAdd != 1 {
			return nil, "", fmt.Errorf("logo_add must be 0 or 1")
		}
		request.LogoAdd = *parsed.LogoAdd
	}
	return request, responseFormat, nil
}

func tokenHubGNInlineDataFromInput(value, contentType string) (*tokenHubGNInlineData, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("input image cannot be empty")
	}
	if strings.HasPrefix(strings.ToLower(value), "data:") {
		header, encoded, ok := strings.Cut(value, ",")
		if !ok || !strings.Contains(strings.ToLower(header), ";base64") {
			return nil, fmt.Errorf("input image data URL must contain base64 data")
		}
		mediaType := strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
		if _, err := base64.StdEncoding.DecodeString(encoded); err != nil {
			return nil, fmt.Errorf("input image data URL contains invalid base64")
		}
		return &tokenHubGNInlineData{MIMEType: mediaType, Data: encoded}, nil
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil {
		return nil, fmt.Errorf("input image must be a public HTTP(S) URL or base64 data URL")
	}
	mediaType := strings.TrimSpace(contentType)
	if mediaType == "" {
		mediaType = mime.TypeByExtension(path.Ext(parsed.Path))
	}
	if mediaType == "" {
		mediaType = "image/jpeg"
	}
	if parsedType, _, parseErr := mime.ParseMediaType(mediaType); parseErr == nil {
		mediaType = parsedType
	}
	return &tokenHubGNInlineData{MIMEType: mediaType, Data: value}, nil
}

func tokenHubGNAspectRatio(size string) string {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(size)), "x")
	if len(parts) != 2 {
		return "1:1"
	}
	w, errW := strconv.ParseFloat(parts[0], 64)
	h, errH := strconv.ParseFloat(parts[1], 64)
	if errW != nil || errH != nil || w <= 0 || h <= 0 {
		return "1:1"
	}
	ratio := w / h
	switch {
	case ratio >= 1.55:
		return "16:9"
	case ratio >= 1.15:
		return "4:3"
	case ratio <= 0.64:
		return "9:16"
	case ratio <= 0.87:
		return "3:4"
	default:
		return "1:1"
	}
}
