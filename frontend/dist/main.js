"use strict";

const $ = id => document.getElementById(id);

/* Global UI state:
 * left/right  — ISO codes of the currently displayed pair;
 * amountSide  — which input is the source of truth ("left" | "right");
 *               only the opposite field is ever recalculated, so the field
 *               the user types in is never overwritten programmatically;
 * table/names — the current snapshot and the code->name dictionary of the
 *               provider that actually answered;
 * provider    — the key of the SELECTED provider (may differ from the one
 *               that answered — see fallback);
 * fallback    — true when the selected provider is down and data comes from
 *               a fallback source;
 * pairOverride — the original pair temporarily replaced because it is
 *               unavailable at the current source (main window only). */
const S = {
  left: "RUB", right: "KZT",
  amountSide: "left",
  table: null, names: {},
  dict: {},           // dictionary of the current language (sent by the backend)
  err: false,         // "no connection" badge state
  provider: "",
  fallback: false,
  wasFallback: null,  // previous fallback value: transition detection for toasts
  pairOverride: null,
};

/* Snapshot of the SAVED settings. null until the settings were opened. */
let saved = null;
let confirmCtx = null;

/* ------- Popup behavior: hide on focus loss (with an anti-race guard) -------
 * Problem solved: right after clicking a tray menu item, Windows briefly
 * returns focus to the taskbar — a naive blur handler would hide the window
 * a split second after showing it. Solution: a grace period after show/focus
 * plus a hasFocus() re-check before hiding. */
let hideGuardUntil = 0;
const armHideGuard = (ms = 700) => { hideGuardUntil = Date.now() + ms; };

window.addEventListener("blur", () => {
  setTimeout(() => {
    if (Date.now() < hideGuardUntil) return;
    if (document.hasFocus()) return;
    try { window.runtime.WindowHide(); } catch {}
  }, 120);
});
window.addEventListener("focus", () => armHideGuard(300));

/* Window presentation goes through the backend (ShowMode): it sizes the
 * window per mode, places it near the tray and takes focus. */
function showMode(mode) {
  try { window.go.app.App.ShowMode(mode); } catch (e) { reportToLog("error", "ShowMode failed: " + (e?.stack ?? e)) }
}

/* ------------------- frontend error bridge -------------------------------
 * A production build has no DevTools console: without this bridge a JS
 * failure leaves no trace at all. Errors are forwarded to the backend log
 * (window.onerror + unhandledrejection), capped per session to survive a
 * pathological error loop. */
let feLogCount = 0;
function reportToLog(level, msg) {
  if (feLogCount++ > 60) return;
  try { window.go.app.App.LogFrontend(level, String(msg).slice(0, 1000)); } catch {}
}
window.addEventListener("error", e =>
  reportToLog("error", `${e.message} @ ${e.filename}:${e.lineno}:${e.colno}`));
window.addEventListener("unhandledrejection", e =>
  reportToLog("error", "unhandled rejection: " + (e.reason?.stack ?? e.reason)));

/* Log + surface an action error (used instead of a bare alert). */
function showError(e) {
  reportToLog("error", "action failed: " + (e?.stack ?? e));
  alert(tErr(e));
}

/* --------------------------------- i18n ---------------------------------- */
function t(key, ...args) {
  let s = S.dict[key];
  if (s == null) return key;
  args.forEach((a, i) => { s = s.replaceAll(`{${i}}`, String(a)); });
  return s;
}
/* Backend errors arrive as localization keys: "err.iso" or
 * "err.provider_unknown|<key>". */
function tErr(e) {
  const parts = String(e).split("|");
  const s = t(parts[0], ...parts.slice(1));
  return s === parts[0] ? String(e) : s;
}
/* Provider label: dictionary translation by key, otherwise the canonical
 * name from the adapter. */
