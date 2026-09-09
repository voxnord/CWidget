"use strict";

const $ = id => document.getElementById(id);

const S = {
  left: "RUB", right: "KZT",
  amountSide: "left", // какое поле сейчас источник истины: "left" | "right"
  table: null, names: {},
  dict: {},  // словарь текущего языка (приходит с бэкенда)
  err: false, // состояние «нет связи» для бейджа
};

/* Снапшот СОХРАНЁННЫХ настроек. null, пока настройки не открывались. */
let saved = null;
let confirmCtx = null;

/* ---------------- Popup-поведение: скрытие при потере фокуса ---------------- */
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

function showMode(mode) {
  try { window.go.app.App.ShowMode(mode); } catch (e) { console.error(e); }
}

/* ------------------------------ i18n ------------------------------------ */
function t(key, ...args) {
  let s = S.dict[key];
  if (s == null) return key;
  args.forEach((a, i) => { s = s.replaceAll(`{${i}}`, String(a)); });
  return s;
}
/* Ошибки бэкенда приходят ключами: "err.iso" или "err.provider_unknown|ключ". */
function tErr(e) {
  const parts = String(e).split("|");
  const s = t(parts[0], ...parts.slice(1));
  return s === parts[0] ? String(e) : s;
}
/* Имя провайдера: перевод по ключу из словаря, иначе каноническое имя. */
function providerLabel(p) {
  const k = `provider.${p.key}`;
  const tr = t(k);
  return tr !== k ? tr : (p.name ?? p.key);
}
function providerName(tbl) {
  if (tbl?.source_key) return providerLabel({ key: tbl.source_key, name: tbl.source });
  return tbl?.source ?? "—";
}
/* Применить словарь ко всем помеченным элементам + перерисовать динамику. */
function applyDict(dict) {
  if (!dict) return;
  S.dict = dict;
  document.querySelectorAll("[data-i18n]").forEach(el => { el.textContent = t(el.dataset.i18n); });
  document.querySelectorAll("[data-i18n-title]").forEach(el => { el.title = t(el.dataset.i18nTitle); });
  document.title = t("app.title");

  // Провайдеры в открытом селекте настроек — переименовать на новом языке.
  const provSel = $("set-provider");
  if (provSel) {
    for (const o of provSel.options) {
      o.textContent = providerLabel({ key: o.value, name: o.dataset.name ?? o.value });
    }
  }

  // Динамические тексты главного окна.
  if (S.err) {
    $("upd-badge").textContent = t("rates.error");
  } else {
    applyUpdate({}); // перерисует бейдж (обн./кот.), строки курса и т.д.
  }
}

function applyTheme(th) {
  document.body.classList.toggle("light", th === "light");
}

/* ---------------------------- форматирование ---------------------------- */
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

/* ------------------------------ расчёты --------------------------------- */
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

function codesFallback(pref, names, taken) {
  names = names ?? {};
  if (pref && names[pref] && pref !== taken) return pref;
  for (const c of ["USD", "EUR", "RUB"]) {
    if (names[c] && c !== taken) return c;
  }
  const keys = Object.keys(names).sort((a, b) =>
    String(names[a]).localeCompare(String(names[b]), "ru"));
  return keys.find(c => c !== taken) ?? pref ?? "";
}

/* ------------------------------- отрисовка ------------------------------- */
/* recalc: пересчитывается ТОЛЬКО соседнее поле — то, в котором пользователь
 * не печатает (S.amountSide). Источник истины читается из самого поля. */
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

/* Нижняя кнопка ⇄: развернуть пару валют; число пользователя остаётся
 * в своём поле (и в роли источника), соседнее пересчитывается. */
function doSwap() {
  [S.left, S.right] = [S.right, S.left];
  $("sel-left").value = S.left;
  $("sel-right").value = S.right;
  recalc();
}

/* Верхняя кнопка ⇄: развернуть операцию — значения обмениваются между полями
 * вместе с валютами: 100 RUB → 525,3784 KZT  ⇒  525,3784 KZT → 100 RUB.
 * Новым источником становится левое поле (оно же — поле ввода) с тем числом,
 * что было выведено справа. Если справа нечего парсить (нет курса) —
 * просто разворачиваем пару. toPrecision(10) убирает float-шум. */
function doSwapAll() {
  const v = parseAmt($("out-result").value);
  [S.left, S.right] = [S.right, S.left];
  $("sel-left").value = S.left;
  $("sel-right").value = S.right;
  if (!isNaN(v)) {
    const clean = String(parseFloat(v.toPrecision(10)));
    S.amountSide = "left";
    $("in-amount").value = clean;
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

  const badge = $("upd-badge");
  if (S.table?.fetched_at) {
    let txt = `${t("badge.updated")} ${fmtTime(new Date(S.table.fetched_at))}`;
    const qd = fmtQuoteDate(S.table.quote_date);
    if (qd) txt += ` · ${t("badge.quoted")} ${qd}`;
    badge.textContent = txt;
    badge.classList.remove("err");
  }
  $("src-badge").textContent = providerName(S.table);

  if (Object.keys(S.names ?? {}).length && !pair(S.left, S.right)) {
    const nl = codesFallback(S.left, S.names);
    const nr = codesFallback(S.right, S.names, nl);
    if (nl && nr && nl !== nr) { S.left = nl; S.right = nr; }
  }
  fillSelect($("sel-left"), S.left);
  fillSelect($("sel-right"), S.right);
  recalc();
}

/* -------------------------------- тост ----------------------------------- */
let toastTimer = null;
function showToast(text) {
  const tt = $("toast");
  tt.textContent = text;
  tt.classList.add("show");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => tt.classList.remove("show"), 1600);
}

