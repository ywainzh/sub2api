import { describe, expect, it } from "vitest";

import {
  buildModelsListConfig,
  countEffectiveStrictModels,
  createModelsListState,
  hydrateModelsListState,
  invertModelsListSelection,
  moveModelsListItem,
  selectAllModelsListItems,
  setModelsListCandidates,
  setModelsListEnforce,
  toggleModelsListItem,
} from "../groupsModelsList";

describe("groupsModelsList", () => {
  it("selects all default candidates for a new disabled config", () => {
    const state = createModelsListState();

    setModelsListCandidates(state, ["gpt-5.5", "gpt-5.4"]);

    expect(state.enabled).toBe(false);
    expect(state.enforce).toBe(false);
    expect(state.items).toEqual([
      { id: "gpt-5.5", selected: true },
      { id: "gpt-5.4", selected: true },
    ]);
  });

  it("keeps saved selections and marks new candidates as unselected when editing", () => {
    const state = createModelsListState({
      enabled: true,
      enforce: true,
      models: ["gpt-5.5", "gpt-5.4"],
    });

    setModelsListCandidates(state, ["gpt-5.4", "legacy-gpt", "gpt-5.5"]);

    expect(state.enabled).toBe(true);
    expect(state.enforce).toBe(true);
    expect(state.items).toEqual([
      { id: "gpt-5.5", selected: true },
      { id: "gpt-5.4", selected: true },
      { id: "legacy-gpt", selected: false },
    ]);
  });

  it("matches saved selections case-insensitively after candidates refresh", () => {
    const state = createModelsListState({
      enabled: false,
      enforce: true,
      models: [" GPT-5.6-TERRA "],
    });

    setModelsListCandidates(state, ["gpt-5.6-terra", "gpt-5.4"]);

    expect(state.items).toEqual([
      { id: "GPT-5.6-TERRA", selected: true },
      { id: "gpt-5.4", selected: false },
    ]);
  });

  it("preserves case-distinct legacy display models while enforcement is disabled", () => {
    const state = createModelsListState({
      enabled: true,
      enforce: false,
      models: ["Custom-Model", "custom-model"],
    });

    setModelsListCandidates(state, []);

    expect(buildModelsListConfig(state)).toEqual({
      enabled: true,
      enforce: false,
      models: ["Custom-Model", "custom-model"],
    });
  });

  it("deduplicates strict model IDs case-insensitively when building the payload", () => {
    const state = createModelsListState({
      enabled: false,
      enforce: false,
      models: ["Custom-Model", "custom-model"],
    });
    state.enforce = true;

    expect(buildModelsListConfig(state)).toEqual({
      enabled: false,
      enforce: true,
      models: ["Custom-Model"],
    });
  });

  it("collapses case variants immediately when strict enforcement is enabled", () => {
    const state = hydrateModelsListState(
      {
        enabled: true,
        enforce: false,
        models: ["Custom-Model", "custom-model"],
      },
      ["Custom-Model", "custom-model", "gpt-5.6-terra"],
    );
    state.items[1].selected = false;

    setModelsListEnforce(state, true);

    expect(state.enforce).toBe(true);
    expect(state.items).toEqual([
      { id: "Custom-Model", selected: true },
      { id: "gpt-5.6-terra", selected: false },
    ]);
    expect(state.savedModels).toEqual(["Custom-Model"]);
  });

  it("preserves explicitly unselected saved candidates when candidates refresh", () => {
    const state = createModelsListState({
      enabled: true,
      models: ["gpt-5.5"],
    });

    setModelsListCandidates(state, ["gpt-5.5", "gpt-5.4"]);

    expect(state.items).toEqual([
      { id: "gpt-5.5", selected: true },
      { id: "gpt-5.4", selected: false },
    ]);
  });

  it("builds config with selected models in current display order", () => {
    const state = hydrateModelsListState({
      enabled: true,
      models: ["gpt-5.5", "gpt-5.4", "legacy-gpt"],
    }, ["gpt-5.5", "gpt-5.4", "legacy-gpt"]);

    toggleModelsListItem(state, "legacy-gpt");
    moveModelsListItem(state, 1, 0);

    expect(buildModelsListConfig(state)).toEqual({
      enabled: true,
      enforce: false,
      models: ["gpt-5.4", "gpt-5.5"],
    });
  });

  it("keeps selected models in payload even when disabled so reopening can restore choices", () => {
    const state = hydrateModelsListState({
      enabled: false,
      models: ["gpt-5.5"],
    }, ["gpt-5.5", "gpt-5.4"]);

    expect(buildModelsListConfig(state)).toEqual({
      enabled: false,
      enforce: false,
      models: ["gpt-5.5"],
    });
  });

  it("preserves saved models when candidates have not loaded yet", () => {
    const state = createModelsListState({
      enabled: true,
      models: ["gpt-5.5", "gpt-5.4"],
    });

    expect(buildModelsListConfig(state)).toEqual({
      enabled: true,
      enforce: false,
      models: ["gpt-5.5", "gpt-5.4"],
    });
  });

  it("preserves strict enforcement independently from display settings", () => {
    const state = createModelsListState({
      enabled: false,
      enforce: true,
      models: [],
    });

    expect(buildModelsListConfig(state)).toEqual({
      enabled: false,
      enforce: true,
      models: [],
    });
  });

  it("treats wildcard-only selections as an empty effective strict allowlist", () => {
    const state = hydrateModelsListState(
      {
        enabled: true,
        enforce: true,
        models: ["gpt-*"],
      },
      ["gpt-*", "gpt-5.6-terra"],
    );

    expect(countEffectiveStrictModels(state)).toBe(0);
    state.items.find(item => item.id === "gpt-5.6-terra")!.selected = true;
    expect(countEffectiveStrictModels(state)).toBe(1);
  });

  it("keeps a persisted strict empty list empty after candidates load", () => {
    const state = createModelsListState({
      enabled: false,
      enforce: true,
      models: [],
    });

    setModelsListCandidates(state, ["gpt-5.6-terra", "gpt-5.4"]);

    expect(state.items).toEqual([
      { id: "gpt-5.6-terra", selected: false },
      { id: "gpt-5.4", selected: false },
    ]);
    expect(buildModelsListConfig(state)).toEqual({
      enabled: false,
      enforce: true,
      models: [],
    });
  });

  it("selects all candidate models from the toolbar action", () => {
    const state = hydrateModelsListState({
      enabled: true,
      models: ["gpt-5.5"],
    }, ["gpt-5.5", "gpt-5.4", "gpt-5.4-mini"]);

    selectAllModelsListItems(state);

    expect(state.items).toEqual([
      { id: "gpt-5.5", selected: true },
      { id: "gpt-5.4", selected: true },
      { id: "gpt-5.4-mini", selected: true },
    ]);
  });

  it("inverts selected models from the toolbar action", () => {
    const state = hydrateModelsListState({
      enabled: true,
      models: ["gpt-5.5"],
    }, ["gpt-5.5", "gpt-5.4", "gpt-5.4-mini"]);

    invertModelsListSelection(state);

    expect(state.items).toEqual([
      { id: "gpt-5.5", selected: false },
      { id: "gpt-5.4", selected: true },
      { id: "gpt-5.4-mini", selected: true },
    ]);
  });
});
