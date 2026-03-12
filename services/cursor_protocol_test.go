package services

import (
	"cursor2api-go/config"
	"cursor2api-go/models"
	"strings"
	"testing"
)

func TestBuildCursorRequestEnablesToolProtocolForBaseModel(t *testing.T) {
	service := &CursorService{
		config: &config.Config{
			SystemPromptInject: "Injected system prompt",
			MaxInputLength:     10000,
		},
	}

	request := &models.ChatCompletionRequest{
		Model: "claude-sonnet-4.6",
		Messages: []models.Message{
			{Role: "user", Content: "What's the weather?"},
		},
		Tools: []models.Tool{
			{
				Type: "function",
				Function: models.FunctionDefinition{
					Name:        "get_weather",
					Description: "Fetch current weather",
					Parameters: map[string]interface{}{
						"type": "object",
					},
				},
			},
		},
	}

	result, err := service.buildCursorRequest(request)
	if err != nil {
		t.Fatalf("buildCursorRequest() error = %v", err)
	}

	if result.Payload.Model != "anthropic/claude-sonnet-4.6" {
		t.Fatalf("Payload.Model = %v, want anthropic/claude-sonnet-4.6", result.Payload.Model)
	}
	if result.ParseConfig.TriggerSignal == "" {
		t.Fatalf("TriggerSignal should not be empty")
	}
	if result.ParseConfig.ThinkingEnabled {
		t.Fatalf("ThinkingEnabled = true, want false")
	}

	systemText := result.Payload.Messages[0].Parts[0].Text
	if !strings.Contains(systemText, "<function_list>") {
		t.Fatalf("system prompt does not include function list: %s", systemText)
	}
	if strings.Contains(systemText, thinkingHint) {
		t.Fatalf("system prompt should not include thinking hint for base model")
	}
}

func TestBuildCursorRequestThinkingModelFormatsToolHistory(t *testing.T) {
	service := &CursorService{
		config: &config.Config{
			MaxInputLength: 10000,
		},
	}

	request := &models.ChatCompletionRequest{
		Model: "claude-sonnet-4.6-thinking",
		Messages: []models.Message{
			{Role: "user", Content: "Plan first, then use tools."},
			{
				Role: "assistant",
				ToolCalls: []models.ToolCall{
					{
						ID:   "call_1",
						Type: "function",
						Function: models.FunctionCall{
							Name:      "lookup",
							Arguments: `{"q":"revivalquant"}`,
						},
					},
				},
			},
			{Role: "tool", ToolCallID: "call_1", Name: "lookup", Content: "Found result"},
		},
		Tools: []models.Tool{
			{
				Type: "function",
				Function: models.FunctionDefinition{
					Name: "lookup",
				},
			},
		},
	}

	result, err := service.buildCursorRequest(request)
	if err != nil {
		t.Fatalf("buildCursorRequest() error = %v", err)
	}

	if result.ParseConfig.TriggerSignal == "" {
		t.Fatalf("TriggerSignal should not be empty")
	}
	if !result.ParseConfig.ThinkingEnabled {
		t.Fatalf("ThinkingEnabled = false, want true")
	}
	if result.Payload.Model != "anthropic/claude-sonnet-4.6" {
		t.Fatalf("Payload.Model = %v, want anthropic/claude-sonnet-4.6", result.Payload.Model)
	}

	userText := result.Payload.Messages[1].Parts[0].Text
	if !strings.Contains(userText, thinkingHint) {
		t.Fatalf("user message should contain thinking hint, got: %s", userText)
	}

	assistantText := result.Payload.Messages[2].Parts[0].Text
	if !strings.Contains(assistantText, result.ParseConfig.TriggerSignal) {
		t.Fatalf("assistant tool history should include trigger signal, got: %s", assistantText)
	}
	if !strings.Contains(assistantText, `<invoke name="lookup">{"q":"revivalquant"}</invoke>`) {
		t.Fatalf("assistant tool history missing invoke block, got: %s", assistantText)
	}

	toolText := result.Payload.Messages[3].Parts[0].Text
	if !strings.Contains(toolText, `<tool_result id="call_1" name="lookup">Found result</tool_result>`) {
		t.Fatalf("tool result history missing tool_result block, got: %s", toolText)
	}
}

