package models

import "strings"

// ThinkingModelSuffix 是自动派生的公开思维模型后缀
const ThinkingModelSuffix = "-thinking"

// ModelCapability 描述公开模型的能力视图
type ModelCapability struct {
	RequestedModel  string
	BaseModel       string
	ThinkingEnabled bool
	ToolCapable     bool
}

// ResolveModelCapability 解析公开模型名到内部基础模型与能力开关
func ResolveModelCapability(modelID string) ModelCapability {
	return ModelCapability{
		RequestedModel:  modelID,
		BaseModel:       TrimThinkingModel(modelID),
		ThinkingEnabled: IsThinkingModel(modelID),
		ToolCapable:     true,
	}
}

// ExpandModelList 将基础模型列表扩展为公开模型目录
func ExpandModelList(baseModels []string) []string {
	seen := make(map[string]struct{}, len(baseModels)*2)
	result := make([]string, 0, len(baseModels)*2)

	add := func(model string) {
		if model == "" {
			return
		}
		if _, exists := seen[model]; exists {
			return
		}
		seen[model] = struct{}{}
		result = append(result, model)
	}

	for _, model := range baseModels {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}

		add(model)
		if !IsThinkingModel(model) {
			add(ThinkingModelID(model))
		}
	}

	return result
}

// IsThinkingModel 判断是否为公开 thinking 模型
func IsThinkingModel(modelID string) bool {
	return strings.HasSuffix(strings.TrimSpace(modelID), ThinkingModelSuffix)
}

// TrimThinkingModel 去除公开 thinking 模型后缀
func TrimThinkingModel(modelID string) string {
	modelID = strings.TrimSpace(modelID)
	if IsThinkingModel(modelID) {
		return strings.TrimSuffix(modelID, ThinkingModelSuffix)
	}
	return modelID
}

// ThinkingModelID 生成公开 thinking 模型名
func ThinkingModelID(baseModel string) string {
	baseModel = strings.TrimSpace(baseModel)
	if baseModel == "" || IsThinkingModel(baseModel) {
		return baseModel
	}
	return baseModel + ThinkingModelSuffix
}
