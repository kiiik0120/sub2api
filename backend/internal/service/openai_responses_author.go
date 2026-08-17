package service

import (
	"bytes"
	"fmt"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// stripOpenAIResponsesInputAuthors removes Codex's agent attribution metadata
// from standard Responses input items. The public Responses API does not accept
// author on replayed output items and rejects the whole request as
// "Unknown parameter: input[N].author".
func stripOpenAIResponsesInputAuthors(body []byte) ([]byte, bool, error) {
	if !bytes.Contains(body, []byte(`"author"`)) {
		return body, false, nil
	}
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body, false, nil
	}

	var rebuilt bytes.Buffer
	rebuilt.Grow(len(input.Raw))
	_ = rebuilt.WriteByte('[')
	changed := false
	first := true
	var stripErr error
	input.ForEach(func(_, item gjson.Result) bool {
		if !first {
			_ = rebuilt.WriteByte(',')
		}
		first = false
		itemBody := []byte(item.Raw)
		if item.IsObject() && item.Get("author").Exists() {
			itemBody, stripErr = sjson.DeleteBytes(itemBody, "author")
			if stripErr != nil {
				return false
			}
			changed = true
		}
		_, _ = rebuilt.Write(itemBody)
		return true
	})
	_ = rebuilt.WriteByte(']')
	if stripErr != nil {
		return body, false, fmt.Errorf("delete OpenAI input author: %w", stripErr)
	}
	if !changed {
		return body, false, nil
	}

	stripped, err := sjson.SetRawBytes(body, "input", rebuilt.Bytes())
	if err != nil {
		return body, false, fmt.Errorf("replace OpenAI input after author deletion: %w", err)
	}
	return stripped, true, nil
}
