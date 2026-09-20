"use strict";

const $ = id => document.getElementById(id);

/* Global UI state:
 * left/right  — ISO codes of the currently displayed pair;
 * amountSide  — which input is the source of truth ("left" | "right");
 * table/names — the current snapshot and the code->name dictionary of the
 *               provider that actually answered;
 * provider    — the key of the SELECTED provider (may differ from the one
 *               that answered — see fallback);
 * fallback    — data comes from a fallback source;
 * favorites   — global favorite currency codes (provider-agnostic; codes
 *               unavailable at the current provider are simply not shown);
 * stayOpen    — keep the window open on focus loss (topmost while visible). */
const S = {
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
};

let saved = null;
let confirmCtx = null;

/* ------- Popup behavior: hide on focus loss (with an anti-race guard) ------- */
let hideGuardUntil = 0;
const armHideGuard = (ms = 700) => { hideGuardUntil = Date.now() + ms; };

window.addEventListener("blur", () => {
  setTimeout(() => {
    if (S.stayOpen) return; // the user pinned the window — no auto-hide
    if (Date.now() < hideGuardUntil) return;
    if (document.hasFocus()) return;
    try { window.runtime.WindowHide(); } catch {}
  }, 120);
});
window.addEventListener("focus", () => armHideGuard(300));

function showMode(mode) {
  try { window.go.app.App.ShowMode(mode); } catch (e) { reportToLog("error", "ShowMode failed: " + (e?.stack ?? e)); }
}

/* ------------------- frontend error bridge ------------------------------- */
let feLogCount = 0;
function reportToLog(level, msg) {
  if (feLogCount++ > 60) return;
  try { window.go.app.App.LogFrontend(level, String(msg).slice(0, 1000)); } catch {}
}
window.addEventListener("error", e =>
  reportToLog("error", `${e.message} @ ${e.filename}:${e.lineno}:${e.colno}`));
