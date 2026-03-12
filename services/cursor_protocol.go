package services

import (
	"bytes"
	"cursor2api-go/middleware"
	"cursor2api-go/models"
	"cursor2api-go/utils"
	"encoding/json"
	"fmt"
	"strings"
)

const thinkingHint = "Use <thinking>...</thinking> for hidden reasoning when it helps. Keep your final visible answer outside the thinking tags."

type cursorBuildResult struct {
	Payload     models.CursorRequest
	ParseConfig models.CursorParseConfig
}

type toolChoiceSpec struct {
	Mode         string
	FunctionName string
}

func (s *CursorService) buildCursorRequest(request *models.ChatCompletionRequest) (cursorBuildResult, error) {
	capability := models.ResolveModelCapability(request.Model)
	toolChoice, err := parseToolChoice(request.ToolChoice)
	if err != nil {
		return cursorBuildResult{}, middleware.NewRequestValidationError(err.Error(), "invalid_tool_choice")
	}

	// Kilo Code 兼容：当上层编排器希望“必须用工具”时，即便 tool_choice=auto，
	// 也可以通过环境变量强制要求至少一次工具调用，避免 MODEL_NO_TOOLS_USED 一类的上层报错。
	if s.config != nil && s.config.KiloToolStrict && len(request.Tools) > 0 && toolChoice.Mode == "auto" {
		toolChoice.Mode = "required"
	}

	if len(request.Tools) == 0 && toolChoice.Mode != "auto" && toolChoice.Mode != "none" {
		return cursorBuildResult{}, middleware.NewRequestValidationError("tool_choice requires tools to be provided", "missing_tools")
	}

	if err := validateTools(request.Tools); err != nil {
		return cursorBuildResult{}, middleware.NewRequestValidationError(err.Error(), "invalid_tools")
	}

	if toolChoice.FunctionName != "" && !toolExists(request.Tools, toolChoice.FunctionName) {
		return cursorBuildResult{}, middleware.NewRequestValidationError(
			fmt.Sprintf("tool_choice references unknown function %q", toolChoice.FunctionName),
			"unknown_tool_choice_function",
		)
	}

	hasToolHistory := messagesContainToolHistory(request.Messages)
	toolProtocolEnabled := len(request.Tools) > 0 && toolChoice.Mode != "none"
	triggerSignal := ""
	if toolProtocolEnabled || hasToolHistory {
		triggerSignal = "<<CALL_" + utils.GenerateRandomString(8) + ">>"
	}

	cursorMessages := buildCursorMessages(
		request.Messages,
		s.config.SystemPromptInject,
		request.Tools,
		toolChoice,
		capability,
		hasToolHistory,
		triggerSignal,
	)
	cursorMessages = s.truncateCursorMessages(cursorMessages)

	payload := models.CursorRequest{
		Context:  []interface{}{},
		Model:    models.GetCursorModel(request.Model),
		ID:       utils.GenerateRandomString(16),
		Messages: cursorMessages,
		Trigger:  "submit-message",
	}

	return cursorBuildResult{
		Payload: payload,
		ParseConfig: models.CursorParseConfig{
			TriggerSignal:   triggerSignal,
			ThinkingEnabled: capability.ThinkingEnabled,
		},
	}, nil
}

func parseToolChoice(raw json.RawMessage) (toolChoiceSpec, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return toolChoiceSpec{Mode: "auto"}, nil
	}

	var choiceString string
	if err := json.Unmarshal(raw, &choiceString); err == nil {
		switch choiceString {
		case "auto", "none", "required":
			return toolChoiceSpec{Mode: choiceString}, nil
		default:
			return toolChoiceSpec{}, fmt.Errorf("unsupported tool_choice value %q", choiceString)
		}
	}

	var choiceObject models.ToolChoiceObject
	if err := json.Unmarshal(raw, &choiceObject); err != nil {
		return toolChoiceSpec{}, fmt.Errorf("tool_choice must be a string or function object")
	}

	if choiceObject.Type != "function" {
		return toolChoiceSpec{}, fmt.Errorf("unsupported tool_choice type %q", choiceObject.Type)
	}
	if choiceObject.Function == nil || strings.TrimSpace(choiceObject.Function.Name) == "" {
		return toolChoiceSpec{}, fmt.Errorf("tool_choice.function.name is required")
	}

	return toolChoiceSpec{
		Mode:         "function",
		FunctionName: strings.TrimSpace(choiceObject.Function.Name),
	}, nil
}