function providerLabel(p) {
  const k = `provider.${p.key}`;
  const tr = t(k);
  return tr !== k ? tr : (p.name ?? p.key);
}
function providerName(tbl) {
  if (tbl?.source_key) return providerLabel({ key: tbl.source_key, name: tbl.source });
  return tbl?.source ?? "—";
}
function preferredName() {
  if (!S.provider) return "—";
  const k = `provider.${S.provider}`;
  const tr = t(k);
  return tr !== k ? tr : S.provider;
}
/* Degradation indicator: a warning mark near the source badge plus a dynamic
 * tooltip explaining where the data actually comes from. */
function updateFallbackUI() {
  const warn = $("src-warn");
  warn.hidden = !S.fallback;
  if (!S.fallback) { warn.title = ""; return; }
  let tip = t("warn.fallback", preferredName(), providerName(S.table));
  if (S.pairOverride) {
    tip += "\n" + t("warn.pair",
      S.pairOverride.left, S.pairOverride.right, S.left, S.right);
  }
  warn.title = tip;
}
/* Apply a dictionary to all marked elements, then redraw dynamic texts. */
function applyDict(dict) {
  if (!dict) return;
  S.dict = dict;
  document.querySelectorAll("[data-i18n]").forEach(el => { el.textContent = t(el.dataset.i18n); });
  document.querySelectorAll("[data-i18n-title]").forEach(el => { el.title = t(el.dataset.i18nTitle); });
  document.title = t("app.title");

  // Provider options in the open settings select — rename in place.
  const provSel = $("set-provider");
  if (provSel) {
    for (const o of provSel.options) {
      o.textContent = providerLabel({ key: o.value, name: o.dataset.name ?? o.value });
    }
  }

  if (S.err) {
    $("upd-badge").textContent = t("rates.error");
  } else {
    applyUpdate({}); // redraws the badge (upd./as of), rate lines, etc.
  }
  updateFallbackUI(); // the warning tooltip in the new language
}

function applyTheme(th) {
  document.body.classList.toggle("light", th === "light");
}

/* ------------------------------ formatting -------------------------------- */
function money(v) {
  if (!isFinite(v)) return "—";
  const a = Math.abs(v);
  const digits = a < 1 ? 6 : a < 1000 ? 4 : 2;
  return v.toLocaleString("ru-RU", { maximumFractionDigits: digits });
}
function parseAmt(s) {
  const v = parseFloat(String(s).replace(/\s/g, "").replace(",", "."));
  return isFinite(v) ? v : NaN;
}
const BLANK = "\u2800"; // braille blank: keeps layout width, draws nothing

function fmtTime(d) {
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}
function fmtQuoteDate(iso) { // "2026-09-10" -> "10.09"
  if (!iso || iso.length < 10) return null;
  return `${iso.slice(8, 10)}.${iso.slice(5, 7)}`;
}

/* ------------- amount inputs with live thousands grouping -----------------
 * The field is reformatted while typing: "24000,5" -> "24 000,5". The caret
 * is anchored to the number of digits to its left, so typing in the middle
 * of a number never jumps to the end. Backspace right after a group
 * separator deletes the DIGIT (not the separator), otherwise the separator
 * would "resurrect" on re-grouping and the digit could not be erased with
 * one keystroke; Delete right before a separator works symmetrically. */

function cleanAmountString(s) {
  s = s.replace(/[^\d.,]/g, "").replace(/\./g, ","); // digits and separators only
  const c = s.indexOf(",");                          // at most one comma
  if (c !== -1) s = s.slice(0, c + 1) + s.slice(c + 1).replace(/,/g, "");
  return s;
}

function groupThousands(s) {
  const c = s.indexOf(",");
  const intPart = c === -1 ? s : s.slice(0, c);
  const frac = c === -1 ? "" : s.slice(c); // fraction as typed
  return intPart.replace(/\B(?=(\d{3})+(?!\d))/g, " ") + frac;
}

/* formatAmountInput(el, caretPos): caretPos is the caret offset in el.value
 * BEFORE the rewrite (on an input event — the current selectionStart). */
