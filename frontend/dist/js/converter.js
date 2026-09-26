import { S } from "./state.js";
import { t, preferredName, providerName } from "./i18n.js";
import { money, parseAmt, fmtTime, fmtQuoteDate } from "./format.js";
import { pair, trendDir, codesFallback } from "./math.js";
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

// Top swap: reverse the whole operation — values are exchanged between the
// fields together with the currencies:
// 100 RUB -> 525.3784 KZT  =>  525.3784 KZT -> 100 RUB.
function doSwapAll() {
  const v = parseAmt($("out-result").value);
  [S.left, S.right] = [S.right, S.left];
  $("sel-left").value = S.left;
  $("sel-right").value = S.right;
  syncAllDropdowns(); // the visible custom buttons must follow the hidden selects
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
  if (p?.table) S.table = p.table;
  if (p?.names) S.names = p.names;
  if (p?.table) S.err = false;
  if (p?.provider !== undefined) S.provider = p.provider;
  const prevFallback = S.wasFallback;
  S.fallback = !!(p?.fallback);
  S.wasFallback = S.fallback;
  // Toast on state TRANSITION only — not on every refresh, not at startup,
  // not on the applyUpdate({}) redraw after a language switch.
  if (p?.table && prevFallback !== undefined && prevFallback !== S.fallback) {
    if (S.fallback) showToast(t("toast.fallback.on", preferredName(), providerName(S.table)));
    else showToast(t("toast.fallback.off", preferredName()));
  }

  const badge = $("upd-badge");
  if (S.table?.fetched_at) {
    let txt = `${t("badge.updated")} ${fmtTime(new Date(S.table.fetched_at))}`;
    const qd = fmtQuoteDate(S.table.quote_date);
    if (qd) txt += ` · ${t("badge.quoted")} ${qd}`;
    badge.textContent = txt;
    badge.classList.remove("err");
  }
  // The source badge follows the actual data — after a provider switch the
  // new name arrives by itself.
  $("src-badge").textContent = providerName(S.table);

  // Automatic pair replacement — MAIN WINDOW ONLY.
  if (!pair(S.left, S.right) && Object.keys(S.names ?? {}).length) {
    if (!S.pairOverride) S.pairOverride = { left: S.left, right: S.right };
    const nl = codesFallback(S.left, S.names);
    const nr = codesFallback(S.right, S.names, nl);
    if (nl && nr && nl !== nr) { S.left = nl; S.right = nr; }
  } else if (S.pairOverride && pair(S.pairOverride.left, S.pairOverride.right)) {
    S.left = S.pairOverride.left;
    S.right = S.pairOverride.right;
    S.pairOverride = null;
  }
  updateFallbackUI();

  fillSelect($("sel-left"), S.left);
  fillSelect($("sel-right"), S.right);
  recalc();
}

export function wireConverter() {
  // Amount inputs: live formatting with caret anchoring + smart Backspace/
  // Delete around group separators.
  for (const [el, side] of [[$("in-amount"), "left"], [$("out-result"), "right"]]) {
    el.addEventListener("input", () => {
      formatAmountInput(el, el.selectionStart); // caret BEFORE the value rewrite
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
  $("swap-amt").addEventListener("click", doSwapAll); // top: amounts + currencies
  $("swap-cur").addEventListener("click", doSwap);    // bottom: currencies only
}