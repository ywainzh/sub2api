package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

const (
	openCodeModelsDevURL      = "https://models.dev/api.json"
	openCodeModelsDevMaxBytes = int64(32 << 20)
)

// openCodeModelsDevProviderKeys 是 models.dev 中 OpenCode Zen tier 的 provider 键。
// 绝不能扩大到 "opencode-go"：两个 tier 存在展示名相同但 ID 不同的模型
// （Ox Alpha Free 在 Zen 下是 x-preview-f-free，在 Go 下是 ox-alpha-free），
// 把 Go tier 的 ID 混进 Zen 注册表会让请求收到 401 ModelError 并连锁禁用账号。
var openCodeModelsDevProviderKeys = []string{"opencode", "opencode-zen", "opencode_zen"}

type openCodeModelsDevCost struct {
	Input  *float64 `json:"input"`
	Output *float64 `json:"output"`
}

type openCodeModelsDevModel struct {
	ID   string                 `json:"id"`
	Cost *openCodeModelsDevCost `json:"cost"`
}

type openCodeModelsDevProvider struct {
	ID     string                            `json:"id"`
	Models map[string]openCodeModelsDevModel `json:"models"`
}

// parseOpenCodeZeroCostModelIDs 从 models.dev 目录中提取 OpenCode Zen 的零成本模型 ID。
//
// 只判定价格，不判 models.dev 的 deprecated/status 字段：存活与否由实时
// /zen/v1/models 决定，真退役的模型会自己从实时目录消失。models.dev 把 29 个
// 零成本模型中的 22 个标为 deprecated，其中包含实测仍可路由的 laguna-s-2.1-free
// 与 deepseek-v4-flash-free——用陈旧元数据过滤实时数据会误杀可用模型。
func parseOpenCodeZeroCostModelIDs(data []byte) ([]string, error) {
	var catalog map[string]json.RawMessage
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("decode models.dev catalog: %w", err)
	}
	for _, key := range openCodeModelsDevProviderKeys {
		raw, exists := catalog[key]
		if !exists {
			continue
		}
		var provider openCodeModelsDevProvider
		if err := json.Unmarshal(raw, &provider); err != nil {
			continue
		}
		ids := make([]string, 0, len(provider.Models))
		for mapKey, model := range provider.Models {
			if model.Cost == nil || model.Cost.Input == nil || model.Cost.Output == nil {
				continue
			}
			if *model.Cost.Input != 0 || *model.Cost.Output != 0 {
				continue
			}
			id := model.ID
			if id == "" {
				id = mapKey
			}
			if normalized := normalizeOpenCodeModelID(id); normalized != "" {
				ids = append(ids, normalized)
			}
		}
		if len(ids) > 0 {
			sort.Strings(ids)
			return ids, nil
		}
	}
	return nil, errors.New("models.dev contains no zero-cost OpenCode Zen models")
}

// intersectOpenCodeModelIDs 求「实时可路由」与「零成本」的交集，即真正可以对外开放的模型。
// 两侧都必须命中：只在实时目录里的是付费模型，只在 models.dev 里的已经下线。
func intersectOpenCodeModelIDs(liveIDs, zeroCostIDs []string) []string {
	zeroCost := make(map[string]struct{}, len(zeroCostIDs))
	for _, id := range zeroCostIDs {
		zeroCost[id] = struct{}{}
	}
	ids := make([]string, 0, len(liveIDs))
	seen := make(map[string]struct{}, len(liveIDs))
	for _, id := range liveIDs {
		if _, ok := zeroCost[id]; !ok {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