function formatAmountInput(el, caretPos) {
  if (caretPos == null) caretPos = el.value.length;
  const raw = el.value;
  let digitsBefore = 0;
  for (let i = 0; i < caretPos && i < raw.length; i++) {
    if (raw[i] >= "0" && raw[i] <= "9") digitsBefore++;
  }
  const cleaned = cleanAmountString(raw);
  const formatted = groupThousands(cleaned);
  el.value = formatted;

  const totalDigits = (cleaned.match(/\d/g) || []).length;
  let newCaret;
  if (digitsBefore === 0) {
    newCaret = 0;
  } else if (digitsBefore >= totalDigits) {
    newCaret = formatted.length;
  } else {
    let seen = 0;
    newCaret = formatted.length;
    for (let i = 0; i < formatted.length; i++) {
      if (formatted[i] >= "0" && formatted[i] <= "9") {
        seen++;
        if (seen === digitsBefore) { newCaret = i + 1; break; }
      }
    }
  }
  el.setSelectionRange(newCaret, newCaret);
}

function amountKeydown(e) {
  if (e.key !== "Backspace" && e.key !== "Delete") return;
  const el = e.target;
  const s = el.selectionStart, en = el.selectionEnd;
  if (s === null || s !== en) return; // a selection exists — native behavior is fine

  if (e.key === "Backspace") {
    if (s < 2 || el.value[s - 1] !== " ") return;
    e.preventDefault();
    // remove the digit before the separator and the separator itself
    el.value = el.value.slice(0, s - 2) + el.value.slice(s - 1);
    formatAmountInput(el, s - 1);
    recalc();
    return;
  }

  // Delete: caret right before a separator -> remove the digit after it
  if (el.value[s] !== " " || s + 1 >= el.value.length) return;
  e.preventDefault();
  el.value = el.value.slice(0, s) + el.value.slice(s + 2);
  formatAmountInput(el, s);
  recalc();
}

/* --------------------------------- math ---------------------------------- */
function pair(a, b) {
  const t2 = S.table;
  if (!t2) return null;
  const f = t2.points?.[a], q = t2.points?.[b];
  if (!f || !q || !q.price) return null;
  const cur = f.price / q.price;
  let prev = null, hasPrev = false;
  if (f.has_prev && q.has_prev && q.prev) { prev = f.prev / q.prev; hasPrev = true; }
  return { cur, prev, hasPrev };
}
/* Trend direction: compare against yesterday's rate when the source provides
 * it; otherwise the blank symbol keeps the layout width. */
function trendDir(calc) {
  if (!calc || !calc.hasPrev) return null;
  const eps = Math.max(Math.abs(calc.prev), Math.abs(calc.cur)) * 1e-7;
  const d = calc.cur - calc.prev;
  if (Math.abs(d) <= eps) return null;
  return d > 0 ? "up" : "down";
}
function setArr(el, dir) {
  el.textContent = dir === "up" ? "\u2191" : dir === "down" ? "\u2193" : BLANK;
  el.className = "arr " + (dir ?? "flat");
}

/* Pick an available code for the automatic pair replacement in the MAIN
 * window. STRICT guarantee: the result exists in names and is NOT equal to
 * taken. Returns "" when there are no candidates (the caller decides).
 * History: the last branch used to be able to return pref === taken, both
 * selects ended up with the same code and the backend answered err.same_pair. */
function codesFallback(pref, names, taken) {
  names = names ?? {};
  const ok = c => c && c !== taken;
  if (pref && names[pref] && ok(pref)) return pref;
  for (const c of ["USD", "EUR", "RUB"]) {
    if (names[c] && ok(c)) return c;
  }
  const keys = Object.keys(names).sort((a, b) =>
    String(names[a]).localeCompare(String(names[b]), "ru"));
  for (const c of keys) {
    if (ok(c)) return c;
  }
  return "";
}

/* -------------------------------- rendering ------------------------------- */
/* recalc: ONLY the field the user is not typing in gets recalculated
 * (see S.amountSide). The source of truth is read from the field itself. */
