import { S } from "./state.js";
import { t, preferredName, providerName } from "./i18n.js";
import { money, parseAmt, fmtTime, fmtQuoteDate } from "./format.js";
import { pair, pairIn, trendDir, codesFallback, pickCachedTable } from "./math.js";
import { formatAmountInput } from "./amount.js";
import { $, setArr, showToast, updateFallbackUI } from "./ui.js";
import { fillSelect, syncAllDropdowns } from "./dropdown.js";

/* recalc: ONLY the field the user is not typing in gets recalculated
 * (see S.amountSide). The source of truth is read from the field itself. */
export function recalc() {
  const leftEl = $("in-amount");
  const rightEl = $("out-result");
  const calcLR = pair(S.left, S.right);
  const calcRL = pair(S.right, S.left);
  const amt = parseAmt(S.amountSide === "right" ? rightEl.value : leftEl.value);
  const ok = !isNaN(amt);

  if (S.amountSide === "right") {
    leftEl.value = (ok && calcRL) ? money(amt * calcRL.cur) : "";
  } else {
    rightEl.value = (ok && calcLR) ? money(amt * calcLR.cur) : "";
  }

  $("cap-l").textContent = calcLR ? `1 ${S.left} = ${money(calcLR.cur)} ${S.right}` : t("rates.unavailable");
  $("cap-r").textContent = calcRL ? `1 ${S.right} = ${money(calcRL.cur)} ${S.left}` : "";

  setArr($("arr-l"), trendDir(calcLR));
  setArr($("arr-r"), trendDir(calcRL));
}

// Bottom swap: reverse the pair; the user's number stays in its field.
function doSwap() {
  [S.left, S.right] = [S.right, S.left];
  $("sel-left").value = S.left;
  $("sel-right").value = S.right;
  syncAllDropdowns(); // the visible custom buttons must follow the hidden selects
  recalc();
}

// Top swap: reverse the whole operation — values and currencies together:
// 100 RUB -> 525.3784 KZT  =>  525.3784 KZT -> 100 RUB.
function doSwapAll() {
  const v = parseAmt($("out-result").value);
  [S.left, S.right] = [S.right, S.left];
  $("sel-left").value = S.left;
  $("sel-right").value = S.right;
  syncAllDropdowns();
  if (!isNaN(v)) {
    // toPrecision(10) strips float noise: 100.00000000000001 -> "100"
    const clean = String(parseFloat(v.toPrecision(10)));
    S.amountSide = "left";
    const el = $("in-amount");
    el.value = clean;
    formatAmountInput(el);
  }
  recalc();
}

