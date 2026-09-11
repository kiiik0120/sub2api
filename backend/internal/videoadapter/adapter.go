// Package videoadapter normalizes third-party asynchronous video protocols to
// the Grok/xAI video contract exposed by Sub2API.
package videoadapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const CredentialKey = "video_adapter"

type Kind string

const (
	KindNative       Kind = "native"
	KindOpenAIVideos Kind = "openai_videos"
)

type Operation string

const (
	OperationCreate    Operation = "create"
	OperationEdit      Operation = "edit"
	OperationExtension Operation = "extension"
	OperationStatus    Operation = "status"
	OperationContent   Operation = "content"
)

// Adapter is the narrow seam used by the Grok video forwarder. Implementations
// own upstream paths, request conversion, and Grok response normalization.
type Adapter interface {
	Kind() Kind
	BuildURL(validatedBaseURL, requestID string, operation Operation) (string, error)
	PrepareRequest(operation Operation, body []byte, contentType string) (PreparedRequest, error)
	NormalizeResponse(operation Operation, body []byte, requestID, contentProxyURL string) ([]byte, error)
}

// PreparedRequest includes the effective billable dimensions after protocol
// conversion. This prevents billing from assuming Grok defaults when an
// upstream protocol has different defaults or resolution tiers.
type PreparedRequest struct {
	Body                 []byte
	ContentType          string
	VideoResolution      string
	VideoDurationSeconds int
}

// Resolve reads the non-secret account credential selector. Missing and native
// values deliberately return enabled=false so existing xAI traffic stays on the
// upstream implementation without any behavior change.
func Resolve(credentials map[string]any) (adapter Adapter, enabled bool, err error) {
	if credentials == nil {
		return nativeAdapter{}, false, nil
	}
	raw, exists := credentials[CredentialKey]
	if !exists || raw == nil {
		return nativeAdapter{}, false, nil
	}
	name := ""
	switch value := raw.(type) {
	case string:
		name = value
	case map[string]any:
		name, _ = value["type"].(string)
	default:
		return nil, false, fmt.Errorf("%s must be a string or object", CredentialKey)
	}
	switch Kind(strings.ToLower(strings.TrimSpace(name))) {
	case "", KindNative:
		return nativeAdapter{}, false, nil
	case KindOpenAIVideos:
		return openAIVideosAdapter{}, true, nil
	default:
		return nil, false, fmt.Errorf("unsupported %s type %q", CredentialKey, name)
	}
}

type nativeAdapter struct{}

func (nativeAdapter) Kind() Kind { return KindNative }
func (nativeAdapter) BuildURL(baseURL, requestID string, operation Operation) (string, error) {
	return buildURL(baseURL, requestID, operation, map[Operation]string{
		OperationCreate: "/videos/generations", OperationEdit: "/videos/edits",
		OperationExtension: "/videos/extensions", OperationStatus: "/videos/{id}",
		OperationContent: "/videos/{id}/content",
	})
}
func (nativeAdapter) PrepareRequest(_ Operation, body []byte, contentType string) (PreparedRequest, error) {
	return PreparedRequest{Body: body, ContentType: contentType}, nil
}
func (nativeAdapter) NormalizeResponse(_ Operation, body []byte, _, _ string) ([]byte, error) {
	return body, nil
}

type openAIVideosAdapter struct{}

func (openAIVideosAdapter) Kind() Kind { return KindOpenAIVideos }
func (openAIVideosAdapter) BuildURL(baseURL, requestID string, operation Operation) (string, error) {
	return buildURL(baseURL, requestID, operation, map[Operation]string{
		OperationCreate: "/videos", OperationEdit: "/videos/edits",
		OperationExtension: "/videos/extensions", OperationStatus: "/videos/{id}",
		OperationContent: "/videos/{id}/content",
	})
}

func (openAIVideosAdapter) PrepareRequest(operation Operation, body []byte, contentType string) (PreparedRequest, error) {
	if operation == OperationStatus || operation == OperationContent {
		return PreparedRequest{}, nil
	}
	if !json.Valid(body) {
		return PreparedRequest{}, errors.New("openai_videos adapter requires a JSON request body")
	}
	var source map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&source); err != nil {
		return PreparedRequest{}, fmt.Errorf("decode video request: %w", err)
	}
	target := make(map[string]any, 6)
	copyStringField(target, source, "model")
	copyStringField(target, source, "prompt")
	seconds := positiveInteger(source["duration"])
	if seconds <= 0 {
		seconds = positiveInteger(source["seconds"])
	}
	if seconds <= 0 {
		seconds = 8 // Grok's public default; do not inherit a vendor-specific default.
	}
	target["seconds"] = strconv.Itoa(seconds)
	size := openAIVideoSize(source)
	target["size"] = size
	if imageURL := firstImageURL(source); imageURL != "" {
		target["input_reference"] = map[string]any{"image_url": imageURL}
	}
	// Edit/extension payloads use the upstream protocol's video object unchanged.
	if operation == OperationEdit || operation == OperationExtension {
		if video, ok := source["video"]; ok {
			target["video"] = video
		}
	}
	out, err := json.Marshal(target)
	if err != nil {
		return PreparedRequest{}, fmt.Errorf("encode openai_videos request: %w", err)
	}
	return PreparedRequest{
		Body: out, ContentType: "application/json",
		VideoResolution: openAIVideoBillingResolution(size), VideoDurationSeconds: seconds,
	}, nil
}