function recalc() {
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

/* Bottom swap button: reverse the currency pair; the user's number stays in
 * its own field (and keeps its source-of-truth role). */
function doSwap() {
  [S.left, S.right] = [S.right, S.left];
  $("sel-left").value = S.left;
  $("sel-right").value = S.right;
  recalc();
}

/* Top swap button: reverse the whole operation — values are exchanged
 * between the fields together with the currencies:
 * 100 RUB -> 525.3784 KZT  =>  525.3784 KZT -> 100 RUB. */
function doSwapAll() {
  const v = parseAmt($("out-result").value);
  [S.left, S.right] = [S.right, S.left];
  $("sel-left").value = S.left;
  $("sel-right").value = S.right;
  if (!isNaN(v)) {
    // toPrecision(10) strips float noise: 100.00000000000001 -> "100"
    const clean = String(parseFloat(v.toPrecision(10)));
    S.amountSide = "left";
    const el = $("in-amount");
    el.value = clean;
    formatAmountInput(el); // immediately with thousands grouping
  }
  recalc();
}

function fillSelect(sel, value) {
  const codes = Object.keys(S.names).sort((a, b) =>
    String(S.names[a]).localeCompare(String(S.names[b]), "ru"));
  if (!codes.includes(value)) codes.unshift(value);
  sel.innerHTML = "";
  for (const c of codes) {
    const o = document.createElement("option");
    o.value = c;
    o.textContent = `${S.names[c] ? S.names[c] : c} (${c})`;
    sel.appendChild(o);
  }
  sel.value = value;
}

function applyUpdate(p) {
  if (p?.table) S.table = p.table;
  if (p?.names) S.names = p.names;
  if (p?.table) S.err = false;
  if (p?.provider !== undefined) S.provider = p.provider;
  const prevFallback = S.wasFallback;
  S.fallback = !!(p?.fallback);
  S.wasFallback = S.fallback;
  /* Toast on state TRANSITION only — not on every refresh, not at startup,
   * not on the applyUpdate({}) redraw after a language switch. */
  if (p?.table && prevFallback !== undefined && prevFallback !== S.fallback) {
    if (S.fallback) {
      showToast(t("toast.fallback.on", preferredName(), providerName(S.table)));
    } else {
      showToast(t("toast.fallback.off", preferredName()));
    }
  }

  const badge = $("upd-badge");
  if (S.table?.fetched_at) {
    let txt = `${t("badge.updated")} ${fmtTime(new Date(S.table.fetched_at))}`;
    const qd = fmtQuoteDate(S.table.quote_date);
    if (qd) txt += ` · ${t("badge.quoted")} ${qd}`;
    badge.textContent = txt;
    badge.classList.remove("err");
  }
  // The source badge is updated from the actual data — after a provider
  // switch the new name arrives by itself, no dedicated code needed.
  $("src-badge").textContent = providerName(S.table);

  /* Automatic pair replacement — MAIN WINDOW ONLY: the pair is unavailable
   * at the source -> a temporary replacement (the original is remembered);
   * the original becomes available again -> automatic return. The settings
   * are never touched by this logic. */
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
  updateFallbackUI(); // after the pair logic: the tooltip may mention the replacement

  fillSelect($("sel-left"), S.left);
  fillSelect($("sel-right"), S.right);
  recalc();
}

/* --------------------------------- toast ---------------------------------- */
let toastTimer = null;
function showToast(text) {
  const tt = $("toast");
  tt.textContent = text;
  tt.classList.add("show");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => tt.classList.remove("show"), 1600);
}

/* --------------------------- refresh button (window) ---------------------- */
let coolTimer = null;
let cooling = false;

