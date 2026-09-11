package videoadapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ctyunMiniMaxAdapter implements Tianyi Cloud's MiniMax-H3 asynchronous
// protocol. It deliberately remains separate from OpenAI-compatible video
// adapters: Tianyi uses video_generation paths and wraps task results in task.
type ctyunMiniMaxAdapter struct{}

func (ctyunMiniMaxAdapter) Kind() Kind { return KindCtyunMiniMax }

func (ctyunMiniMaxAdapter) BuildURL(baseURL, requestID string, operation Operation) (string, error) {
	return buildURL(baseURL, requestID, operation, map[Operation]string{
		OperationCreate: "/video_generation",
		OperationStatus: "/query/video_generation/{id}",
	})
}

func (ctyunMiniMaxAdapter) PrepareRequest(operation Operation, body []byte, _ string) (PreparedRequest, error) {
	if operation == OperationStatus || operation == OperationContent {
		return PreparedRequest{}, nil
	}
	if operation != OperationCreate {
		return PreparedRequest{}, fmt.Errorf("ctyun_minimax does not support %s", operation)
	}
	if !json.Valid(body) {
		return PreparedRequest{}, errors.New("ctyun_minimax adapter requires a JSON request body")
	}
	var source map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&source); err != nil {
		return PreparedRequest{}, fmt.Errorf("decode video request: %w", err)
	}
	model := strings.TrimSpace(stringValue(source["model"]))
	prompt := strings.TrimSpace(stringValue(source["prompt"]))
	if model == "" || prompt == "" {
		return PreparedRequest{}, errors.New("ctyun_minimax video request requires model and prompt")
	}
	duration := positiveInteger(source["duration"])
	if duration <= 0 {
		duration = positiveInteger(source["seconds"])
	}
	if duration <= 0 {
		duration = 5
	}
	if duration < 4 || duration > 15 {
		return PreparedRequest{}, errors.New("ctyun_minimax supports video durations from 4 to 15 seconds")
	}
	rawResolution := strings.TrimSpace(stringValue(source["resolution"]))
	resolution := ctyunMiniMaxResolution(rawResolution)
	if rawResolution != "" && resolution == "" {
		return PreparedRequest{}, errors.New("ctyun_minimax supports only 480P, 768P, or 2K resolution")
	}
	if resolution == "" {
		resolution = "480P"
	}
	ratio := strings.TrimSpace(stringValue(source["aspect_ratio"]))
	if ratio == "" || strings.EqualFold(ratio, "adaptive") {
		ratio = "16:9"
	}
	target := map[string]any{
		"model": model, "content": []any{map[string]any{"type": "text", "text": prompt}},
		"resolution": resolution, "duration": duration, "ratio": ratio,
	}
	out, err := json.Marshal(target)
	if err != nil {
		return PreparedRequest{}, fmt.Errorf("encode ctyun_minimax request: %w", err)
	}
	return PreparedRequest{Body: out, ContentType: "application/json", VideoResolution: strings.ToLower(resolution), VideoDurationSeconds: duration}, nil
}

func (ctyunMiniMaxAdapter) NormalizeResponse(operation Operation, body []byte, requestID, contentProxyURL string) ([]byte, error) {
	task, err := ctyunMiniMaxTask(body)
	if err != nil {
		return nil, err
	}
	id := firstString(task, "id", "task_id", "request_id")
	if id == "" {
		id = strings.TrimSpace(requestID)
	}
	if id == "" && operation == OperationCreate {
		return nil, errors.New("ctyun_minimax create response is missing a task ID")
	}
	result := map[string]any{"request_id": id, "status": normalizeOpenAIStatus(stringValue(task["status"]))}
	if model := strings.TrimSpace(stringValue(task["model"])); model != "" {
		result["model"] = model
	}
	if operation == OperationStatus && result["status"] == "done" {
		video := map[string]any{"url": strings.TrimSpace(contentProxyURL)}
		if duration := positiveInteger(task["duration"]); duration > 0 {
			video["duration"] = duration
		}
		result["video"] = video
	}
	if errValue, ok := task["error"]; ok && errValue != nil {
		result["error"] = errValue
	}
	return json.Marshal(result)
}

func (ctyunMiniMaxAdapter) ResolveContent(baseURL, _ string, statusBody []byte) (ContentRequest, error) {
	task, err := ctyunMiniMaxTask(statusBody)
	if err != nil {
		return ContentRequest{}, err
	}
	content, _ := task["content"].(map[string]any)
	rawURL := strings.TrimSpace(stringValue(content["url"]))
	parsed, err := url.Parse(rawURL)
	if err != nil || !isSafeSignedMediaURL(parsed) {
		return ContentRequest{}, errors.New("ctyun_minimax returned an unsupported video content URL")
	}
	return ContentRequest{URL: parsed.String(), Authenticated: false}, nil
}

func ctyunMiniMaxTask(body []byte) (map[string]any, error) {
	if !json.Valid(body) {
		return nil, errors.New("ctyun_minimax upstream returned invalid JSON")
	}
	var source map[string]any
	if err := json.Unmarshal(body, &source); err != nil {
		return nil, fmt.Errorf("decode ctyun_minimax response: %w", err)
	}
	if task, ok := source["task"].(map[string]any); ok {
		return task, nil
	}
	return source, nil // create responses contain task_id at the top level.
}

func ctyunMiniMaxResolution(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "480p":
		return "480P"
	case "768p":
		return "768P"
	case "2k":
		return "2K"
	default:
		return ""
	}
}
