// Central UI state shared across modules.
// table/tables — the SERVED snapshot and all per-source cached snapshots;
// tableFromCache — true when the displayed table came from the cache;
// liveFallback   — the selected source is down and a FALLBACK answered
//                  live (distinct from the cache state);
// fallback       — combined display flag: liveFallback || fromCache (⚠).
export const S = {
  left: "RUB", right: "KZT",
  amountSide: "left",
  table: null, tables: [], names: {},
  dict: {},
  err: false,
  provider: "",
  liveFallback: false,
  fallback: false,
  tableFromCache: false,
  pairOverride: null,
  favorites: [],
  stayOpen: false,
  lang: "ru",
};

let saved = null;      // snapshot of the SAVED settings (null until opened)
let confirmCtx = null; // current confirmation dialog context

export function getSaved() { return saved; }
export function setSaved(v) { saved = v; }
export function getConfirmCtx() { return confirmCtx; }
export function setConfirmCtx(v) { confirmCtx = v; }