function setRefreshBusy(busy) {
  const b = $("btn-refresh");
  if (busy) {
    clearInterval(coolTimer); cooling = false;
    b.disabled = true; b.classList.remove("cool");
    b.classList.add("spin"); b.textContent = "⟳"; b.title = t("tray.refresh.busy");
  } else {
    b.classList.remove("spin");
    if (!cooling) { b.disabled = false; b.textContent = "⟳"; b.title = t("tray.refresh"); }
  }
}
function startCooldownUI(sec) {
  const b = $("btn-refresh");
  clearInterval(coolTimer);
  if (!sec || sec <= 0) { endCooldownUI(); return; }
  cooling = true;
  b.disabled = true;
  b.classList.add("cool");
  const tick = () => {
    if (sec <= 0) { endCooldownUI(); return; }
    b.textContent = String(sec);
    b.title = t("ui.retry_in", sec);
    sec--;
  };
  tick();
  coolTimer = setInterval(tick, 1000);
}
function endCooldownUI() {
  clearInterval(coolTimer); cooling = false;
  const b = $("btn-refresh");
  b.classList.remove("cool", "spin");
  b.disabled = false;
  b.textContent = "⟳";
  b.title = t("tray.refresh");
}

/* ----------------------------- mode switching ----------------------------- */
function setMode(mode) {
  $("view-main").hidden = mode === "settings";
  $("view-settings").hidden = mode === "main";
  if (mode === "main") {
    closeConfirm();
    recalc();
  }
}

/* Refresh ONLY the currency labels in the open settings (fresh data arrived —
 * e.g. after a provider switch). Selected values are never touched: automatic
 * code replacement in the settings is forbidden (it used to be the source of
 * phantom dirty state and silent pair overwrites). */
function refreshSettingsCurrencyLabels() {
  if ($("view-settings").hidden) return;
  if (!$("confirm-overlay").hidden) return;
  for (const id of ["set-left", "set-right"]) {
    const sel = $(id);
    if (sel.value) fillSelect(sel, sel.value);
  }
  updateDirtyUI();
}

/* ------------- settings: snapshot, dirty tracking, confirmation ------------- */

function snapshotFromState(st) {
  return {
    base: st.config.base_currency,
    target: st.config.target_currency,
    interval: String(st.config.refresh_interval_minutes),
    provider: st.provider ?? "",
    theme: st.config.theme === "light" ? "light" : "dark",
    lang: st.lang ?? "",
  };
}

/* Unconfirmed edits? The autostart toggle is NOT included: it applies
 * instantly and never marks the form dirty. */
function computeDirty() {
  if (!saved) return false;
  return (
    $("set-left").value !== saved.base ||
    $("set-right").value !== saved.target ||
    $("set-interval").value !== saved.interval ||
    $("set-provider").value !== saved.provider ||
    $("set-theme").value !== saved.theme ||
    $("set-lang").value !== saved.lang
  );
}

function updateDirtyUI() {
  const dirty = computeDirty();
  $("btn-save").classList.toggle("dirty", dirty);
  return dirty;
}

function openConfirm(ctx) {
  confirmCtx = ctx;
  $("confirm-overlay").hidden = false;
}

function closeConfirm() {
  $("confirm-overlay").hidden = true;
  confirmCtx = null;
}

/* Apply everything selected (except autostart — it is instant). Exactly what
 * the controls show is saved — nothing is silently rewritten on the way to
 * the backend. */
async function applyPending() {
  const base = $("set-left").value;
  const target = $("set-right").value;
  const minutes = parseInt($("set-interval").value, 10);
  await window.go.app.App.SetDefaults(base, target, minutes);

  const provider = $("set-provider").value;
  if (provider !== saved.provider) await window.go.app.App.SetProvider(provider);

  const theme = $("set-theme").value;
  if (theme !== saved.theme) await window.go.app.App.SetTheme(theme);

  const lang = $("set-lang").value;
  if (lang !== saved.lang) await window.go.app.App.SetLanguage(lang);

  saved = { base, target, interval: String(minutes), provider, theme, lang };
  updateDirtyUI();
}

/* Roll the controls back to the saved values (the "No" button). */
function revertPending() {
  fillSelect($("set-left"), saved.base);
  fillSelect($("set-right"), saved.target);
  $("set-interval").value = saved.interval;
  $("set-provider").value = saved.provider;
  $("set-theme").value = saved.theme;
  $("set-lang").value = saved.lang;
  updateDirtyUI();
}

