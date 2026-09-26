import { S } from "./state.js";

export function pair(a, b) {
  const t2 = S.table;
  if (!t2) return null;
  const f = t2.points?.[a], q = t2.points?.[b];
  if (!f || !q || !q.price) return null;
  const cur = f.price / q.price;
  let prev = null, hasPrev = false;
  if (f.has_prev && q.has_prev && q.prev) { prev = f.prev / q.prev; hasPrev = true; }
  return { cur, prev, hasPrev };
}

// Trend: compared against yesterday's rate when the source provides it;
// otherwise the caller renders a blank placeholder.
export function trendDir(calc) {
  if (!calc || !calc.hasPrev) return null;
  const eps = Math.max(Math.abs(calc.prev), Math.abs(calc.cur)) * 1e-7;
  const d = calc.cur - calc.prev;
  if (Math.abs(d) <= eps) return null;
  return d > 0 ? "up" : "down";
}

// Pick an available code for the automatic pair replacement in the MAIN
// window. Preference: the preferred code, then the user's favorites, then
// common USD/EUR/RUB, then anything else. STRICT guarantee: the result
// exists in names and is NOT equal to taken; "" when there are no candidates.
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