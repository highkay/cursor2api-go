package utils

import (
	"bytes"
	"cursor2api-go/models"
	"encoding/json"
	"strings"
)

const (
	thinkingStartTag = "<thinking>"
	thinkingEndTag   = "</thinking>"
	invokeEndTag     = "</invoke>"
)

// CursorProtocolParser 将 Cursor 的纯文本增量转换为内部文本/thinking/tool_call 事件
type CursorProtocolParser struct {
	config  models.CursorParseConfig
	pending string
}

// NewCursorProtocolParser 创建新的协议解析器
func NewCursorProtocolParser(config models.CursorParseConfig) *CursorProtocolParser {
	return &CursorProtocolParser{config: config}
}

// Feed 喂入一个上游增量片段
func (p *CursorProtocolParser) Feed(chunk string) []models.AssistantEvent {
	if chunk == "" {
		return nil
	}
	p.pending += chunk
	return p.extract(false)
}

// Finish 在流结束时刷新剩余缓冲
func (p *CursorProtocolParser) Finish() []models.AssistantEvent {
	events := p.extract(true)
	if p.pending != "" {
		events = append(events, models.AssistantEvent{
			Kind: models.AssistantEventText,
			Text: p.pending,
		})
		p.pending = ""
	}
	return events
}

func (p *CursorProtocolParser) extract(final bool) []models.AssistantEvent {
	events := make([]models.AssistantEvent, 0, 4)

	for len(p.pending) > 0 {
		idx, kind := p.findNextSpecial()
		if idx < 0 {
			keep := 0
			if !final {
				keep = p.partialStartKeep()
			}
			if len(p.pending) <= keep {
				break
			}
			text := p.pending[:len(p.pending)-keep]
			p.pending = p.pending[len(p.pending)-keep:]
			if text != "" {
				events = append(events, models.AssistantEvent{
					Kind: models.AssistantEventText,
					Text: text,
				})
			}
			continue
		}

		if idx > 0 {
			text := p.pending[:idx]
			p.pending = p.pending[idx:]
			if text != "" {
				events = append(events, models.AssistantEvent{
					Kind: models.AssistantEventText,
					Text: text,
				})
			}
			continue
		}

		switch kind {
		case models.AssistantEventThinking:
			if event, ok := p.tryParseThinking(final); ok {
				events = append(events, event)
				continue
			}
			if !final {
				return events
			}
		case models.AssistantEventToolCall:
			if event, ok := p.tryParseToolCall(final); ok {
				events = append(events, event)
				continue
			}
			if !final {
				return events
			}
		}

		events = append(events, models.AssistantEvent{
			Kind: models.AssistantEventText,
			Text: p.pending,
		})
		p.pending = ""
	}

	return events
}

func (p *CursorProtocolParser) findNextSpecial() (int, models.AssistantEventKind) {
	bestIdx := -1
	bestKind := models.AssistantEventText

	if p.config.ThinkingEnabled {
		if idx := strings.Index(p.pending, thinkingStartTag); idx >= 0 {
			bestIdx = idx
			bestKind = models.AssistantEventThinking
		}
	}

	if p.config.TriggerSignal != "" {
		if idx := strings.Index(p.pending, p.config.TriggerSignal); idx >= 0 && (bestIdx < 0 || idx < bestIdx) {
			bestIdx = idx
			bestKind = models.AssistantEventToolCall
		}
	}

	return bestIdx, bestKind
}

func (p *CursorProtocolParser) partialStartKeep() int {
	maxKeep := 0
	if p.config.ThinkingEnabled {
		maxKeep = max(maxKeep, longestPrefixSuffix(p.pending, thinkingStartTag))
	}
	if p.config.TriggerSignal != "" {
		maxKeep = max(maxKeep, longestPrefixSuffix(p.pending, p.config.TriggerSignal))
	}
	return maxKeep
}

func (p *CursorProtocolParser) tryParseThinking(final bool) (models.AssistantEvent, bool) {
	if !strings.HasPrefix(p.pending, thinkingStartTag) {
		return models.AssistantEvent{}, false
	}

	endIdx := strings.Index(p.pending[len(thinkingStartTag):], thinkingEndTag)
	if endIdx < 0 {
		if final {
			return models.AssistantEvent{
				Kind: models.AssistantEventText,
				Text: p.pending,
			}, true
		}
		return models.AssistantEvent{}, false
	}

	start := len(thinkingStartTag)
	end := start + endIdx
	content := p.pending[start:end]
	p.pending = p.pending[end+len(thinkingEndTag):]
	return models.AssistantEvent{
		Kind:     models.AssistantEventThinking,
		Thinking: content,
	}, true
}

func (p *CursorProtocolParser) tryParseToolCall(final bool) (models.AssistantEvent, bool) {
	if p.config.TriggerSignal == "" || !strings.HasPrefix(p.pending, p.config.TriggerSignal) {
		return models.AssistantEvent{}, false
	}

	endIdx := strings.Index(p.pending, invokeEndTag)
	if endIdx < 0 {
		if final {
			return models.AssistantEvent{
				Kind: models.AssistantEventText,
				Text: p.pending,
			}, true
		}
		return models.AssistantEvent{}, false
	}

	segment := p.pending[:endIdx+len(invokeEndTag)]
	call, ok := parseToolCallSegment(segment, p.config.TriggerSignal)
	p.pending = p.pending[endIdx+len(invokeEndTag):]
	if !ok {
		return models.AssistantEvent{
			Kind: models.AssistantEventText,
			Text: segment,
		}, true
	}

	return models.AssistantEvent{
		Kind:     models.AssistantEventToolCall,
		ToolCall: call,
	}, true
}

func parseToolCallSegment(segment, triggerSignal string) (*models.ToolCall, bool) {
	body := strings.TrimSpace(strings.TrimPrefix(segment, triggerSignal))
	if !strings.HasPrefix(body, "<invoke") {
		return nil, false
	}

	openEnd := strings.Index(body, ">")
	if openEnd < 0 {
		return nil, false
	}
	openTag := body[:openEnd+1]
	if !strings.HasSuffix(body, invokeEndTag) {
		return nil, false
	}

	name := extractInvokeName(openTag)
	if name == "" {
		return nil, false
	}

	args := strings.TrimSpace(body[openEnd+1 : len(body)-len(invokeEndTag)])
	if args == "" {
		args = "{}"
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(args)); err != nil {
		return nil, false
	}

	return &models.ToolCall{
		ID:   "call_" + GenerateRandomString(24),
		Type: "function",
		Function: models.FunctionCall{
			Name:      name,
			Arguments: compact.String(),
		},
	}, true
}

func extractInvokeName(openTag string) string {
	nameIdx := strings.Index(openTag, `name="`)
	if nameIdx < 0 {
		return ""
	}
	value := openTag[nameIdx+len(`name="`):]
	endIdx := strings.Index(value, `"`)
	if endIdx < 0 {
		return ""
	}
	return strings.TrimSpace(value[:endIdx])
}

func longestPrefixSuffix(text, token string) int {
	maxLen := min(len(text), len(token)-1)
	for size := maxLen; size > 0; size-- {
		if strings.HasSuffix(text, token[:size]) {
			return size
		}
	}
	return 0
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
