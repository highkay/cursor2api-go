package utils

import (
	"cursor2api-go/models"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCursorProtocolParserParsesThinkingAndToolCallsAcrossChunks(t *testing.T) {
	parser := NewCursorProtocolParser(models.CursorParseConfig{
		TriggerSignal:   "<<CALL_test>>",
		ThinkingEnabled: true,
	})

	var events []models.AssistantEvent
	events = append(events, parser.Feed("Hello <think")...)
	events = append(events, parser.Feed("ing>draft</thinking> world ")...)
	events = append(events, parser.Feed("<<CALL_test>>\n<invoke name=\"lookup\">{\"q\":\"hel")...)
	events = append(events, parser.Feed("lo\"}</invoke>!")...)
	events = append(events, parser.Finish()...)

	if len(events) != 5 {
		t.Fatalf("event count = %v, want 5", len(events))
	}
	if events[0].Kind != models.AssistantEventText || events[0].Text != "Hello " {
		t.Fatalf("event[0] = %#v, want text Hello", events[0])
	}
	if events[1].Kind != models.AssistantEventThinking || events[1].Thinking != "draft" {
		t.Fatalf("event[1] = %#v, want thinking draft", events[1])
	}
	if events[2].Kind != models.AssistantEventText || events[2].Text != " world " {
		t.Fatalf("event[2] = %#v, want text world", events[2])
	}
	if events[3].Kind != models.AssistantEventToolCall || events[3].ToolCall == nil {
		t.Fatalf("event[3] = %#v, want tool call", events[3])
	}
	if events[3].ToolCall.Function.Name != "lookup" {
		t.Fatalf("tool name = %v, want lookup", events[3].ToolCall.Function.Name)
	}
	if events[3].ToolCall.Function.Arguments != `{"q":"hello"}` {
		t.Fatalf("tool arguments = %v, want compact json", events[3].ToolCall.Function.Arguments)
	}
	if events[4].Kind != models.AssistantEventText || events[4].Text != "!" {
		t.Fatalf("event[4] = %#v, want trailing exclamation text", events[4])
	}
}

func TestNonStreamChatCompletionReturnsToolCalls(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	ch := make(chan interface{}, 4)
	ch <- models.AssistantEvent{Kind: models.AssistantEventText, Text: "Let me check."}
	ch <- models.AssistantEvent{
		Kind: models.AssistantEventToolCall,
		ToolCall: &models.ToolCall{
			ID:   "call_1",
			Type: "function",
			Function: models.FunctionCall{
				Name:      "lookup",
				Arguments: `{"q":"revivalquant"}`,
			},
		},
	}
	ch <- models.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
	close(ch)

	NonStreamChatCompletion(ctx, ch, "claude-sonnet-4.6")

	var response models.ChatCompletionResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if response.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("finish reason = %v, want tool_calls", response.Choices[0].FinishReason)
	}
	if response.Choices[0].Message.ToolCalls[0].Function.Name != "lookup" {
		t.Fatalf("tool call name = %v, want lookup", response.Choices[0].Message.ToolCalls[0].Function.Name)
	}
	if response.Choices[0].Message.Content != "Let me check." {
		t.Fatalf("message content = %#v, want Let me check.", response.Choices[0].Message.Content)
	}
}

func TestStreamChatCompletionEmitsToolCallChunks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	ch := make(chan interface{}, 2)
	ch <- models.AssistantEvent{
		Kind: models.AssistantEventToolCall,
		ToolCall: &models.ToolCall{
			ID:   "call_1",
			Type: "function",
			Function: models.FunctionCall{
				Name:      "lookup",
				Arguments: `{"q":"revivalquant"}`,
			},
		},
	}
	close(ch)

	StreamChatCompletion(ctx, ch, "claude-sonnet-4.6")

	body := recorder.Body.String()
	if !strings.Contains(body, `"tool_calls":[{"index":0,"id":"call_1","type":"function"`) {
		t.Fatalf("stream body missing tool_calls delta: %s", body)
	}
	if !strings.Contains(body, `"finish_reason":"tool_calls"`) {
		t.Fatalf("stream body missing tool_calls finish reason: %s", body)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Fatalf("stream body missing DONE marker: %s", body)
	}
}
