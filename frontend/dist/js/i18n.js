import { S } from "./state.js";

export function t(key, ...args) {
  let s = S.dict[key];
  if (s == null) return key;
  args.forEach((a, i) => { s = s.replaceAll(`{${i}}`, String(a)); });
  return s;
}

// Backend errors arrive as localization keys: "err.iso" or
// "err.provider_unknown|<key>".
export function tErr(e) {
  const parts = String(e).split("|");
  const s = t(parts[0], ...parts.slice(1));
  return s === parts[0] ? String(e) : s;
}

// Provider label: dictionary translation by key, else the canonical name.
export function providerLabel(p) {
  const k = `provider.${p.key}`;
  const tr = t(k);
  return tr !== k ? tr : (p.name ?? p.key);
}

export function providerName(tbl) {
  if (tbl?.source_key) return providerLabel({ key: tbl.source_key, name: tbl.source });
  return tbl?.source ?? "—";
}

export function preferredName() {
  if (!S.provider) return "—";
  const k = `provider.${S.provider}`;
  const tr = t(k);
  return tr !== k ? tr : S.provider;
}