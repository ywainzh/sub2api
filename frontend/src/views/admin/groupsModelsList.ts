export interface ModelsListConfig {
  enabled: boolean
  enforce: boolean
  models: string[]
}

export interface ModelsListItem {
  id: string
  selected: boolean
}

export interface ModelsListState {
  enabled: boolean
  enforce: boolean
  explicitEmptyStrictList: boolean
  savedModels: string[]
  items: ModelsListItem[]
}

export const createModelsListState = (
  config?: Partial<ModelsListConfig> | null,
): ModelsListState => {
  const enforce = config?.enforce ?? false
  const savedModels = normalizeModels(config?.models ?? [], enforce)
  return {
    enabled: config?.enabled ?? false,
    enforce,
    // A persisted strict empty list is intentional fail-closed state. Keep it
    // distinct from a brand-new form, whose legacy behavior selects all loaded
    // candidates by default.
    explicitEmptyStrictList: config?.enforce === true && savedModels.length === 0,
    savedModels,
    items: [],
  }
}

export const hydrateModelsListState = (
  config: Partial<ModelsListConfig> | null | undefined,
  candidates: string[],
): ModelsListState => {
  const state = createModelsListState(config)
  setModelsListCandidates(state, candidates)
  return state
}

export const setModelsListCandidates = (
  state: ModelsListState,
  candidates: string[],
) => {
  const normalizedCandidates = normalizeModels(candidates, state.enforce)
  const currentSelected = new Set(
    state.items.filter(item => item.selected).map(item => modelKey(item.id, state.enforce)),
  )
  const currentKnown = new Set(state.items.map(item => modelKey(item.id, state.enforce)))
  const savedSelected = new Set(state.savedModels.map(model => modelKey(model, state.enforce)))
  const hasExistingItems = state.items.length > 0
  const selectionOrder = normalizeModels(
    [
      ...state.items.map(item => item.id),
      ...state.savedModels,
      ...normalizedCandidates,
    ],
    state.enforce,
  )

  state.items = selectionOrder.map(id => {
    const key = modelKey(id, state.enforce)
    const selected = hasExistingItems
      ? currentSelected.has(key)
      : state.savedModels.length > 0
        ? savedSelected.has(key)
        : state.explicitEmptyStrictList
          ? false
          : normalizedCandidates.some(candidate => modelKey(candidate, state.enforce) === key)

    return {
      id,
      selected: selected && (currentKnown.has(key) || savedSelected.has(key) || state.savedModels.length === 0),
    }
  })
}

// Toggle strict matching without leaving duplicate case variants in the UI.
// The API treats strict entries case-insensitively, so collapsing them here
// keeps the visible selection and the payload in sync as soon as the switch is
// changed instead of only normalizing at save time.
export const setModelsListEnforce = (
  state: ModelsListState,
  enforce: boolean,
) => {
  if (state.enforce === enforce) {
    return
  }

  const previousItems = state.items
  state.enforce = enforce
  state.savedModels = normalizeModels(state.savedModels, enforce)

  const orderedIDs = normalizeModels(
    previousItems.map(item => item.id),
    enforce,
  )
  state.items = orderedIDs.map(id => {
    const key = modelKey(id, enforce)
    return {
      id,
      selected: previousItems.some(
        item => item.selected && modelKey(item.id, enforce) === key,
      ),
    }
  })
}

export const countEffectiveStrictModels = (state: ModelsListState): number => {
  const selected = state.items.length > 0
    ? state.items.filter(item => item.selected).map(item => item.id)
    : state.savedModels
  return normalizeModels(selected, true).filter(model => !model.includes("*")).length
}

export const toggleModelsListItem = (state: ModelsListState, modelID: string) => {
  const key = modelKey(modelID, state.enforce)
  const item = state.items.find(item => modelKey(item.id, state.enforce) === key)
  if (item) {
    item.selected = !item.selected
  }
}

export const selectAllModelsListItems = (state: ModelsListState) => {
  state.items.forEach(item => {
    item.selected = true
  })
}

export const invertModelsListSelection = (state: ModelsListState) => {
  state.items.forEach(item => {
    item.selected = !item.selected
  })
}

export const moveModelsListItem = (
  state: ModelsListState,
  fromIndex: number,
  toIndex: number,
) => {
  if (
    fromIndex === toIndex ||
    fromIndex < 0 ||
    toIndex < 0 ||
    fromIndex >= state.items.length ||
    toIndex >= state.items.length
  ) {
    return
  }
  const [item] = state.items.splice(fromIndex, 1)
  state.items.splice(toIndex, 0, item)
}

export const buildModelsListConfig = (state: ModelsListState): ModelsListConfig => ({
  enabled: state.enabled,
  enforce: state.enforce,
  models: normalizeModels(
    state.items.length > 0
      ? state.items.filter(item => item.selected).map(item => item.id)
      : [...state.savedModels],
    state.enforce,
  ),
})

const normalizeModels = (models: string[], caseInsensitive: boolean): string[] => {
  const seen = new Set<string>()
  const out: string[] = []
  for (const raw of models) {
    const model = raw.trim()
    const key = caseInsensitive ? model.toLowerCase() : model
    if (!model || seen.has(key)) {
      continue
    }
    seen.add(key)
    out.push(model)
  }
  return out
}

const modelKey = (model: string, caseInsensitive: boolean): string => {
  const normalized = model.trim()
  return caseInsensitive ? normalized.toLowerCase() : normalized
}
