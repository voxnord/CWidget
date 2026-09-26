// Central UI state shared across modules.
// left/right   — ISO codes of the displayed pair;
// amountSide   — which input is the source of truth ("left" | "right");
// table/names  — the snapshot and code->name dictionary of the provider
//                that ACTUALLY answered;
// provider     — the key of the SELECTED provider (may differ — fallback);
// fallback     — data comes from a fallback source;
// pairOverride — the original pair temporarily replaced in the main window;
// favorites    — global favorite ISO codes (provider-agnostic);
// stayOpen     — keep the window open on focus loss (topmost while visible);
// lang         — UI language code: drives number/date formatting.
export const S = {
  left: "RUB", right: "KZT",
  amountSide: "left",
  table: null, names: {},
  dict: {},
  err: false,
  provider: "",
  fallback: false,
  wasFallback: null,
  pairOverride: null,
  favorites: [],
  stayOpen: false,
  lang: "ru",
};

// Module-private lets: cross-module access goes through accessors
// (ES module bindings cannot be assigned from importing files).
let saved = null;      // snapshot of the SAVED settings (null until opened)
let confirmCtx = null; // current confirmation dialog context

export function getSaved() { return saved; }
export function setSaved(v) { saved = v; }
export function getConfirmCtx() { return confirmCtx; }
export function setConfirmCtx(v) { confirmCtx = v; }