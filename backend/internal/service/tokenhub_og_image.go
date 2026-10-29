package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	tokenHubOGImageGenerationEndpoint = "/v1/wand/og-image/generation"
	tokenHubOGImageEditsEndpoint      = "/v1/wand/og-image/edits"
	tokenHubOGImageMultipartEndpoint  = "/v1/wand/og-image/multipart-edits"
)

type tokenHubOGImageResponse struct {
	Created int64 `json:"created"`
	Data    []struct {
		URL     string `json:"url"`
		B64JSON string `json:"b64_json"`
	} `json:"data"`
	TokenHubUsage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"tokenhub_usage"`
	RequestID string `json:"request_id"`
}

func (s *OpenAIGatewayService) forwardTokenHubOGImage(
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
	if !isTokenHubOGImageModel(model) {
		return nil, fmt.Errorf("unsupported TokenHub OG image model %q", model)
	}
	requestBody, endpoint, contentType, err := buildTokenHubOGImageRequest(model, parsed)
	if err != nil {
		return nil, &OpenAIImagesUpstreamError{
			StatusCode: http.StatusBadRequest,
			ErrorType:  "invalid_request_error",
			Code:       "invalid_image_request",
			Message:    err.Error(),
		}
	}

	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	defer releaseUpstreamCtx()
	request, err := http.NewRequestWithContext(upstreamCtx, http.MethodPost, "https://tokenhub.tencentmaas.com"+endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return nil, fmt.Errorf("build TokenHub OG image request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(account.GetCredential("api_key")))
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Accept", "application/json")
	SetActualOpenAIUpstreamEndpoint(c, endpoint)
	SetOpsUpstreamModel(c, model)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	startedAt := time.Now()
	resp, err := s.doOpenAIUpstream(request, proxyURL, account)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(startedAt).Milliseconds())
	if err != nil {
		return nil, fmt.Errorf("TokenHub OG image request failed: %s", sanitizeUpstreamErrorMessage(err.Error()))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body := s.readUpstreamErrorBody(resp)
		requestID := firstNonEmpty(resp.Header.Get("request_id"), resp.Header.Get("x-request-id"), gjson.GetBytes(body, "request_id").String())
		upstreamError := gjson.GetBytes(body, "error")
		message := firstNonEmpty(upstreamError.Get("message").String(), extractUpstreamErrorMessage(body), http.StatusText(resp.StatusCode))
		statusCode := resp.StatusCode
		if strings.EqualFold(upstreamError.Get("type").String(), "image_generation_user_error") && statusCode >= 500 {
			statusCode = http.StatusBadRequest
		}
		return nil, &OpenAIImagesUpstreamError{
			StatusCode:        statusCode,
			ErrorType:         firstNonEmpty(upstreamError.Get("type").String(), "upstream_error"),
			Code:              firstNonEmpty(upstreamError.Get("code").String(), "tokenhub_image_error"),
			Message:           sanitizeUpstreamErrorMessage(message),
			Param:             upstreamError.Get("param").String(),
			UpstreamRequestID: requestID,
		}
	}
	body, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	var upstream tokenHubOGImageResponse
	if err := json.Unmarshal(body, &upstream); err != nil {
		return nil, fmt.Errorf("parse TokenHub OG image response: %w", err)
	}
	images := make([]map[string]string, 0, len(upstream.Data))
	for _, item := range upstream.Data {
		image := map[string]string{}
		if strings.TrimSpace(item.URL) != "" {
			image["url"] = item.URL
		} else if strings.TrimSpace(item.B64JSON) != "" {
			image["b64_json"] = item.B64JSON
		}
		if len(image) > 0 {
			images = append(images, image)
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
	created := upstream.Created
	if created <= 0 {
		created = time.Now().Unix()
	}
	c.JSON(http.StatusOK, gin.H{"created": created, "data": images})

	return &OpenAIForwardResult{
		RequestID:         upstream.RequestID,
		UpstreamHeaders:   resp.Header,
		Usage:             OpenAIUsage{OutputTokens: upstream.TokenHubUsage.TotalTokens},
		Model:             parsed.Model,
		BillingModel:      model,
		UpstreamModel:     model,
		ResponseHeaders:   resp.Header.Clone(),
		Duration:          time.Since(startedAt),
		ImageCount:        len(images),
		ImageEndpoint:     true,
		ImageSize:         NormalizeImageBillingTierOrDefault(parsed.SizeTier),
		ImageInputSize:    parsed.Size,
		ForceTokenBilling: true,
	}, nil
}

func buildTokenHubOGImageRequest(model string, parsed *OpenAIImagesRequest) ([]byte, string, string, error) {
	if parsed.Prompt == "" {
		return nil, "", "", fmt.Errorf("prompt is required")
	}
	if parsed.N < 1 || parsed.N > 10 {
		return nil, "", "", fmt.Errorf("n must be between 1 and 10")
	}
	if parsed.Background != "" && !tokenHubOGOneOf(strings.ToLower(parsed.Background), "opaque", "transparent", "auto") {
		return nil, "", "", fmt.Errorf("background must be opaque, transparent, or auto")
	}
	quality := strings.ToLower(strings.TrimSpace(parsed.Quality))
	if quality != "" && !tokenHubOGOneOf(quality, "auto", "high", "medium", "low", "xhigh", "max") {
		return nil, "", "", fmt.Errorf("quality is not supported")
	}
	if (quality == "xhigh" || quality == "max") && model == "custom-model-og-v2" {
		return nil, "", "", fmt.Errorf("quality %q is supported only by custom-model-og-v2.5-f and custom-model-og-v2.5-s", quality)
	}
	outputFormat := strings.ToLower(strings.TrimSpace(parsed.OutputFormat))
	if outputFormat != "" && outputFormat != "png" && outputFormat != "jpeg" {
		return nil, "", "", fmt.Errorf("output_format must be png or jpeg")
	}
	if parsed.OutputCompression != nil {
		if outputFormat != "jpeg" {
			return nil, "", "", fmt.Errorf("output_compression requires output_format jpeg")
		}
		if *parsed.OutputCompression < 0 || *parsed.OutputCompression > 100 {
			return nil, "", "", fmt.Errorf("output_compression must be between 0 and 100")
		}
	}
	responseFormat := strings.ToLower(strings.TrimSpace(parsed.ResponseFormat))
	if responseFormat == "" {
		responseFormat = "url"
	}
	if responseFormat != "url" && responseFormat != "b64_json" {
		return nil, "", "", fmt.Errorf("response_format must be url or b64_json")
	}
	logoAdd := 0
	if parsed.LogoAdd != nil {
		logoAdd = *parsed.LogoAdd
	}
	if logoAdd != 0 && logoAdd != 1 {
		return nil, "", "", fmt.Errorf("logo_add must be 0 or 1")
	}

	if parsed.IsEdits() {
		imageCount := len(parsed.InputImageURLs) + len(parsed.Uploads)
		if imageCount < 1 || imageCount > 16 {
			return nil, "", "", fmt.Errorf("edits requires between 1 and 16 input images")
		}
		if len(parsed.Uploads) > 0 {
			if len(parsed.InputImageURLs) > 0 {
				return nil, "", "", fmt.Errorf("mixing uploaded images and image URLs in one multipart edit is not supported")
			}
			return buildTokenHubOGMultipartEditsRequest(model, parsed, responseFormat, outputFormat, quality, logoAdd)
		}
		if len(parsed.InputImageURLs) == 0 {
			return nil, "", "", fmt.Errorf("at least one images[].image_url is required for edits")
		}
		images := make([]map[string]string, 0, len(parsed.InputImageURLs))
		for _, value := range parsed.InputImageURLs {
			imageURL, err := tokenHubOGImageRef(value, "")
			if err != nil {
				return nil, "", "", err
			}
			images = append(images, map[string]string{"image_url": imageURL})
		}
		payload := tokenHubOGCommonRequest(model, parsed, responseFormat, outputFormat, quality, logoAdd)
		payload["images"] = images
		if parsed.MaskImageURL != "" {
			mask, err := tokenHubOGImageRef(parsed.MaskImageURL, "")
			if err != nil {
				return nil, "", "", err
			}
			payload["mask"] = map[string]string{"image_url": mask}
		}
		body, err := json.Marshal(payload)
		return body, tokenHubOGImageEditsEndpoint, "application/json", err
	}
	payload := tokenHubOGCommonRequest(model, parsed, responseFormat, outputFormat, quality, logoAdd)
	body, err := json.Marshal(payload)
	return body, tokenHubOGImageGenerationEndpoint, "application/json", err
}

func tokenHubOGCommonRequest(model string, parsed *OpenAIImagesRequest, responseFormat, outputFormat, quality string, logoAdd int) map[string]any {
	payload := map[string]any{
		"model":           model,
		"prompt":          parsed.Prompt,
		"n":               parsed.N,
		"response_format": responseFormat,
	}
	if parsed.Size != "" {
		payload["size"] = parsed.Size
	}
	if quality != "" {
		payload["quality"] = quality
	}
	if parsed.Background != "" {
		payload["background"] = strings.ToLower(strings.TrimSpace(parsed.Background))
	}
	if outputFormat != "" {
		payload["output_format"] = outputFormat
	}
	if parsed.OutputCompression != nil {
		payload["output_compression"] = *parsed.OutputCompression
	}
	if logoAdd == 1 {
		payload["logo_add"] = logoAdd
	}
	return payload
}

func tokenHubOGOneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

func buildTokenHubOGMultipartEditsRequest(model string, parsed *OpenAIImagesRequest, responseFormat, outputFormat, quality string, logoAdd int) ([]byte, string, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fields := tokenHubOGCommonRequest(model, parsed, responseFormat, outputFormat, quality, logoAdd)
	for key, value := range fields {
		if err := writer.WriteField(key, fmt.Sprint(value)); err != nil {
			return nil, "", "", err
		}
	}
	for _, upload := range parsed.Uploads {
		filename := strings.TrimSpace(upload.FileName)
		if filename == "" {
			filename = "image"
		}
		part, err := writer.CreateFormFile("image[]", filename)
		if err != nil {
			return nil, "", "", err
		}
		if _, err := part.Write(upload.Data); err != nil {
			return nil, "", "", err
		}
	}
	if parsed.MaskUpload != nil {
		filename := strings.TrimSpace(parsed.MaskUpload.FileName)
		if filename == "" {
			filename = "mask"
		}
		part, err := writer.CreateFormFile("mask", filename)
		if err != nil {
			return nil, "", "", err
		}
		if _, err := part.Write(parsed.MaskUpload.Data); err != nil {
			return nil, "", "", err
		}
	} else if parsed.MaskImageURL != "" {
		mask, err := tokenHubOGImageRef(parsed.MaskImageURL, "")
		if err != nil {
			return nil, "", "", err
		}
		if err := writer.WriteField("mask[image_url]", mask); err != nil {
			return nil, "", "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", "", err
	}
	return body.Bytes(), tokenHubOGImageMultipartEndpoint, writer.FormDataContentType(), nil
}

func tokenHubOGImageRef(value, contentType string) (string, error) {
	inline, err := tokenHubGNInlineDataFromInput(value, contentType)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(inline.Data, "http://") || strings.HasPrefix(inline.Data, "https://") {
		return inline.Data, nil
	}
	return "data:" + inline.MIMEType + ";base64," + inline.Data, nil
}