/* Fill the settings "from scratch" (no unsaved edits). The saved pair is
 * shown AS IS, with NO automatic replacement by "available" codes: if the
 * current source has no RUB/KZT (a fallback is answering), the codes stay in
 * the lists without a name — "(KZT)". That is honest: the settings reflect
 * the config, not momentary network availability. */
async function populateSettings() {
  const st = await window.go.app.App.GetInitState();
  saved = snapshotFromState(st);
  S.names = st.update.names ?? {};
  fillSelect($("set-left"), saved.base);
  fillSelect($("set-right"), saved.target);

  $("set-interval").value = saved.interval;
  $("set-autostart").setAttribute("aria-checked", String(st.autostart_enabled));

  // The provider list is built from the backend registry — a newly
  // registered provider appears here automatically, no extra code.
  const provSel = $("set-provider");
  provSel.innerHTML = "";
  for (const p of st.providers ?? []) {
    const o = document.createElement("option");
    o.value = p.key;
    o.textContent = providerLabel(p); // translated with the current language dictionary
    o.dataset.name = p.name;          // canonical name — for renaming on a language switch
    provSel.appendChild(o);
  }
  provSel.value = saved.provider;

  $("set-theme").value = saved.theme;

  // The language list comes from the backend: a new locales/*.json appears
  // here automatically.
  const langSel = $("set-lang");
  langSel.innerHTML = "";
  for (const l of st.languages ?? []) {
    const o = document.createElement("option");
    o.value = l.code;
    o.textContent = l.name; // endonym — never translated
    langSel.appendChild(o);
  }
  langSel.value = saved.lang;

  renderProviderStatuses(st.provider_statuses);
  $("ver").textContent = "v" + (st.version || "dev");
  updateDirtyUI();
}

async function openSettings() {
  await populateSettings();
  showMode("settings");
}

/* The provider status line in the settings: available / unavailable (time)
 * / not checked yet. The full text of the last error is in the tooltip. */
function renderProviderStatuses(list) {
  const el = $("provider-status");
  if (!el) return;
  el.innerHTML = "";
  for (const p of list ?? []) {
    const s = document.createElement("span");
    const label = providerLabel({ key: p.key, name: p.name });
    if (!p.tried) {
      s.className = "p-unk";
      s.textContent = `${label}: ${t("status.never")}`;
    } else if (p.ok) {
      s.className = "p-ok";
      const at = p.last_ok_at ? fmtTime(new Date(p.last_ok_at)) : "";
      s.textContent = `${label}: ${t("status.ok")}${at ? " · " + at : ""}`;
      s.title = at ? t("status.tip.ok", at) : "";
    } else {
      s.className = "p-err";
      const at = p.last_err_at ? fmtTime(new Date(p.last_err_at)) : "";
      s.textContent = `${label}: ${t("status.fail", at)}`;
      s.title = p.last_err ?? "";
    }
    el.appendChild(s);
  }
}

/* Refresh only the status line (settings open, an event arrived). */
async function refreshProviderStatuses() {
  if ($("view-settings").hidden) return;
  try {
    const st = await window.go.app.App.GetInitState();
    renderProviderStatuses(st.provider_statuses);
  } catch (e) { console.error(e); }
}

