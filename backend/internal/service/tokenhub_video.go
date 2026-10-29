package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const tokenHubVideoBaseURL = "https://tokenhub.tencentmaas.com"

func TokenHubVideoTaskKey(family, id string) string {
	return "tokenhub-video:" + family + ":" + strings.TrimSpace(id)
}

func isTokenHubVideoModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "wand-vega-video-") ||
		model == "wand-vega-video1.0-lite" ||
		model == "wand-vega-video1.0-pro" ||
		model == "wand-vega-video1.5-pro" ||
		model == "minimax-video-v2.3" ||
		model == "minimax-video-v2.3-fast" ||
		model == "minimax-video-h3" ||
		model == "minimax-video-h3-max"
}

func normalizeTokenHubVideoModel(model string) string {
	model = strings.TrimSpace(model)
	switch strings.ToLower(model) {
	case "wand-vega-video1.0-lite":
		return "wand-vega-video-1.0-lite"
	case "wand-vega-video1.0-pro":
		return "wand-vega-video-1.0-pro"
	case "wand-vega-video1.5-pro":
		return "wand-vega-video-1.5-pro"
	default:
		return model
	}
}

func tokenHubVideoFamily(model string) string {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "minimax-video-v2.3", "minimax-video-v2.3-fast":
		return "minimax-v1"
	case "minimax-video-h3", "minimax-video-h3-max":
		return "minimax-v2"
	default:
		return "wand"
	}
}

func splitTokenHubVideoTaskKey(taskKey string) (family, id string, err error) {
	parts := strings.SplitN(strings.TrimSpace(taskKey), ":", 3)
	if len(parts) != 3 || parts[0] != "tokenhub-video" || (parts[1] != "wand" && parts[1] != "minimax-v1" && parts[1] != "minimax-v2") || strings.TrimSpace(parts[2]) == "" {
		return "", "", fmt.Errorf("invalid TokenHub video task ID")
	}
	if err := validateUpstreamPathSegment("TokenHub video task ID", parts[2]); err != nil {
		return "", "", fmt.Errorf("invalid TokenHub video task ID")
	}
	return parts[1], parts[2], nil
}

