package videoadapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const retiredSeedanceLiteT2V = "doubao-seedance-1-0-lite-t2v-250428"

// volcengineArkAdapter implements the asynchronous Volcengine Ark content
// generation protocol. The old Seedance Lite model remains accepted for Ark
// accounts and compatible relays that still expose that model ID.
type volcengineArkAdapter struct{}

func (volcengineArkAdapter) Kind() Kind { return KindVolcengineArk }

func (volcengineArkAdapter) BuildURL(baseURL, requestID string, operation Operation) (string, error) {
	return buildURL(baseURL, requestID, operation, map[Operation]string{
		OperationCreate: "/contents/generations/tasks",
		OperationStatus: "/contents/generations/tasks/{id}",
	})
}

func (volcengineArkAdapter) PrepareRequest(operation Operation, body []byte, _ string) (PreparedRequest, error) {
	if operation == OperationStatus || operation == OperationContent {
		return PreparedRequest{}, nil
	}
	if operation != OperationCreate {
		return PreparedRequest{}, fmt.Errorf("volcengine_ark does not support %s", operation)
	}
	if !json.Valid(body) {
		return PreparedRequest{}, errors.New("volcengine_ark adapter requires a JSON request body")
	}
	var source map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&source); err != nil {
		return PreparedRequest{}, fmt.Errorf("decode video request: %w", err)
	}
	model := strings.TrimSpace(stringValue(source["model"]))
	if model == "" {
		return PreparedRequest{}, errors.New("volcengine_ark video request requires model")
	}
	prompt := strings.TrimSpace(stringValue(source["prompt"]))
	if prompt == "" {
		return PreparedRequest{}, errors.New("volcengine_ark video request requires prompt")
	}
	duration := positiveInteger(source["duration"])
	if duration <= 0 {
		duration = positiveInteger(source["seconds"])
	}
	resolution := strings.ToLower(strings.TrimSpace(stringValue(source["resolution"])))
	if model == retiredSeedanceLiteT2V {
		if duration == 0 {
			duration = 5
		}
		if duration != 5 && duration != 10 {
			return PreparedRequest{}, fmt.Errorf("%s supports only 5 or 10 second videos", model)
		}
		if resolution == "" {
			resolution = "480p"
		}
		if resolution != "480p" && resolution != "720p" {
			return PreparedRequest{}, fmt.Errorf("%s supports only 480p or 720p", model)
		}
	}
	if ratio := strings.TrimSpace(stringValue(source["aspect_ratio"])); ratio != "" {
		prompt = appendArkPromptOption(prompt, "ratio", ratio)
	}
	if resolution != "" {
		prompt = appendArkPromptOption(prompt, "resolution", resolution)
	}
	if duration > 0 {
		prompt = appendArkPromptOption(prompt, "duration", strconv.Itoa(duration))
	}
	target := map[string]any{
		"model":   model,
		"content": []any{map[string]any{"type": "text", "text": prompt}},
	}
	out, err := json.Marshal(target)
	if err != nil {
		return PreparedRequest{}, fmt.Errorf("encode volcengine_ark request: %w", err)
	}
	return PreparedRequest{
		Body: out, ContentType: "application/json", VideoResolution: resolution,
		VideoDurationSeconds: duration,
	}, nil
}

func (volcengineArkAdapter) NormalizeResponse(operation Operation, body []byte, requestID, contentProxyURL string) ([]byte, error) {
	if !json.Valid(body) {
		return nil, errors.New("volcengine_ark upstream returned invalid JSON")
	}
	var source map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&source); err != nil {
		return nil, fmt.Errorf("decode volcengine_ark response: %w", err)
	}
	id := firstString(source, "id", "request_id", "task_id")
	if id == "" {
		id = strings.TrimSpace(requestID)
	}
	if id == "" && operation == OperationCreate {
		return nil, errors.New("volcengine_ark create response is missing a task ID")
	}
	status := normalizeOpenAIStatus(stringValue(source["status"]))
	result := map[string]any{"request_id": id, "status": status}
	if model := strings.TrimSpace(stringValue(source["model"])); model != "" {
		result["model"] = model
	}
	if operation == OperationStatus && status == "done" {
		video := map[string]any{"url": strings.TrimSpace(contentProxyURL)}
		if seconds := positiveInteger(source["duration"]); seconds > 0 {
			video["duration"] = seconds
		}
		result["video"] = video
	}
	if errValue, exists := source["error"]; exists && errValue != nil {
		result["error"] = errValue
	}
	out, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode Grok video response: %w", err)
	}
	return out, nil
}

func (volcengineArkAdapter) ResolveContent(baseURL, _ string, statusBody []byte) (ContentRequest, error) {
	if !json.Valid(statusBody) {
		return ContentRequest{}, errors.New("volcengine_ark upstream returned invalid JSON")
	}
	var source struct {
		Content struct {
			VideoURL string `json:"video_url"`
		} `json:"content"`
	}
	if err := json.Unmarshal(statusBody, &source); err != nil {
		return ContentRequest{}, fmt.Errorf("decode volcengine_ark content URL: %w", err)
	}
	rawURL := strings.TrimSpace(source.Content.VideoURL)
	if rawURL == "" {
		return ContentRequest{}, errors.New("volcengine_ark completed task is missing content.video_url")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || !isSafeSignedMediaURL(parsed) {
		return ContentRequest{}, errors.New("volcengine_ark returned an unsupported video content URL")
	}
	return ContentRequest{URL: parsed.String(), Authenticated: false}, nil
}

func appendArkPromptOption(prompt, name, value string) string {
	if strings.Contains(prompt, "--"+name+" ") || strings.HasSuffix(prompt, "--"+name) {
		return prompt
	}
	return strings.TrimSpace(prompt) + " --" + name + " " + strings.TrimSpace(value)
}