/* ---------------------- кнопка «Обновить» (окно) ------------------------- */
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

/* --------------------------- переключение режимов ------------------------ */
function setMode(mode) {
  $("view-main").hidden = mode === "settings";
  $("view-settings").hidden = mode === "main";
  if (mode === "main") {
    closeConfirm();
    recalc();
  }
}

function syncSettingsCurrencies() {
  if ($("view-settings").hidden) return;
  if (!$("confirm-overlay").hidden) return;
  const l = $("set-left").value;
  const r = $("set-right").value;
  const nl = codesFallback(l, S.names);
  const nr = codesFallback(r, S.names, nl);
  fillSelect($("set-left"), nl);
  fillSelect($("set-right"), nr);
  updateDirtyUI();
}

/* ------------- настройки: снапшот, «грязность», подтверждение ------------- */

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

/* Тумблер автозапуска применяется мгновенно и в «грязность» не входит. */
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

/* Применить всё выбранное (кроме автозапуска — он мгновенный). */
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
  fillSelect($("set-left"), st.config.base_currency);
  fillSelect($("set-right"), st.config.target_currency);
  const nl = codesFallback(st.config.base_currency, S.names);
  const nr = codesFallback(st.config.target_currency, S.names, nl);
  if (nl && nr) {
    fillSelect($("set-left"), nl);
    fillSelect($("set-right"), nr);
  }

  $("set-interval").value = saved.interval;
  $("set-autostart").setAttribute("aria-checked", String(st.autostart_enabled));

  const provSel = $("set-provider");
  provSel.innerHTML = "";
  for (const p of st.providers ?? []) {
    const o = document.createElement("option");
    o.value = p.key;
    o.textContent = providerLabel(p);   // перевод по словарю текущего языка
    o.dataset.name = p.name;            // каноническое имя — для переименования при смене языка
    provSel.appendChild(o);
  }
  provSel.value = saved.provider;

  $("set-theme").value = saved.theme;

  const langSel = $("set-lang");
  langSel.innerHTML = "";
  for (const l of st.languages ?? []) {
    const o = document.createElement("option");
    o.value = l.code;
    o.textContent = l.name; // самоназвание — не переводится
    langSel.appendChild(o);
  }
  langSel.value = saved.lang;

  $("ver").textContent = "v0.1.0";
  updateDirtyUI();
}

async function openSettings() {
  await populateSettings();
  showMode("settings");
}

/* ------------------------------ события UI ------------------------------- */
 $("in-amount").addEventListener("input", () => { S.amountSide = "left"; recalc(); });
 $("out-result").addEventListener("input", () => { S.amountSide = "right"; recalc(); });
 $("sel-left").addEventListener("change", e => { S.left = e.target.value; recalc(); });
 $("sel-right").addEventListener("change", e => { S.right = e.target.value; recalc(); });
 $("swap-amt").addEventListener("click", doSwapAll); // верхняя: суммы + валюты
 $("swap-cur").addEventListener("click", doSwap);    // нижняя: только валюты
 $("btn-refresh").addEventListener("click", () => window.go.app.App.ManualRefresh());
 $("btn-settings").addEventListener("click", () => openSettings());

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
  } catch (e) { alert(tErr(e)); }
});

 $("confirm-yes").addEventListener("click", async () => {
  const ctx = confirmCtx;
  try {
    await applyPending();
    closeConfirm();
    showToast(t("toast.applied"));
    if (ctx === "back") showMode("main");
  } catch (e) { alert(tErr(e)); } // диалог остаётся открытым
});
 $("confirm-no").addEventListener("click", () => {
  const ctx = confirmCtx;
  revertPending();
  closeConfirm();
  if (ctx === "back") showMode("main");
});

/* команды режима приходят с бэкенда (трей → ShowMode → событие) */
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

/* события от бэкенда */
window.runtime?.EventsOn?.("rates:update", p => {
  applyUpdate(p);
  syncSettingsCurrencies();
});
window.runtime?.EventsOn?.("rates:error", () => {
  S.err = true;
  $("upd-badge").textContent = t("rates.error");
  $("upd-badge").classList.add("err");
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
  fillSelect($("sel-left"), S.left); fillSelect($("sel-right"), S.right);
});

/* вкладки настроек */
document.querySelectorAll(".tab").forEach(btn => btn.addEventListener("click", () => {
  document.querySelectorAll(".tab").forEach(b => b.classList.remove("active"));
  btn.classList.add("active");
  $("tab-basic").hidden = btn.dataset.tab !== "basic";
  $("tab-about").hidden = btn.dataset.tab !== "about";
}));

 $("set-autostart").addEventListener("click", async () => {
  const st = await window.go.app.App.GetInitState();
  const nxt = !st.autostart_enabled;
  $("set-autostart").setAttribute("aria-checked", String(nxt));
  await window.go.app.App.SetAutoStart(nxt);
});

/* ------------------------------ старт ------------------------------------ */
(async function init() {
  try {
    const st = await window.go.app.App.GetInitState();
    applyDict(st.i18n);        // словарь ДО первого рендера
    applyTheme(st.config.theme);
    S.names = st.update.names ?? {};
    S.left = st.config.base_currency; S.right = st.config.target_currency;
    applyUpdate(st.update);
    setRefreshBusy(false);
    startCooldownUI(st.cooldown_sec | 0);
    setMode("main");
  } catch (e) { console.error("init:", e); }
})();