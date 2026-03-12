# API Capabilities

## Supported

- OpenAI-compatible `POST /v1/chat/completions`
- OpenAI-compatible `GET /v1/models`
- Non-stream responses with:
  - plain assistant text
  - assistant `tool_calls`
- Stream responses with:
  - `delta.content`
  - `delta.tool_calls`
- Multi-turn context via the `messages` array
- Tool request fields:
  - `tools`
  - `tool_choice`
  - assistant history `tool_calls`
  - tool role history `tool_call_id`
- Automatically derived `*-thinking` public models, for example:
  - `claude-sonnet-4.6`
  - `claude-sonnet-4.6-thinking`
- Bearer token auth via `Authorization: Bearer <API_KEY>`

## Behavior Notes

- Tool support is implemented through an internal prompt-and-parser bridge, not Cursor-native tool calling.
- Base models keep the current model name and enable tool use.
- `*-thinking` models map back to the same upstream base model, but also enable the internal thinking protocol.
- Thinking is an internal bridge capability only. The public OpenAI response does not expose a separate reasoning field.

## Not Supported

- Anthropic `/v1/messages`
- MCP orchestration
- Native upstream OpenAI tool execution
- Exposed reasoning/thinking response fields
- Local filesystem or OS command execution through the API

## Example: Non-Stream Tool Call

```bash
curl -X POST http://127.0.0.1:8002/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer 0000" \
  -d '{
    "model": "claude-sonnet-4.6",
    "stream": false,
    "messages": [
      {"role": "user", "content": "帮我查询北京天气"}
    ],
    "tools": [
      {
        "type": "function",
        "function": {
          "name": "get_weather",
          "description": "Get current weather",
          "parameters": {
            "type": "object",
            "properties": {
              "city": {"type": "string"}
            },
            "required": ["city"]
          }
        }
      }
    ]
  }'
```

## Example: Thinking Model

```bash
curl -X POST http://127.0.0.1:8002/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer 0000" \
  -d '{
    "model": "claude-sonnet-4.6-thinking",
    "stream": true,
    "messages": [
      {"role": "user", "content": "先思考，再决定是否需要工具"}
    ],
    "tools": [
      {
        "type": "function",
        "function": {
          "name": "lookup",
          "parameters": {
            "type": "object",
            "properties": {
              "q": {"type": "string"}
            },
            "required": ["q"]
          }
        }
      }
    ]
  }'
```