func (s *OpenAIGatewayService) ForwardTokenHubVideo(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	endpoint GrokMediaEndpoint,
	taskKey string,
	body []byte,
) (*OpenAIForwardResult, error) {
	if account == nil || !account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityTokenHubVideo) || !endpoint.IsSeedance() {
		return nil, fmt.Errorf("TokenHub video requires an enabled TokenHub API-key account and Ark video endpoint")
	}
	if endpoint == SeedanceEndpointDelete {
		return nil, fmt.Errorf("TokenHub video task cancellation is not supported")
	}

	apiKey := strings.TrimSpace(account.GetCredential("api_key"))
	if apiKey == "" {
		return nil, fmt.Errorf("TokenHub video account is missing api_key")
	}
	method := http.MethodGet
	upstreamURL := ""
	clientModel := ""
	upstreamModel := ""
	family := ""
	var requestBody []byte
	if endpoint == SeedanceEndpointCreate {
		var err error
		clientModel, err = tokenHubVideoRequestModel(body)
		if err != nil {
			return nil, err
		}
		upstreamModel = normalizeTokenHubVideoModel(account.GetMappedModel(clientModel))
		if !isTokenHubVideoModel(upstreamModel) {
			return nil, fmt.Errorf("unsupported TokenHub video model mapping for %q", clientModel)
		}
		family = tokenHubVideoFamily(upstreamModel)
		requestBody, upstreamURL, err = buildTokenHubVideoRequest(body, upstreamModel, family)
		if err != nil {
			return nil, err
		}
		method = http.MethodPost
	} else {
		var err error
		var id string
		family, id, err = splitTokenHubVideoTaskKey(taskKey)
		if err != nil {
			return nil, err
		}
		upstreamURL = tokenHubVideoStatusURL(family) + "/" + id
	}

	requestURL := tokenHubVideoBaseURL + upstreamURL
	upstreamCtx, release := detachUpstreamContext(ctx)
	defer release()
	request, err := http.NewRequestWithContext(upstreamCtx, method, requestURL, bytes.NewReader(requestBody))
	if err != nil {
		return nil, fmt.Errorf("build TokenHub video request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	SetActualOpenAIUpstreamEndpoint(c, upstreamURL)
	if upstreamModel != "" {
		SetOpsUpstreamModel(c, upstreamModel)
	}
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	started := time.Now()
	response, err := s.httpUpstream.Do(request, proxyURL, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(started).Milliseconds())
	if err != nil {
		return nil, fmt.Errorf("TokenHub video request failed: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := ReadUpstreamResponseBody(response.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		writeGrokMediaResponse(c, response, responseBody, s.responseHeaderFilter)
		return nil, fmt.Errorf("TokenHub video upstream status %d", response.StatusCode)
	}

	result := &OpenAIForwardResult{
		Model: clientModel, BillingModel: clientModel, UpstreamModel: upstreamModel,
		RequestID: strings.TrimSpace(gjson.GetBytes(responseBody, "request_id").String()),
		Duration:  time.Since(started), ResponseHeaders: response.Header.Clone(),
	}
	if endpoint == SeedanceEndpointCreate {
		id := strings.TrimSpace(gjson.GetBytes(responseBody, "id").String())
		if id == "" {
			id = strings.TrimSpace(gjson.GetBytes(responseBody, "task_id").String())
		}
		if id == "" {
			return nil, fmt.Errorf("TokenHub video create response is missing task ID")
		}
		result.ResponseID = TokenHubVideoTaskKey(family, id)
		c.JSON(http.StatusOK, gin.H{"id": result.ResponseID, "model": clientModel, "status": "queued"})
		return result, nil
	}

	result.ResponseID = taskKey
	status, videoURL, upstreamResponseModel, errorMessage := parseTokenHubVideoStatus(family, responseBody)
	result.UpstreamModel = firstNonEmpty(upstreamResponseModel, upstreamModel)
	if status == "failed" {
		response := gin.H{"id": taskKey, "status": status, "error": gin.H{"message": errorMessage}}
		if result.UpstreamModel != "" {
			response["model"] = result.UpstreamModel
		}
		c.JSON(http.StatusOK, response)
		return result, nil
	}
	if status == "succeeded" {
		if videoURL == "" {
			return nil, fmt.Errorf("TokenHub video task succeeded without a video URL")
		}
		result.Usage.OutputTokens = max(0, int(gjson.GetBytes(responseBody, "tokenhub_usage.total_tokens").Int()))
		if result.Usage.OutputTokens <= 0 {
			return nil, fmt.Errorf("TokenHub video task succeeded without token usage")
		}
		content := gin.H{"video_url": videoURL}
		if lastFrameURL := strings.TrimSpace(gjson.GetBytes(responseBody, "content.last_frame_url").String()); lastFrameURL != "" {
			content["last_frame_url"] = lastFrameURL
		}
		response := gin.H{"id": taskKey, "status": status, "content": content}
		if result.UpstreamModel != "" {
			response["model"] = result.UpstreamModel
		}
		c.JSON(http.StatusOK, response)
		return result, nil
	}
	responseJSON := gin.H{"id": taskKey, "status": status}
	if result.UpstreamModel != "" {
		responseJSON["model"] = result.UpstreamModel
	}
	c.JSON(http.StatusOK, responseJSON)
	return result, nil
}

func tokenHubVideoRequestModel(body []byte) (string, error) {
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return "", fmt.Errorf("request body must be a JSON object")
	}
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if model == "" {
		return "", fmt.Errorf("model is required")
	}
	content := gjson.GetBytes(body, "content")
	if !content.IsArray() || len(content.Array()) == 0 {
		return "", fmt.Errorf("content must be a non-empty array")
	}
	return model, nil
}

func buildTokenHubVideoRequest(body []byte, model, family string) ([]byte, string, error) {
	content := gjson.GetBytes(body, "content").Array()
	texts := make([]string, 0, len(content))
	for _, part := range content {
		if part.Get("type").String() == "text" && strings.TrimSpace(part.Get("text").String()) != "" {
			texts = append(texts, strings.TrimSpace(part.Get("text").String()))
		}
	}
	if len(texts) == 0 {
		return nil, "", fmt.Errorf("content must include at least one non-empty text part")
	}

	payload := map[string]any{"model": model}
	endpoint := ""
	if family == "minimax-v1" {
		payload["prompt"] = strings.Join(texts, "\n")
		firstFrameCount := 0
		lastFrameFound := false
		for _, part := range content {
			kind := part.Get("type").String()
			role := part.Get("role").String()
			switch {
			case kind == "image_url" && role == "first_frame":
				payload["first_frame_image"] = part.Get("image_url.url").String()
				firstFrameCount++
			case kind == "image_url" && role == "last_frame":
				payload["last_frame_image"] = part.Get("image_url.url").String()
				lastFrameFound = true
			case kind == "text":
			case kind == "image_url" || kind == "video_url" || kind == "audio_url":
				return nil, "", fmt.Errorf("%s supports only text and first/last frame images", model)
			default:
				return nil, "", fmt.Errorf("unsupported content type %q", kind)
			}
		}
		if model == "minimax-video-v2.3-fast" && (firstFrameCount != 1 || lastFrameFound) {
			return nil, "", fmt.Errorf("minimax-video-v2.3-fast requires exactly one first-frame image and does not support a last frame")
		}
		endpoint = "/v1/wand/minimax-video/generation"
	} else {
		payload["content"] = json.RawMessage(gjson.GetBytes(body, "content").Raw)
		if family == "minimax-v2" {
			endpoint = "/v1/wand/minimax-video-v2/generation"
		} else {
			endpoint = "/v1/wand/vega-videos/tasks"
		}
	}

	fields := []string{"duration", "resolution", "aigc_watermark"}
	if family != "minimax-v1" {
		fields = append(fields, "ratio")
	}
	for _, field := range fields {
		value := gjson.GetBytes(body, field)
		if value.Exists() {
			payload[field] = value.Value()
		}
	}
	if family == "wand" {
		options := gjson.GetBytes(body, "extras.tokenhub.options")
		if options.Exists() {
			if !options.IsObject() {
				return nil, "", fmt.Errorf("extras.tokenhub.options must be an object")
			}
			payload["options"] = json.RawMessage(options.Raw)
		}
	} else {
		extra := gjson.GetBytes(body, "extras.minimax")
		if extra.Exists() {
			if !extra.IsObject() {
				return nil, "", fmt.Errorf("extras.minimax must be an object")
			}
			if family == "minimax-v2" {
				if value := extra.Get("extra.prompt_expansion_mode"); value.Exists() {
					payload["extra"] = map[string]any{"prompt_expansion_mode": value.Value()}
				}
				for _, field := range []string{"aigc_watermark"} {
					if value := extra.Get(field); value.Exists() {
						payload[field] = value.Value()
					}
				}
			} else {
				for _, field := range []string{"prompt_optimizer", "fast_pretreatment"} {
					if value := extra.Get(field); value.Exists() {
						payload[field] = value.Value()
					}
				}
			}
		}
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, "", fmt.Errorf("encode TokenHub video request: %w", err)
	}
	return encoded, endpoint, nil
}

func tokenHubVideoStatusURL(family string) string {
	switch family {
	case "minimax-v1":
		return "/v1/wand/minimax-video/tasks"
	case "minimax-v2":
		return "/v1/wand/minimax-video-v2/tasks"
	default:
		return "/v1/wand/vega-videos/tasks"
	}
}

func parseTokenHubVideoStatus(family string, body []byte) (status, videoURL, model, errorMessage string) {
	statusPath, contentPath, errorPath := "status", "content.url", "base_resp.status_msg"
	if family == "wand" {
		contentPath = "content.video_url"
	}
	if family == "minimax-v2" {
		statusPath, contentPath, errorPath = "task.status", "task.content.url", "task.error.message"
	}
	status = strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, statusPath).String()))
	switch status {
	case "success", "succeeded":
		status = "succeeded"
	case "fail", "failed":
		status = "failed"
	case "cancelled":
		status = "cancelled"
	case "preparing", "queueing", "queued":
		status = "queued"
	case "processing", "running":
		status = "running"
	default:
		status = "running"
	}
	videoURL = strings.TrimSpace(gjson.GetBytes(body, contentPath).String())
	model = firstNonEmpty(gjson.GetBytes(body, "task.model").String(), gjson.GetBytes(body, "model").String())
	errorMessage = strings.TrimSpace(gjson.GetBytes(body, errorPath).String())
	if errorMessage == "" {
		errorMessage = strings.TrimSpace(gjson.GetBytes(body, "base_resp.status_msg").String())
	}
	return status, videoURL, model, errorMessage
}
