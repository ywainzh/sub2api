package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// modelsDevFixture 复刻 models.dev 真实响应的关键形状：同一个展示名
// "Ox Alpha Free (Unlimited)" 在 Zen tier 下 ID 是 x-preview-f-free，
// 在 Go tier 下才是 ox-alpha-free。两个 tier 都有零成本模型。
const modelsDevFixture = `{
  "opencode": {
    "id": "opencode",
    "name": "OpenCode Zen",
    "models": {
      "x-preview-f-free":       {"id": "x-preview-f-free",       "name": "Ox Alpha Free (Unlimited)", "cost": {"input": 0, "output": 0}},
      "big-pickle":             {"id": "big-pickle",             "name": "Big Pickle",                "cost": {"input": 0, "output": 0}},
      "deepseek-v4-flash-free": {"id": "deepseek-v4-flash-free", "name": "DeepSeek V4 Flash",         "cost": {"input": 0, "output": 0}, "status": "deprecated"},
      "laguna-s-2.1-free":      {"id": "laguna-s-2.1-free",      "name": "Laguna S 2.1",              "cost": {"input": 0, "output": 0}, "status": "deprecated"},
      "retired-free":           {"id": "retired-free",           "name": "Retired Free",              "cost": {"input": 0, "output": 0}},
      "claude-fable-5":         {"id": "claude-fable-5",         "name": "Claude Fable 5",            "cost": {"input": 3, "output": 15}}
    }
  },
  "opencode-go": {
    "id": "opencode-go",
    "name": "OpenCode Go",
    "models": {
      "ox-alpha-free": {"id": "ox-alpha-free", "name": "Ox Alpha Free (Unlimited)", "cost": {"input": 0, "output": 0}}
    }
  }
}`

const zenLiveCatalogFixture = `{"object":"list","data":[
  {"id":"x-preview-f-free","object":"model","owned_by":"opencode"},
  {"id":"big-pickle","object":"model","owned_by":"opencode"},
  {"id":"deepseek-v4-flash-free","object":"model","owned_by":"opencode"},
  {"id":"laguna-s-2.1-free","object":"model","owned_by":"opencode"},
  {"id":"claude-fable-5","object":"model","owned_by":"opencode"}
]}`

func TestParseOpenCodeZeroCostModelIDsSkipsOtherTiers(t *testing.T) {
	ids, err := parseOpenCodeZeroCostModelIDs([]byte(modelsDevFixture))
	require.NoError(t, err)
	// 只取 provider "opencode"；Go tier 的 ox-alpha-free 必须进不来。
	require.NotContains(t, ids, "ox-alpha-free")
	require.Contains(t, ids, "x-preview-f-free")
	// 付费模型被价格过滤掉。
	require.NotContains(t, ids, "claude-fable-5")
	// status=deprecated 不参与过滤：存活与否交给实时目录判定。
	require.Contains(t, ids, "deepseek-v4-flash-free")
	require.Contains(t, ids, "laguna-s-2.1-free")
}

func TestParseOpenCodeLiveModelIDs(t *testing.T) {
	ids, err := parseOpenCodeLiveModelIDs([]byte(zenLiveCatalogFixture))
	require.NoError(t, err)
	require.Equal(t, []string{
		"big-pickle", "claude-fable-5", "deepseek-v4-flash-free", "laguna-s-2.1-free", "x-preview-f-free",
	}, ids)

	_, err = parseOpenCodeLiveModelIDs([]byte(`{"object":"list","data":[]}`))
	require.Error(t, err)
}

// 事故回归：抓展示名列再 slug 化会得到 ox-alpha-free，请求它返回 401 ModelError
// 并被当成凭据失效连锁禁用了 11 个账号。交集规则必须让它无法再进入注册表。
func TestOpenCodeModelIntersectionExcludesPoisonID(t *testing.T) {
	liveIDs, err := parseOpenCodeLiveModelIDs([]byte(zenLiveCatalogFixture))
	require.NoError(t, err)
	zeroCostIDs, err := parseOpenCodeZeroCostModelIDs([]byte(modelsDevFixture))
	require.NoError(t, err)

	ids := intersectOpenCodeModelIDs(liveIDs, zeroCostIDs)

	require.Equal(t, []string{
		"big-pickle", "deepseek-v4-flash-free", "laguna-s-2.1-free", "x-preview-f-free",
	}, ids)
	require.NotContains(t, ids, "ox-alpha-free")  // 只在 Go tier
	require.NotContains(t, ids, "claude-fable-5") // 实时可路由但收费
	require.NotContains(t, ids, "retired-free")   // 零成本但已不在实时目录
}

func TestIntersectOpenCodeModelIDsEmptyWhenDisjoint(t *testing.T) {
	require.Empty(t, intersectOpenCodeModelIDs([]string{"a"}, []string{"b"}))
	require.Empty(t, intersectOpenCodeModelIDs(nil, []string{"a"}))
	require.Empty(t, intersectOpenCodeModelIDs([]string{"a"}, nil))
}

func TestReplaceStrictReportsDiffAndAcceptsEmpty(t *testing.T) {
	registry := newOpenCodeFreeModelRegistry([]string{"ox-alpha-free", "big-pickle"})

	added, removed := registry.ReplaceStrict([]string{"big-pickle", "x-preview-f-free"})
	require.Equal(t, []string{"x-preview-f-free"}, added)
	require.Equal(t, []string{"ox-alpha-free"}, removed)
	require.False(t, registry.Has("ox-alpha-free"))
	require.True(t, registry.Has("x-preview-f-free"))

	// 与 Replace 不同：空输入会真的清空，所以只能在两个来源都成功后调用。
	_, removed = registry.ReplaceStrict(nil)
	require.ElementsMatch(t, []string{"big-pickle", "x-preview-f-free"}, removed)
	require.Empty(t, registry.IDs())

	// 对照：Replace 的空输入仍是 no-op，保护恢复缓存/基线两个调用方。
	registry.Replace([]string{"big-pickle"})
	registry.Replace(nil)
	require.Equal(t, []string{"big-pickle"}, registry.IDs())
}
