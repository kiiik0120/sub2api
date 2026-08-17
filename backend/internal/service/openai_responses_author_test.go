//go:build unit

package service

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestStripOpenAIResponsesInputAuthorsRemovesLongHistoryAuthor(t *testing.T) {
	items := make([]string, 25)
	for i := range items {
		items[i] = fmt.Sprintf(`{"type":"message","role":"assistant","content":"item %d"}`, i)
	}
	items[24] = `{"type":"function_call","call_id":"call_24","name":"spawn_agent","namespace":"collaboration","author":"agent-alpha","arguments":"{}"}`
	body := []byte(`{"model":"gpt-5.5","input":[` + strings.Join(items, ",") + `]}`)

	stripped, changed, err := stripOpenAIResponsesInputAuthors(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(stripped, "input.24.author").Exists())
	require.Equal(t, "collaboration", gjson.GetBytes(stripped, "input.24.namespace").String())
	require.Equal(t, "call_24", gjson.GetBytes(stripped, "input.24.call_id").String())
}

func TestStripOpenAIResponsesInputAuthorsLeavesNestedAuthorUntouched(t *testing.T) {
	body := []byte(`{"input":[{"type":"message","author":"remove","content":[{"type":"input_text","text":"hi","author":"nested"}]}]}`)

	stripped, changed, err := stripOpenAIResponsesInputAuthors(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(stripped, "input.0.author").Exists())
	require.Equal(t, "nested", gjson.GetBytes(stripped, "input.0.content.0.author").String())
}