window.addEventListener("unhandledrejection", e =>
  reportToLog("error", "unhandled rejection: " + (e.reason?.stack ?? e.reason)));

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
function tErr(e) {
  const parts = String(e).split("|");
  const s = t(parts[0], ...parts.slice(1));
  return s === parts[0] ? String(e) : s;
}
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
function applyDict(dict) {
  if (!dict) return;
  S.dict = dict;
  document.querySelectorAll("[data-i18n]").forEach(el => { el.textContent = t(el.dataset.i18n); });
  document.querySelectorAll("[data-i18n-title]").forEach(el => { el.title = t(el.dataset.i18nTitle); });
  document.title = t("app.title");

  const provSel = $("set-provider");
  if (provSel) {
    for (const o of provSel.options) {
      o.textContent = providerLabel({ key: o.value, name: o.dataset.name ?? o.value });
    }
  }
  // Dropdown chrome is rendered by us: search placeholders and star tooltips
  // must follow the language immediately.
  for (const sel of currencySelects) {
    sel._cwidget.search.placeholder = t("ui.search");
    renderDropdownList(sel);
  }

  if (S.err) {
    $("upd-badge").textContent = t("rates.error");
  } else {
    applyUpdate({});
  }
  updateFallbackUI();
  refreshProviderStatuses();
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
const BLANK = "\u2800";

function fmtTime(d) {
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}
function fmtQuoteDate(iso) {
  if (!iso || iso.length < 10) return null;
  return `${iso.slice(8, 10)}.${iso.slice(5, 7)}`;
}

/* ------------- amount inputs with live thousands grouping ----------------- */
function cleanAmountString(s) {
  s = s.replace(/[^\d.,]/g, "").replace(/\./g, ",");
  const c = s.indexOf(",");
  if (c !== -1) s = s.slice(0, c + 1) + s.slice(c + 1).replace(/,/g, "");
  return s;
}
function groupThousands(s) {
  const c = s.indexOf(",");
  const intPart = c === -1 ? s : s.slice(0, c);
  const frac = c === -1 ? "" : s.slice(c);
  return intPart.replace(/\B(?=(\d{3})+(?!\d))/g, " ") + frac;
}
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
  if (s === null || s !== en) return;

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
 * window. Preference order: the preferred code itself, then the user's
 * favorites, then common USD/EUR/RUB, then anything else. STRICT guarantee:
 * the result exists in names and is NOT equal to taken; "" when none. */
function codesFallback(pref, names, taken) {
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

/* -------------------------------- rendering ------------------------------- */
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

function doSwap() {
  [S.left, S.right] = [S.right, S.left];
  $("sel-left").value = S.left;
  $("sel-right").value = S.right;
  syncDropdown($("sel-left"));
  syncDropdown($("sel-right"));
  recalc();
}

function doSwapAll() {
  const v = parseAmt($("out-result").value);
  [S.left, S.right] = [S.right, S.left];
  $("sel-left").value = S.left;
  $("sel-right").value = S.right;
  syncDropdown($("sel-left"));
  syncDropdown($("sel-right"));
  if (!isNaN(v)) {
    const clean = String(parseFloat(v.toPrecision(10)));
    S.amountSide = "left";
    const el = $("in-amount");
    el.value = clean;
    formatAmountInput(el);
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
  syncDropdown(sel);
}

function applyUpdate(p) {
  if (p?.table) S.table = p.table;
  if (p?.names) S.names = p.names;
  if (p?.table) S.err = false;
  if (p?.provider !== undefined) S.provider = p.provider;
  const prevFallback = S.wasFallback;
  S.fallback = !!(p?.fallback);
  S.wasFallback = S.fallback;
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
  $("src-badge").textContent = providerName(S.table);

  /* Automatic pair replacement — MAIN WINDOW ONLY. */
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
    closeDropdowns();
    closeConfirm();
    recalc();
  }
}

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

/* Confirmable fields only. The autostart and stay-open toggles apply
 * instantly and never mark the form dirty. */
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

function revertPending() {
  fillSelect($("set-left"), saved.base);
  fillSelect($("set-right"), saved.target);
  $("set-interval").value = saved.interval;
  $("set-provider").value = saved.provider;
  $("set-theme").value = saved.theme;
  $("set-lang").value = saved.lang;
  updateDirtyUI();
}

async function populateSettings() {
  const st = await window.go.app.App.GetInitState();
  saved = snapshotFromState(st);
  S.names = st.update.names ?? {};
  S.favorites = st.config.favorites ?? [];
  fillSelect($("set-left"), saved.base);
  fillSelect($("set-right"), saved.target);

  $("set-interval").value = saved.interval;
  $("set-autostart").setAttribute("aria-checked", String(st.autostart_enabled));
  $("set-stayopen").setAttribute("aria-checked", String(!!st.config.stay_open));

  const provSel = $("set-provider");
  provSel.innerHTML = "";
  for (const p of st.providers ?? []) {
    const o = document.createElement("option");
    o.value = p.key;
    o.textContent = providerLabel(p);
    o.dataset.name = p.name;
    provSel.appendChild(o);
  }
  provSel.value = saved.provider;

  $("set-theme").value = saved.theme;

  const langSel = $("set-lang");
  langSel.innerHTML = "";
  for (const l of st.languages ?? []) {
    const o = document.createElement("option");
    o.value = l.code;
    o.textContent = l.name;
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

/* Provider status line (see tray.go / manager.go for the data source). */
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

async function refreshProviderStatuses() {
  if ($("view-settings").hidden) return;
  try {
    const st = await window.go.app.App.GetInitState();
    renderProviderStatuses(st.provider_statuses);
  } catch (e) { reportToLog("error", "refreshProviderStatuses failed: " + (e?.stack ?? e)); }
}

/* --------------- favorites & custom currency dropdowns --------------------
 * A native <select> cannot host buttons inside its options, so each currency
 * select is enhanced: the select itself is hidden (it remains the state
 * holder and keeps firing change events for all existing handlers), and a
 * custom list is drawn on top — favorites duplicated at the top (the
 * provider's own order below is untouched), a star per row, a search filter.
 * Favorites are a GLOBAL list of ISO codes: provider-agnostic by design,
 * unavailable codes are just not rendered for the current source. */
const currencySelects = [];

function isFav(code) { return S.favorites.includes(code); }

async function toggleFavorite(code) {
  const next = isFav(code)
    ? S.favorites.filter(c => c !== code)
    : [...S.favorites, code];
  try {
    await window.go.app.App.SetFavorites(next);
    S.favorites = next;
    for (const sel of currencySelects) renderDropdownList(sel);
  } catch (e) { showError(e); }
}

function enhanceCurrencySelect(sel) {
  const wrap = document.createElement("div");
  wrap.className = "cwrap no-drag";
  sel.parentNode.insertBefore(wrap, sel);
  wrap.appendChild(sel); // move the select into the wrapper

  const btn = document.createElement("button");
  btn.type = "button";
  btn.className = "cbtn";
  btn.addEventListener("click", () => toggleDropdown(sel));

  const list = document.createElement("div");
  list.className = "clist";
  const search = document.createElement("input");
  search.type = "text";
  search.className = "csearch";
  search.addEventListener("input", () => filterDropdown(sel));
  search.addEventListener("keydown", e => {
    e.stopPropagation();
    if (e.key === "Escape") closeDropdowns();
  });
  const rows = document.createElement("div");
  rows.className = "crows";
  list.appendChild(search);
  list.appendChild(rows);
  wrap.appendChild(btn);
  //wrap.appendChild(list);

  document.body.appendChild(list);
  
  sel.classList.add("chidden");
  sel._cwidget = { wrap, btn, list, rows, search };
  currencySelects.push(sel);
  syncDropdown(sel);
}

function syncDropdown(sel) {
  if (!sel._cwidget) return;
  const o = sel.selectedOptions[0];
  sel._cwidget.btn.textContent = o ? o.textContent : "—";
}

/* Open/close + positioning. The list lives in <body> with position:fixed —
 * it is never clipped by the window and may extend past its bottom edge
 * (like native comboboxes). The height is fitted to the actual free space
 * (to the screen edge / taskbar), capped at 240px, so at least 4-5 rows are
 * always visible. If there is no room below, the list opens upward with its
 * bottom aligned to the button. */
function toggleDropdown(sel) {
  const w = sel._cwidget;
  const wasOpen = w.list.classList.contains("open");
  closeDropdowns();
  if (wasOpen) return;
  renderDropdownList(sel);
  w.list.classList.add("open");
  positionDropdown(sel);
  w.search.value = "";
  filterDropdown(sel);
  w.search.focus();
}

/* Move the list into <body> once and keep it anchored to the button. Fixed
 * coordinates are relative to the viewport, which is exactly the window's
 * inner area — the list may legally go past the bottom of the window. */
function positionDropdown(sel) {
  const w = sel._cwidget;
  if (w.list.parentNode !== document.body) document.body.appendChild(w.list);

  const r = w.wrap.getBoundingClientRect();
  const vw = window.innerWidth;
  const vh = window.innerHeight;

  const chrome = 56; // search input + paddings + borders
  const minRows = 4, rowH = 27, maxH = 240;
  const spaceBelow = vh - r.bottom - 6;  // free space under the button
  const spaceAbove = r.top - 6;          // free space above the button

  let h, below;
  if (spaceBelow >= chrome + minRows * rowH || spaceBelow >= spaceAbove) {
    below = true;
    h = Math.min(maxH, Math.max(minRows * rowH + chrome, spaceBelow));
  } else {
    below = false;
    h = Math.min(maxH, Math.max(minRows * rowH + chrome, spaceAbove));
  }
  w.rows.style.maxHeight = Math.max(80, h - chrome) + "px";

  let left = Math.max(4, Math.min(r.left, vw - r.width - 4));
  w.list.style.left = Math.round(left) + "px";
  w.list.style.width = Math.round(r.width) + "px";
  w.list.classList.toggle("up", !below);
  if (below) {
    w.list.style.top = Math.round(r.bottom + 4) + "px";
  } else {
    w.list.style.top = ""; // anchor from the bottom instead
    w.list.style.bottom = Math.round(vh - r.top + 4) + "px";
  }
}

function closeDropdowns() {
  for (const sel of currencySelects) sel._cwidget.list.classList.remove("open");
}
document.addEventListener("click", e => {
  if (!e.target.closest(".cwrap") && !e.target.closest(".clist")) closeDropdowns();
});
document.addEventListener("keydown", e => { if (e.key === "Escape") closeDropdowns(); });

function renderDropdownList(sel) {
  const w = sel._cwidget;
  if (!w) return;
  const opts = [...sel.options].map(o => ({ code: o.value, label: o.textContent }));
  const favs = S.favorites.map(c => opts.find(o => o.code === c)).filter(Boolean);
  const rest = opts.filter(o => !isFav(o.code));
  w.rows.innerHTML = "";
  for (const o of [...favs, ...rest]) {
    const row = document.createElement("div");
    row.className = "crow" + (o.code === sel.value ? " selected" : "");
    row.dataset.code = o.code;
    const name = document.createElement("span");
    name.className = "cname";
    name.textContent = o.label;
    const star = document.createElement("button");
    star.type = "button";
    star.className = "cstar" + (isFav(o.code) ? " on" : "");
    star.textContent = isFav(o.code) ? "★" : "☆";
    star.title = t(isFav(o.code) ? "fav.remove" : "fav.add");
    star.addEventListener("click", e => {
      e.stopPropagation(); // star a few currencies in a row — the list stays open
      toggleFavorite(o.code);
    });
    row.addEventListener("click", () => {
      sel.value = o.code;
      sel.dispatchEvent(new Event("change")); // existing handlers keep working
      syncDropdown(sel);
      closeDropdowns();
    });
    row.appendChild(name);
    row.appendChild(star);
    w.rows.appendChild(row);
  }
  filterDropdown(sel);
}

function filterDropdown(sel) {
  const w = sel._cwidget;
  const q = w.search.value.trim().toLowerCase();
  for (const row of w.rows.children) {
    const code = (row.dataset.code ?? "").toLowerCase();
    const name = row.querySelector(".cname")?.textContent.toLowerCase() ?? "";
    row.style.display = (!q || code.includes(q) || name.includes(q)) ? "" : "none";
  }
}

/* ------------------------------- UI events -------------------------------- */
for (const [el, side] of [[$("in-amount"), "left"], [$("out-result"), "right"]]) {
  el.addEventListener("input", () => {
    formatAmountInput(el, el.selectionStart);
    S.amountSide = side;
    recalc();
  });
  el.addEventListener("keydown", amountKeydown);
}
 $("sel-left").addEventListener("change", e => {
  S.left = e.target.value;
  S.pairOverride = null;
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
 $("btn-refresh").addEventListener("click", () => window.go.app.App.ManualRefresh());
 $("btn-settings").addEventListener("click", () => openSettings());
 $("btn-close").addEventListener("click", () => {
  try { window.runtime.WindowHide(); } catch {}
});

 $("btn-back").addEventListener("click", () => {
  if (computeDirty()) {
    openConfirm("back");
    return;
  }
  showMode("main");
});

for (const id of ["set-left", "set-right", "set-interval", "set-provider", "set-theme", "set-lang"]) {
  $(id).addEventListener("change", updateDirtyUI);
}

 $("btn-save").addEventListener("click", async () => {
  try {
    await applyPending();
    showToast(t("toast.saved"));
  } catch (e) { showError(e); }
});

 $("confirm-yes").addEventListener("click", async () => {
  const ctx = confirmCtx;
  try {
    await applyPending();
    closeConfirm();
    showToast(t("toast.applied"));
    if (ctx === "back") showMode("main");
  } catch (e) { showError(e); }
});
 $("confirm-no").addEventListener("click", () => {
  const ctx = confirmCtx;
  revertPending();
  closeConfirm();
  if (ctx === "back") showMode("main");
});

window.runtime?.EventsOn?.("ui:mode", m => {
  setMode(m);
  if (m === "settings") {
    if (computeDirty()) {
      openConfirm("reopen");
    } else {
      populateSettings();
    }
  }
});

/* Backend events */
window.runtime?.EventsOn?.("rates:update", p => {
  applyUpdate(p);
  refreshSettingsCurrencyLabels();
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
window.runtime?.EventsOn?.("stayopen:changed", on => { S.stayOpen = !!on; });
window.runtime?.EventsOn?.("favorites:changed", favs => {
  S.favorites = favs ?? [];
  for (const sel of currencySelects) renderDropdownList(sel);
});
window.runtime?.EventsOn?.("autostart:changed", on => {
  $("set-autostart").setAttribute("aria-checked", String(!!on));
});
window.runtime?.EventsOn?.("defaults:changed", cfg => {
  S.left = cfg.base_currency; S.right = cfg.target_currency;
  S.pairOverride = null;
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

/* Instant toggles (excluded from the confirmation flow). */
 $("set-autostart").addEventListener("click", async () => {
  const st = await window.go.app.App.GetInitState();
  const nxt = !st.autostart_enabled;
  $("set-autostart").setAttribute("aria-checked", String(nxt));
  try { await window.go.app.App.SetAutoStart(nxt); }
  catch (e) { showError(e); $("set-autostart").setAttribute("aria-checked", String(!nxt)); }
});
 $("set-stayopen").addEventListener("click", async () => {
  const st = await window.go.app.App.GetInitState();
  const nxt = !st.config.stay_open;
  $("set-stayopen").setAttribute("aria-checked", String(nxt));
  try { await window.go.app.App.SetStayOpen(nxt); }
  catch (e) { showError(e); $("set-stayopen").setAttribute("aria-checked", String(!nxt)); }
});

/* Enhance the currency selects before the first fill (the hidden native
 * selects stay the state holders; the custom dropdown draws on top). */
document.querySelectorAll("select.ccurrency").forEach(enhanceCurrencySelect);

/* --------------------------------- start ---------------------------------- */
(async function init() {
  try {
    const st = await window.go.app.App.GetInitState();
    applyDict(st.i18n);
    applyTheme(st.config.theme);
    S.names = st.update.names ?? {};
    S.favorites = st.config.favorites ?? [];
    S.stayOpen = !!st.config.stay_open;
    S.provider = st.provider ?? "";
    S.left = st.config.base_currency; S.right = st.config.target_currency;
    applyUpdate(st.update);
    setRefreshBusy(false);
    startCooldownUI(st.cooldown_sec | 0);
    setMode("main");
  } catch (e) {
    reportToLog("error", "init failed: " + (e?.stack ?? e));
    console.error("init:", e);
  }
})();