func validateTools(tools []models.Tool) error {
	seen := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		toolType := tool.Type
		if toolType == "" {
			toolType = "function"
		}
		if toolType != "function" {
			return fmt.Errorf("unsupported tool type %q", tool.Type)
		}

		name := strings.TrimSpace(tool.Function.Name)
		if name == "" {
			return fmt.Errorf("tool function name is required")
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("duplicate tool function name %q", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func toolExists(tools []models.Tool, name string) bool {
	for _, tool := range tools {
		if strings.TrimSpace(tool.Function.Name) == name {
			return true
		}
	}
	return false
}

func buildCursorMessages(
	messages []models.Message,
	systemPromptInject string,
	tools []models.Tool,
	toolChoice toolChoiceSpec,
	capability models.ModelCapability,
	hasToolHistory bool,
	triggerSignal string,
) []models.CursorMessage {
	result := make([]models.CursorMessage, 0, len(messages)+1)
	startIdx := 0
	systemSegments := make([]string, 0, 3)

	if len(messages) > 0 && strings.EqualFold(messages[0].Role, "system") {
		if systemText := strings.TrimSpace(messages[0].GetStringContent()); systemText != "" {
			systemSegments = append(systemSegments, systemText)
		}
		startIdx = 1
	}
	if inject := strings.TrimSpace(systemPromptInject); inject != "" {
		systemSegments = append(systemSegments, inject)
	}
	if protocolText := strings.TrimSpace(buildProtocolPrompt(tools, toolChoice, capability.ThinkingEnabled, hasToolHistory, triggerSignal)); protocolText != "" {
		systemSegments = append(systemSegments, protocolText)
	}
	if len(systemSegments) > 0 {
		result = append(result, newCursorTextMessage("system", strings.Join(systemSegments, "\n\n")))
	}

	for _, msg := range messages[startIdx:] {
		converted, ok := convertMessage(msg, capability.ThinkingEnabled, triggerSignal)
		if !ok {
			continue
		}
		result = append(result, converted)
	}

	return result
}

func buildProtocolPrompt(tools []models.Tool, toolChoice toolChoiceSpec, thinkingEnabled bool, hasToolHistory bool, triggerSignal string) string {
	var sections []string

	if len(tools) > 0 && triggerSignal != "" {
		var builder strings.Builder
		builder.WriteString("You may call external tools through the bridge below.\n")
		builder.WriteString("When you need a tool, output exactly in this format with no markdown fences:\n")
		builder.WriteString(triggerSignal)
		builder.WriteString("\n<invoke name=\"tool_name\">{\"arg\":\"value\"}</invoke>\n")
		builder.WriteString("Available tools:\n")
		builder.WriteString(renderFunctionList(tools))

		switch toolChoice.Mode {
		case "required":
			builder.WriteString("\nYou must call at least one tool before your final answer.")
			builder.WriteString("\nIMPORTANT: Your next assistant message MUST be a tool call using the exact format above. Do not include any natural language text in that message.")
		case "function":
			builder.WriteString(fmt.Sprintf("\nYou must call the function %q before your final answer.", toolChoice.FunctionName))
			builder.WriteString("\nIMPORTANT: Your next assistant message MUST be a tool call using the exact format above. Do not include any natural language text in that message.")
		}

		sections = append(sections, builder.String())
	} else if hasToolHistory && triggerSignal != "" {
		var builder strings.Builder
		builder.WriteString("Previous assistant tool calls in this conversation are serialized in the following format:\n")
		builder.WriteString(triggerSignal)
		builder.WriteString("\n<invoke name=\"tool_name\">{\"arg\":\"value\"}</invoke>\n")
		builder.WriteString("Previous tool results are serialized as <tool_result ...>...</tool_result>.\n")
		builder.WriteString("Treat those tool transcripts as completed history. Do not emit a new tool call unless a current tool list is provided.")
		sections = append(sections, builder.String())
	}

	if thinkingEnabled {
		sections = append(sections, thinkingHint)
	}

	return strings.Join(sections, "\n\n")
}

func messagesContainToolHistory(messages []models.Message) bool {
	for _, msg := range messages {
		if len(msg.ToolCalls) > 0 {
			return true
		}
		if strings.EqualFold(strings.TrimSpace(msg.Role), "tool") {
			return true
		}
	}
	return false
}

func renderFunctionList(tools []models.Tool) string {
	var builder strings.Builder
	builder.WriteString("<function_list>\n")
	for _, tool := range tools {
		schema := "{}"
		if len(tool.Function.Parameters) > 0 {
			if marshaled, err := json.MarshalIndent(tool.Function.Parameters, "", "  "); err == nil {
				schema = string(marshaled)
			}
		}

		builder.WriteString(fmt.Sprintf("<function name=\"%s\">\n", tool.Function.Name))
		if desc := strings.TrimSpace(tool.Function.Description); desc != "" {
			builder.WriteString(desc)
			builder.WriteString("\n")
		}
		builder.WriteString("JSON Schema:\n")
		builder.WriteString(schema)
		builder.WriteString("\n</function>\n")
	}
	builder.WriteString("</function_list>")
	return builder.String()
}

func convertMessage(msg models.Message, thinkingEnabled bool, triggerSignal string) (models.CursorMessage, bool) {
	role := strings.TrimSpace(msg.Role)
	if role == "" {
		return models.CursorMessage{}, false
	}

	switch role {
	case "tool":
		return newCursorTextMessage("user", formatToolResult(msg)), true
	case "assistant":
		text := strings.TrimSpace(msg.GetStringContent())
		segments := make([]string, 0, len(msg.ToolCalls)+1)
		if text != "" {
			segments = append(segments, text)
		}
		for _, toolCall := range msg.ToolCalls {
			segments = append(segments, formatAssistantToolCall(toolCall, triggerSignal))
		}
		if len(segments) == 0 {
			return models.CursorMessage{}, false
		}
		return newCursorTextMessage("assistant", strings.Join(segments, "\n\n")), true
	case "user":
		text := msg.GetStringContent()
		if thinkingEnabled {
			text = appendThinkingHint(text)
		}
		if strings.TrimSpace(text) == "" {
			return models.CursorMessage{}, false
		}
		return newCursorTextMessage("user", text), true
	default:
		text := msg.GetStringContent()
		if strings.TrimSpace(text) == "" {
			return models.CursorMessage{}, false
		}
		return newCursorTextMessage(role, text), true
	}
}

func appendThinkingHint(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return thinkingHint
	}
	return content + "\n\n" + thinkingHint
}

func formatAssistantToolCall(toolCall models.ToolCall, triggerSignal string) string {
	pieces := make([]string, 0, 2)
	if triggerSignal != "" {
		pieces = append(pieces, triggerSignal)
	}

	callType := toolCall.Type
	if callType == "" {
		callType = "function"
	}
	name := strings.TrimSpace(toolCall.Function.Name)
	if name == "" {
		name = "tool"
	}

	arguments := strings.TrimSpace(toolCall.Function.Arguments)
	if arguments == "" {
		arguments = "{}"
	}

	pieces = append(pieces, fmt.Sprintf("<invoke name=\"%s\">%s</invoke>", name, arguments))
	return strings.Join(pieces, "\n")
}

func formatToolResult(msg models.Message) string {
	content := msg.GetStringContent()
	id := strings.TrimSpace(msg.ToolCallID)
	name := strings.TrimSpace(msg.Name)

	var builder strings.Builder
	builder.WriteString("<tool_result")
	if id != "" {
		builder.WriteString(fmt.Sprintf(" id=\"%s\"", id))
	}
	if name != "" {
		builder.WriteString(fmt.Sprintf(" name=\"%s\"", name))
	}
	builder.WriteString(">")
	builder.WriteString(content)
	builder.WriteString("</tool_result>")
	return builder.String()
}

func newCursorTextMessage(role, text string) models.CursorMessage {
	return models.CursorMessage{
		Role: role,
		Parts: []models.CursorPart{
			{
				Type: "text",
				Text: text,
			},
		},
	}
}
