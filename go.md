# Cursor2API Go 实现说明：OpenAI `chat/completions` 下的 thinking + tools

本文档只描述当前 Go 仓库已经实现或明确约束的行为，不再沿用旧的 Deno `/v1/messages` 迁移口径。

## 1. 当前范围

当前仓库只支持两个公开接口：

- `GET /v1/models`
- `POST /v1/chat/completions`

不实现 `/v1/messages`，也不承诺 Anthropic 原生 block/SSE 兼容。

## 2. 真实能力边界

### 2.1 公开模型目录

配置项 `MODELS` 只填写基础模型，例如：

```env
MODELS=claude-sonnet-4.6
```

服务会自动向外暴露两类模型：

- 基础模型：`claude-sonnet-4.6`
- thinking 派生模型：`claude-sonnet-4.6-thinking`

约定如下：

- 基础模型：保留现有模型名，启用 tool 能力。
- `-thinking` 模型：自动映射回基础模型出站，同时启用 thinking 和 tool 能力。
- 不允许继续派生 `-thinking-thinking`。

### 2.2 外部接口契约

当前实现的是 OpenAI `chat/completions` 兼容面：

- 请求支持 `messages`
- 请求支持 `tools`
- 请求支持 `tool_choice`
- assistant 历史消息支持 `tool_calls`
- tool 历史消息支持 `tool_call_id`
- 响应支持非流式 `message.tool_calls`
- 响应支持流式 `delta.tool_calls`

thinking 不作为 OpenAI 独立字段对外暴露。它只作为内部 prompt 协议与解析协议使用。

## 3. 请求侧链路

### 3.1 入口文件

- `handlers/handler.go`
- `services/cursor.go`
- `services/cursor_protocol.go`

### 3.2 处理步骤

1. `handlers.ChatCompletions` 绑定并校验 OpenAI 请求。
2. `CursorService.buildCursorRequest(...)` 解析模型能力：
   - `claude-sonnet-4.6` -> 基础模型
   - `claude-sonnet-4.6-thinking` -> 基础模型 + thinking 开启
3. `tool_choice` 被解析为三类模式：
   - `auto`
   - `none`
   - `required`
   - 或 `{"type":"function","function":{"name":"..."}}`
4. 工具定义被统一校验：
   - 仅支持 `type=function`
   - `function.name` 必填
   - 不允许重名
5. 构造单次请求专用 `TriggerSignal`，格式：

```text
<<CALL_xxxxxxxx>>
```

6. 构造发往 Cursor 的纯文本消息。

## 4. 内部 prompt 协议

上游 Cursor 仍然只收到文本消息，不使用原生 OpenAI tool calling。

### 4.1 tool 协议

当请求含有 `tools` 且 `tool_choice != "none"` 时，system message 会注入：

- 工具桥接说明
- tool 调用格式约束
- `<function_list>` 工具清单
- `required` / 指定 function 的额外约束

模型被要求按如下格式输出工具调用：

```text
<<CALL_xxxxxxxx>>
<invoke name="tool_name">{"arg":"value"}</invoke>
```

### 4.2 thinking 协议

当请求命中 `*-thinking` 模型时：

- system prompt 注入 thinking 规则
- 每条 user message 追加固定 thinking hint

hint 为：

```text
Use <thinking>...</thinking> for hidden reasoning when it helps. Keep your final visible answer outside the thinking tags.
```

### 4.3 历史消息回放

为了让多轮 tool loop 继续工作，OpenAI 历史消息会被回放成内部文本协议：

- assistant `tool_calls` -> `TriggerSignal + <invoke ...>`
- tool message -> `<tool_result id="...">...</tool_result>`

普通 `system/user/assistant` 文本则继续按 Cursor `parts[].text` 发送。

## 5. 响应解析链路

### 5.1 入口文件

- `services/cursor.go`
- `utils/cursor_protocol.go`
- `utils/utils.go`

### 5.2 解析步骤

1. `consumeSSE(...)` 读取 Cursor SSE。
2. 每个 `delta` 文本片段进入 `CursorProtocolParser`。
3. 解析器增量产出三类内部事件：
   - `text`
   - `thinking`
   - `tool_call`
4. `thinking` 只保留在内部事件层，不直接回写给客户端。

### 5.3 解析规则

- `<thinking>...</thinking>` 在 thinking 模型下会被消费为内部 thinking 事件。
- `TriggerSignal + <invoke ...>` 会被消费为工具调用事件。
- 不完整标签会在流结束时降级为普通文本，避免丢内容。
- 多个连续 `<invoke>` 会逐个产出，不再只保留第一个。

## 6. OpenAI 响应写回

### 6.1 非流式

非流式响应会在 `services.CursorService.ChatCompletionNonStream(...)` 中聚合内部事件：

- 只有文本时，返回普通 `message.content`
- 有工具调用时，返回 assistant `tool_calls`
- 若本轮出现工具调用，`finish_reason = "tool_calls"`
- 否则 `finish_reason = "stop"`

为提升与部分编排器（例如 Kilo Code）在“必须用工具”场景下的兼容性：

- 当请求含 `tools` 且被判定为“必须至少调用一次工具”（`tool_choice=required/指定函数`，或启用 `KILO_TOOL_STRICT`）时，
  如果第一轮没有产出任何 `tool_calls`，服务会自动重试 1 次（仅非流式）。

### 6.2 流式

`utils.StreamChatCompletion(...)` 会输出：

- 首个 role chunk：`assistant`
- 文本 chunk：`delta.content`
- 工具调用 chunk：`delta.tool_calls`
- 收尾 chunk：
  - 有工具调用 -> `finish_reason = "tool_calls"`
  - 无工具调用 -> `finish_reason = "stop"`

thinking 内容不会作为独立 OpenAI 字段透出。

## 7. 代码落点

本次能力集中在以下模块：

- `models/`
  - OpenAI request/response/tool 类型
  - thinking 模型派生规则
  - 基础模型到 Cursor 模型的映射
- `config/`
  - `MODELS` 基础模型配置
  - 自动扩展 `*-thinking` 公开模型目录
- `services/cursor_protocol.go`
  - `tool_choice` 解析
  - 工具定义校验
  - OpenAI 历史消息到 Cursor 文本协议的转换
- `utils/cursor_protocol.go`
  - Cursor 文本增量到 `text/thinking/tool_call` 事件的解析
- `utils/utils.go`
  - OpenAI 流式/非流式响应写回

## 8. 测试基线

当前测试覆盖以下关键路径：

- `MODELS` 自动扩展基础模型和 `-thinking` 模型
- thinking 模型回落到基础出站模型
- tool prompt 注入与 tool history 回放
- `<thinking>` / `<invoke>` 的增量解析
- 流式 `delta.tool_calls`
- 非流式 `message.tool_calls`
- 纯文本聊天回归不破坏

## 9. 非目标

当前不做以下内容：

- Anthropic `/v1/messages`
- MCP
- 原生 OpenAI tool execution
- 可见的 reasoning/thinking 对外字段
- 图像、文档、文件等多模态 block 协议