func TestBuildCursorRequestPreservesToolHistoryWithoutCurrentTools(t *testing.T) {
	service := &CursorService{
		config: &config.Config{
			MaxInputLength: 10000,
		},
	}

	request := &models.ChatCompletionRequest{
		Model:      "claude-sonnet-4.6",
		ToolChoice: []byte(`"none"`),
		Messages: []models.Message{
			{
				Role: "assistant",
				ToolCalls: []models.ToolCall{
					{
						ID:   "call_weather",
						Type: "function",
						Function: models.FunctionCall{
							Name:      "get_weather",
							Arguments: `{"city":"Beijing"}`,
						},
					},
				},
			},
			{Role: "tool", ToolCallID: "call_weather", Name: "get_weather", Content: "Sunny"},
			{Role: "user", Content: "Summarize the result."},
		},
	}

	result, err := service.buildCursorRequest(request)
	if err != nil {
		t.Fatalf("buildCursorRequest() error = %v", err)
	}

	if result.ParseConfig.TriggerSignal == "" {
		t.Fatalf("TriggerSignal should be kept for tool history replay")
	}

	systemText := result.Payload.Messages[0].Parts[0].Text
	if !strings.Contains(systemText, "completed history") {
		t.Fatalf("system prompt should explain historical tool transcript, got: %s", systemText)
	}
	if !strings.Contains(systemText, result.ParseConfig.TriggerSignal) {
		t.Fatalf("system prompt should include trigger signal, got: %s", systemText)
	}

	assistantText := result.Payload.Messages[1].Parts[0].Text
	if !strings.Contains(assistantText, result.ParseConfig.TriggerSignal) {
		t.Fatalf("assistant history should preserve trigger signal, got: %s", assistantText)
	}
}

func TestBuildCursorRequestAllowsToolChoiceNoneWithoutTools(t *testing.T) {
	service := &CursorService{
		config: &config.Config{
			MaxInputLength: 10000,
		},
	}

	request := &models.ChatCompletionRequest{
		Model:      "claude-sonnet-4.6",
		ToolChoice: []byte(`"none"`),
		Messages: []models.Message{
			{Role: "user", Content: "Hello"},
		},
	}

	result, err := service.buildCursorRequest(request)
	if err != nil {
		t.Fatalf("buildCursorRequest() error = %v", err)
	}
	if result.ParseConfig.TriggerSignal != "" {
		t.Fatalf("TriggerSignal = %q, want empty for plain chat", result.ParseConfig.TriggerSignal)
	}
	if len(result.Payload.Messages) != 1 {
		t.Fatalf("payload message count = %d, want 1", len(result.Payload.Messages))
	}
}

func TestBuildCursorRequestCountsSerializedToolCallsInMaxInputLength(t *testing.T) {
	service := &CursorService{
		config: &config.Config{
			MaxInputLength: 20,
		},
	}

	request := &models.ChatCompletionRequest{
		Model: "claude-sonnet-4.6",
		Messages: []models.Message{
			{
				Role: "assistant",
				ToolCalls: []models.ToolCall{
					{
						ID:   "call_1",
						Type: "function",
						Function: models.FunctionCall{
							Name:      "lookup",
							Arguments: `{"payload":"1234567890123456789012345678901234567890"}`,
						},
					},
				},
			},
			{Role: "user", Content: "Short"},
		},
	}

	result, err := service.buildCursorRequest(request)
	if err != nil {
		t.Fatalf("buildCursorRequest() error = %v", err)
	}

	for _, msg := range result.Payload.Messages {
		if strings.Contains(msg.Parts[0].Text, `"payload":"1234567890123456789012345678901234567890"`) {
			t.Fatalf("serialized tool call arguments should be removed by truncation, payload still contains long tool json: %#v", result.Payload.Messages)
		}
	}
	totalLength := 0
	for _, msg := range result.Payload.Messages {
		totalLength += len(msg.Parts[0].Text)
	}
	if totalLength == 0 {
		t.Fatalf("truncation should preserve at least one message")
	}
}