/* ------------------------------- UI events -------------------------------- */
for (const [el, side] of [[$("in-amount"), "left"], [$("out-result"), "right"]]) {
  el.addEventListener("input", () => {
    formatAmountInput(el, el.selectionStart); // caret BEFORE the value rewrite
    S.amountSide = side;
    recalc();
  });
  el.addEventListener("keydown", amountKeydown);
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
 $("btn-refresh").addEventListener("click", () => window.go.app.App.ManualRefresh());
 $("btn-settings").addEventListener("click", () => openSettings());

/* Leaving the settings: with edits — via confirmation, without — instantly. */
 $("btn-back").addEventListener("click", () => {
  if (computeDirty()) {
    openConfirm("back");
    return;
  }
  showMode("main");
});

/* Any change of a confirmable setting only marks the form dirty.
 * (Autostart is the exception — handled separately and applied instantly.) */
for (const id of ["set-left", "set-right", "set-interval", "set-provider", "set-theme", "set-lang"]) {
  $(id).addEventListener("change", updateDirtyUI);
}

 $("btn-save").addEventListener("click", async () => {
  try {
    await applyPending();
    showToast(t("toast.saved"));
  } catch (e) { showError(e); }
});

/* Confirmation dialog */
 $("confirm-yes").addEventListener("click", async () => {
  const ctx = confirmCtx;
  try {
    await applyPending();
    closeConfirm();
    showToast(t("toast.applied"));
    if (ctx === "back") showMode("main");
  } catch (e) { showError(e); } // a validation error keeps the dialog open
});
 $("confirm-no").addEventListener("click", () => {
  const ctx = confirmCtx;
  revertPending(); // "no" — nothing changes
  closeConfirm();
  if (ctx === "back") showMode("main");
});

/* Mode commands come from the backend (tray -> ShowMode -> event). */
window.runtime?.EventsOn?.("ui:mode", m => {
  setMode(m);
  if (m === "settings") {
    if (computeDirty()) {
      openConfirm("reopen"); // returned to unsent edits — ask
    } else {
      populateSettings();
    }
  }
});

/* Backend events */
window.runtime?.EventsOn?.("rates:update", p => {
  applyUpdate(p);
  refreshSettingsCurrencyLabels(); // labels only; values are untouched
  refreshProviderStatuses();
});
window.runtime?.EventsOn?.("rates:error", () => {
  S.err = true;
  $("upd-badge").textContent = t("rates.error");
  $("upd-badge").classList.add("err");
  refreshProviderStatuses();
});
window.runtime?.EventsOn?.("refresh:busy", b => setRefreshBusy(!!b));
window.runtime?.EventsOn?.("refresh:cooldown", s => startCooldownUI(s | 0));
window.runtime?.EventsOn?.("theme:changed", th => applyTheme(th));
window.runtime?.EventsOn?.("i18n:changed", p => applyDict(p?.dict));
window.runtime?.EventsOn?.("autostart:changed", on => {
  $("set-autostart").setAttribute("aria-checked", String(!!on));
});
window.runtime?.EventsOn?.("defaults:changed", cfg => {
  S.left = cfg.base_currency; S.right = cfg.target_currency;
  S.pairOverride = null; // the user pinned the pair deliberately
  fillSelect($("sel-left"), S.left); fillSelect($("sel-right"), S.right);
  updateFallbackUI();
});

/* Settings tabs */
document.querySelectorAll(".tab").forEach(btn => btn.addEventListener("click", () => {
  document.querySelectorAll(".tab").forEach(b => b.classList.remove("active"));
  btn.classList.add("active");
  $("tab-basic").hidden = btn.dataset.tab !== "basic";
  $("tab-about").hidden = btn.dataset.tab !== "about";
}));

/* The autostart toggle applies instantly (the exclusion from confirmations). */
 $("set-autostart").addEventListener("click", async () => {
  const st = await window.go.app.App.GetInitState();
  const nxt = !st.autostart_enabled;
  $("set-autostart").setAttribute("aria-checked", String(nxt));
  await window.go.app.App.SetAutoStart(nxt);
});

/* --------------------------------- start ---------------------------------- */
(async function init() {
  try {
    const st = await window.go.app.App.GetInitState();
    applyDict(st.i18n);        // the dictionary BEFORE the first render
    applyTheme(st.config.theme);
    S.names = st.update.names ?? {};
    S.left = st.config.base_currency; S.right = st.config.target_currency;
    applyUpdate(st.update);
    setRefreshBusy(false);
    startCooldownUI(st.cooldown_sec | 0); // the app was restarted mid-cooldown
    setMode("main"); // the window stays hidden until a tray click
  } catch (e) { 
    reportToLog("error", "init failed: " + (e?.stack ?? e));
    console.error("init:", e);  
  }
})();