export function applyUpdate(p) {
  // A REAL backend update carries the provider field; a pure redraw
  // (applyUpdate({}) after a language switch) does not and must not touch
  // the stored state.
  const real = p?.provider !== undefined;

  if (real) {
    // A null table is meaningful: the provider has just been switched and
    // the new source has neither answered nor has a fresh cached snapshot —
    // clear the displayed data instead of keeping the previous source's.
    S.table = p.table ?? null;
    if (p.snapshots) S.tables = p.snapshots;
    if (p.names) S.names = p.names;
    S.err = false;
    S.provider = p.provider;

    // Toast on live-fallback TRANSITION only — not on every refresh, not
    // at startup, not on redraws.
    const prev = S.liveFallback;
    S.liveFallback = !!(p.fallback);
    if (S.table && prev !== undefined && prev !== S.liveFallback) {
      if (S.liveFallback) showToast(t("toast.fallback.on", preferredName(), providerName(S.table)));
      else showToast(t("toast.fallback.off", preferredName()));
    }
  }

  // Determine the serving mode. The selected source being ALIVE is the
  // key fact: a live answer always wins, and per-pair cache digging is
  // FORBIDDEN — otherwise the previous source's cached snapshot would
  // shadow the newly selected source's data right after a switch.
  const disp0 = S.table;
  let disp = disp0;
  let fromCache = !!disp0?.from_cache;
  const selectedAlive = real && !S.liveFallback && !fromCache && !!disp0;

  // Per-pair cached refinement — ONLY while the selected source is DOWN
  // and a fallback answered live (the "urgently need a pair the fallback
  // cannot serve" scenario: CBR down, ECB answers, RUB/KZT come from the
  // CBR's own cached snapshot, marked as cached).
  if (!selectedAlive && disp && !pairIn(disp, S.left, S.right)) {
    const alt = pickCachedTable(S.left, S.right);
    if (alt) { disp = alt; fromCache = true; }
  }
  if (disp && disp !== disp0) {
    S.table = disp;
    if (disp.names) S.names = disp.names;
  }
  S.tableFromCache = fromCache;
  S.fallback = S.liveFallback || fromCache;

  // Automatic pair replacement — when a table EXISTS and does not cover
  // the pair:
  //   - selected alive: the pair follows the provider's coverage (switching
  //     to ECB turns RUB/KZT into an ECB-covered pair — exactly what the
  //     user expects after a switch);
  //   - degraded: last resort after the cache refinement found nothing.
  // With no table at all (a provider switch, fetching) — no replacement:
  // the UI shows its "unavailable" state until data arrives.
  if (disp && !pairIn(disp, S.left, S.right) && Object.keys(S.names ?? {}).length) {
    if (!S.pairOverride) S.pairOverride = { left: S.left, right: S.right };
    const nl = codesFallback(S.left, S.names);
    const nr = codesFallback(S.right, S.names, nl);
    if (nl && nr && nl !== nr) { S.left = nl; S.right = nr; }
  } else if (S.pairOverride && disp && pairIn(disp, S.pairOverride.left, S.pairOverride.right)) {
    S.left = S.pairOverride.left;
    S.right = S.pairOverride.right;
    S.pairOverride = null;
  }
  updateFallbackUI();

  const badge = $("upd-badge");
  if (S.table?.fetched_at) {
    let txt = `${t("badge.updated")} ${fmtTime(new Date(S.table.fetched_at))}`;
    const qd = fmtQuoteDate(S.table.quote_date);
    if (qd) txt += ` · ${t("badge.quoted")} ${qd}`;
    badge.textContent = txt;
    badge.classList.remove("err");
  }
  // Source badge: what the DISPLAYED data comes from. With no data yet
  // (a provider switch, fetching) — the selected provider we wait for.
  $("src-badge").textContent = S.table
    ? providerName(S.table) + (fromCache ? " " + t("badge.cached") : "")
    : preferredName();

  fillSelect($("sel-left"), S.left);
  fillSelect($("sel-right"), S.right);
  recalc();
}

export function wireConverter() {
  // Amount inputs: live formatting with caret anchoring + smart Backspace/
  // Delete around group separators.
  for (const [el, side] of [[$("in-amount"), "left"], [$("out-result"), "right"]]) {
    el.addEventListener("input", () => {
      formatAmountInput(el, el.selectionStart);
      S.amountSide = side;
      recalc();
    });
    el.addEventListener("keydown", e => {
      if (e.key !== "Backspace" && e.key !== "Delete") return;
      const s = el.selectionStart, en = el.selectionEnd;
      if (s === null || s !== en) return; // a selection exists — native behavior

      if (e.key === "Backspace") {
        if (s < 2 || el.value[s - 1] !== " ") return;
        e.preventDefault();
        el.value = el.value.slice(0, s - 2) + el.value.slice(s - 1);
        formatAmountInput(el, s - 1);
        recalc();
        return;
      }
      if (el.value[s] !== " " || s + 1 >= el.value.length) return;
      e.preventDefault();
      el.value = el.value.slice(0, s) + el.value.slice(s + 2);
      formatAmountInput(el, s);
      recalc();
    });
  }
  $("sel-left").addEventListener("change", e => {
    S.left = e.target.value;
    S.pairOverride = null; // a deliberate user choice — automation must not revert it
    updateFallbackUI();
    recalc();
  });
  $("sel-right").addEventListener("change", e => {
    S.right = e.target.value;
    S.pairOverride = null;
    updateFallbackUI();
    recalc();
  });
  $("swap-amt").addEventListener("click", doSwapAll);
  $("swap-cur").addEventListener("click", doSwap);
}