func (openAIVideosAdapter) NormalizeResponse(operation Operation, body []byte, requestID, contentProxyURL string) ([]byte, error) {
	if operation == OperationContent {
		return body, nil
	}
	if !json.Valid(body) {
		return nil, errors.New("openai_videos upstream returned invalid JSON")
	}
	var source map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&source); err != nil {
		return nil, fmt.Errorf("decode openai_videos response: %w", err)
	}
	id := firstString(source, "id", "request_id", "task_id")
	if id == "" {
		id = strings.TrimSpace(requestID)
	}
	if id == "" && (operation == OperationCreate || operation == OperationEdit || operation == OperationExtension) {
		return nil, errors.New("openai_videos create response is missing a task ID")
	}
	status := normalizeOpenAIStatus(stringValue(source["status"]))
	result := map[string]any{"request_id": id, "status": status}
	if model := strings.TrimSpace(stringValue(source["model"])); model != "" {
		result["model"] = model
	}
	if operation == OperationStatus && status == "done" {
		video := map[string]any{"url": strings.TrimSpace(contentProxyURL)}
		if seconds := positiveInteger(source["seconds"]); seconds > 0 {
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

func buildURL(baseURL, requestID string, operation Operation, paths map[Operation]string) (string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return "", errors.New("video adapter base URL is required")
	}
	path, ok := paths[operation]
	if !ok {
		return "", fmt.Errorf("unsupported video adapter operation %q", operation)
	}
	if strings.Contains(path, "{id}") {
		requestID = strings.TrimSpace(requestID)
		if requestID == "" {
			return "", errors.New("video request ID is required")
		}
		path = strings.ReplaceAll(path, "{id}", url.PathEscape(requestID))
	}
	return baseURL + path, nil
}

func normalizeOpenAIStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "completed", "succeeded", "success", "done":
		return "done"
	case "failed", "error", "cancelled", "canceled", "expired":
		return "failed"
	default:
		return "pending"
	}
}

func openAIVideoSize(source map[string]any) string {
	if size := strings.TrimSpace(stringValue(source["size"])); strings.Contains(size, "x") {
		return size
	}
	landscape := true
	if ratio := strings.TrimSpace(stringValue(source["aspect_ratio"])); ratio != "" {
		parts := strings.SplitN(ratio, ":", 2)
		if len(parts) == 2 {
			width, _ := strconv.ParseFloat(parts[0], 64)
			height, _ := strconv.ParseFloat(parts[1], 64)
			landscape = width >= height
		}
	}
	switch strings.ToLower(strings.TrimSpace(stringValue(source["resolution"]))) {
	case "1080p":
		if landscape {
			return "1792x1024"
		}
		return "1024x1792"
	case "720p", "480p", "":
		if landscape {
			return "1280x720"
		}
		return "720x1280"
	default:
		return ""
	}
}

func openAIVideoBillingResolution(size string) string {
	switch strings.ToLower(strings.TrimSpace(size)) {
	case "1024x1792", "1792x1024":
		return "1080p"
	default:
		return "720p"
	}
}

func firstImageURL(source map[string]any) string {
	for _, key := range []string{"image", "images", "reference_images"} {
		if value, ok := source[key]; ok {
			if values, ok := value.([]any); ok {
				for _, item := range values {
					if found := imageURL(item); found != "" {
						return found
					}
				}
			} else if found := imageURL(value); found != "" {
				return found
			}
		}
	}
	return ""
}

func imageURL(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		if value := strings.TrimSpace(stringValue(typed["url"])); value != "" {
			return value
		}
		if nested, ok := typed["image_url"].(map[string]any); ok {
			return strings.TrimSpace(stringValue(nested["url"]))
		}
		return strings.TrimSpace(stringValue(typed["image_url"]))
	default:
		return ""
	}
}

func copyStringField(target, source map[string]any, key string) {
	if value := strings.TrimSpace(stringValue(source[key])); value != "" {
		target[key] = value
	}
}
func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	default:
		return ""
	}
}
func positiveInteger(value any) int {
	n, err := strconv.Atoi(strings.TrimSpace(stringValue(value)))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}
func firstString(source map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(stringValue(source[key])); value != "" {
			return value
		}
	}
	return ""
}
