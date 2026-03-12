// Copyright (c) 2025-2026 libaxuan
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package models

// ModelConfig 模型配置结构
type ModelConfig struct {
	ID            string `json:"id"`
	Provider      string `json:"provider"`
	MaxTokens     int    `json:"max_tokens"`
	ContextWindow int    `json:"context_window"`
	CursorModel   string `json:"cursor_model"`
}

// GetModelConfigs 获取所有基础模型配置
func GetModelConfigs() map[string]ModelConfig {
	return map[string]ModelConfig{
		"claude-sonnet-4.6": {
			ID:            "claude-sonnet-4.6",
			Provider:      "Anthropic",
			MaxTokens:     200000,
			ContextWindow: 200000,
			CursorModel:   "anthropic/claude-sonnet-4.6",
		},
	}
}

// GetModelConfig 获取指定模型的配置，支持公开 thinking 模型映射回基础模型
func GetModelConfig(modelID string) (ModelConfig, bool) {
	configs := GetModelConfigs()
	baseModel := TrimThinkingModel(modelID)
	config, exists := configs[baseModel]
	if !exists {
		return ModelConfig{}, false
	}

	config.ID = modelID
	return config, true
}

// GetCursorModel 获取Cursor API使用的模型名称
func GetCursorModel(modelID string) string {
	if config, exists := GetModelConfig(modelID); exists && config.CursorModel != "" {
		return config.CursorModel
	}
	return TrimThinkingModel(modelID)
}

// GetMaxTokensForModel 获取指定模型的最大token数
func GetMaxTokensForModel(modelID string) int {
	if config, exists := GetModelConfig(modelID); exists {
		return config.MaxTokens
	}
	return 4096
}

// GetContextWindowForModel 获取指定模型的上下文窗口大小
func GetContextWindowForModel(modelID string) int {
	if config, exists := GetModelConfig(modelID); exists {
		return config.ContextWindow
	}
	return 128000
}

// ValidateMaxTokens 验证并调整max_tokens参数
func ValidateMaxTokens(modelID string, requestedMaxTokens *int) *int {
	modelMaxTokens := GetMaxTokensForModel(modelID)

	if requestedMaxTokens == nil {
		return &modelMaxTokens
	}

	if *requestedMaxTokens > modelMaxTokens {
		return &modelMaxTokens
	}

	if *requestedMaxTokens <= 0 {
		return &modelMaxTokens
	}

	return requestedMaxTokens
}
