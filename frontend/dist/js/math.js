import { S } from "./state.js";

// pairIn: the a→b rate within a SPECIFIC table (today + yesterday).
export function pairIn(tbl, a, b) {
  if (!tbl) return null;
  const f = tbl.points?.[a], q = tbl.points?.[b];
  if (!f || !q || !q.price) return null;
  const cur = f.price / q.price;
  let prev = null, hasPrev = false;
  if (f.has_prev && q.has_prev && q.prev) { prev = f.prev / q.prev; hasPrev = true; }
  return { cur, prev, hasPrev };
}

export function pair(a, b) {
  return pairIn(S.table, a, b);
}

// Trend: compared against yesterday's rate when the source provides it.
export function trendDir(calc) {
  if (!calc || !calc.hasPrev) return null;
  const eps = Math.max(Math.abs(calc.prev), Math.abs(calc.cur)) * 1e-7;
  const d = calc.cur - calc.prev;
  if (Math.abs(d) <= eps) return null;
  return d > 0 ? "up" : "down";
}

// Pick the best CACHED snapshot covering the pair: the user's preferred
// source first, then the freshest quote date. Only snapshots within the
// one-day freshness window count (same rule as the backend's serving).
export function pickCachedTable(a, b) {
  const yday = new Date();
  yday.setDate(yday.getDate() - 1);
  const minDate = yday.toISOString().slice(0, 10);
  let best = null;
  for (const tbl of S.tables ?? []) {
    if (!tbl || !pairIn(tbl, a, b)) continue;
    if (!tbl.quote_date || tbl.quote_date < minDate) continue;
    if (tbl.source_key === S.provider) return tbl;
    if (!best || (tbl.quote_date ?? "") > (best.quote_date ?? "")) best = tbl;
  }
  return best;
}

// Pick an available code for the automatic pair replacement. Preference:
// the preferred code, then the user's favorites, then USD/EUR/RUB, then
// anything else. STRICT guarantee: the result exists in names and is NOT
// equal to taken; "" when there are no candidates.
export function codesFallback(pref, names, taken) {
  names = names ?? {};
  const ok = c => c && c !== taken;
  if (pref && names[pref] && ok(pref)) return pref;
  for (const c of S.favorites) if (names[c] && ok(c)) return c;
  for (const c of ["USD", "EUR", "RUB"]) if (names[c] && ok(c)) return c;
  const keys = Object.keys(names).sort((a, b) =>
    String(names[a]).localeCompare(String(names[b]), "ru"));
  for (const c of keys) if (ok(c)) return c;
  return "";
}