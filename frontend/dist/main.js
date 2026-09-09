"use strict";

const $ = id => document.getElementById(id);

const S = {
  left: "RUB", right: "KZT", amount: "100",
  table: null, names: {},
};

/* Снапшот СОХРАНЁННЫХ настроек (что реально лежит в config.json / применено).
 * null, пока настройки ни разу не открывались в этой сессии. */
let saved = null;

/* Контекст диалога: 'back' — выход в конвертер по «Назад»,
 * 'reopen' — окно было скрыто с несохранёнными правками и открыто снова. */
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

/* -------------------------------- тема ----------------------------------- */
function applyTheme(t) {
  document.body.classList.toggle("light", t === "light");
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
  const t = S.table;
  if (!t) return null;
  const f = t.points?.[a], q = t.points?.[b];
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

/* Выбор доступного кода: предпочитаемый, если есть у источника;
 * иначе ходовые USD/EUR/RUB; иначе первый по алфавиту имён. */
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
function recalc() {
  const calcLR = pair(S.left, S.right);
  const amt = parseAmt($("in-amount").value);
  $("out-result").textContent =
    (!isNaN(amt) && calcLR) ? money(amt * calcLR.cur) : "—";

  $("cap-l").textContent = calcLR ? `1 ${S.left} = ${money(calcLR.cur)} ${S.right}` : "курс недоступен";
  const calcRL = pair(S.right, S.left);
  $("cap-r").textContent = calcRL ? `1 ${S.right} = ${money(calcRL.cur)} ${S.left}` : "";

  setArr($("arr-l"), trendDir(calcLR));
  setArr($("arr-r"), trendDir(calcRL));
}

function doSwap() {
  [S.left, S.right] = [S.right, S.left];
  $("sel-left").value = S.left;
  $("sel-right").value = S.right;
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
  if (p?.names) S.names = p.names; // строгая замена: набор валют текущего провайдера
  const badge = $("upd-badge");
  if (S.table?.fetched_at) {
    let txt = `обн. ${fmtTime(new Date(S.table.fetched_at))}`;
    const qd = fmtQuoteDate(S.table.quote_date);
    if (qd) txt += ` · кот. ${qd}`;
    badge.textContent = txt;
    badge.classList.remove("err");
  }
  $("src-badge").textContent = S.table?.source ?? "—";

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
  const t = $("toast");
  t.textContent = text;
  t.classList.add("show");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.remove("show"), 1600);
}

/* ---------------------- кнопка «Обновить» (окно) ------------------------- */
let coolTimer = null;
let cooling = false;

function setRefreshBusy(busy) {
  const b = $("btn-refresh");
  if (busy) {
    clearInterval(coolTimer); cooling = false;
    b.disabled = true; b.classList.remove("cool");
    b.classList.add("spin"); b.textContent = "⟳"; b.title = "Обновление…";
  } else {
    b.classList.remove("spin");
    if (!cooling) { b.disabled = false; b.textContent = "⟳"; b.title = "Обновить курс"; }
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
    b.title = `Повтор через ${sec} с`;
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
  b.title = "Обновить курс";
}

/* --------------------------- переключение режимов ------------------------ */
function setMode(mode) {
  $("view-main").hidden = mode === "settings";
  $("view-settings").hidden = mode === "main";
  if (mode === "main") {
    closeConfirm(); // диалог живёт только внутри настроек; правки остаются ждать
    recalc();
  }
}

/* Списки валют в ОТКРЫТЫХ настройках подтягиваются под нового провайдера. */
function syncSettingsCurrencies() {
  if ($("view-settings").hidden) return;
  if (!$("confirm-overlay").hidden) return; // открыт диалог — контролы не трогаем
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
  };
}

/* Есть ли неподтверждённые правки. Тумблер автозапуска сюда не входит:
 * он применяется мгновенно и на «грязность» не влияет. */
function computeDirty() {
  if (!saved) return false;
  return (
    $("set-left").value !== saved.base ||
    $("set-right").value !== saved.target ||
    $("set-interval").value !== saved.interval ||
    $("set-provider").value !== saved.provider ||
    $("set-theme").value !== saved.theme
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

/* Применить всё выбранное в настройках (кроме автозапуска — он мгновенный). */
async function applyPending() {
  const base = $("set-left").value;
  const target = $("set-right").value;
  const minutes = parseInt($("set-interval").value, 10);
  await window.go.app.App.SetDefaults(base, target, minutes); // валидация внутри

  const provider = $("set-provider").value;
  if (provider !== saved.provider) {
    await window.go.app.App.SetProvider(provider);
  }
  const theme = $("set-theme").value;
  if (theme !== saved.theme) {
    await window.go.app.App.SetTheme(theme);
  }
  saved = { base, target, interval: String(minutes), provider, theme };
  updateDirtyUI();
}

/* Откатить контролы к сохранённым значениям (кнопка «Нет»). */
function revertPending() {
  fillSelect($("set-left"), saved.base);
  fillSelect($("set-right"), saved.target);
  $("set-interval").value = saved.interval;
  $("set-provider").value = saved.provider;
  $("set-theme").value = saved.theme;
  updateDirtyUI();
}

/* Заполнение настроек — при открытии «с нуля» (без несохранённых правок). */
async function populateSettings() {
  const st = await window.go.app.App.GetInitState();
  saved = snapshotFromState(st);
  // СТРОГО словарь текущего провайдера — без подмеси от предыдущих.
  S.names = st.update.names ?? {};
  fillSelect($("set-left"), st.config.base_currency);
  fillSelect($("set-right"), st.config.target_currency);
  // Если дефолтная пара отсутствует у текущего источника — показываем доступную.
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
    o.textContent = p.name;
    provSel.appendChild(o);
  }
  provSel.value = saved.provider;

  $("set-theme").value = saved.theme;
  $("ver").textContent = "v0.1.0";
  updateDirtyUI();
}

async function openSettings() {
  await populateSettings();
  showMode("settings");
}

/* ------------------------------ события UI ------------------------------- */
 $("in-amount").addEventListener("input", () => { S.amount = $("in-amount").value; recalc(); });
 $("sel-left").addEventListener("change", e => { S.left = e.target.value; recalc(); });
 $("sel-right").addEventListener("change", e => { S.right = e.target.value; recalc(); });
 $("swap-amt").addEventListener("click", doSwap);
 $("swap-cur").addEventListener("click", doSwap);
 $("btn-refresh").addEventListener("click", () => window.go.app.App.ManualRefresh());
 $("btn-settings").addEventListener("click", () => openSettings());

/* Выход из настроек: с правками — через подтверждение, без — сразу. */
 $("btn-back").addEventListener("click", () => {
  if (computeDirty()) {
    openConfirm("back");
    return;
  }
  showMode("main");
});

/* Любое изменение подтверждаемых настроек — только помечаем «грязность».
 * (Автозапуск — исключение, обрабатывается отдельно и мгновенно.) */
for (const id of ["set-left", "set-right", "set-interval", "set-provider", "set-theme"]) {
  $(id).addEventListener("change", updateDirtyUI);
}

/* Кнопка «Сохранить» = применить всё выбранное (остаёмся в настройках). */
 $("btn-save").addEventListener("click", async () => {
  try {
    await applyPending();
    showToast("Сохранено");
  } catch (e) { alert(e); }
});

/* Диалог подтверждения */
 $("confirm-yes").addEventListener("click", async () => {
  const ctx = confirmCtx;
  try {
    await applyPending();
    closeConfirm();
    showToast("Настройки применены");
    if (ctx === "back") showMode("main"); // «reopen» — остаёмся в настройках
  } catch (e) { alert(e); } // ошибка валидации — диалог остаётся открытым
});
 $("confirm-no").addEventListener("click", () => {
  const ctx = confirmCtx;
  revertPending(); // «нет» — ничего не изменяется
  closeConfirm();
  if (ctx === "back") showMode("main");
});

/* команды режима приходят с бэкенда (трей → ShowMode → событие) */
window.runtime?.EventsOn?.("ui:mode", m => {
  setMode(m);
  if (m === "settings") {
    if (computeDirty()) {
      openConfirm("reopen"); // вернулись к неотправленным правкам — спросим
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
  const b = $("upd-badge");
  b.textContent = "нет связи";
  b.classList.add("err");
});
window.runtime?.EventsOn?.("refresh:busy", b => setRefreshBusy(!!b));
window.runtime?.EventsOn?.("refresh:cooldown", s => startCooldownUI(s | 0));
window.runtime?.EventsOn?.("theme:changed", t => applyTheme(t));
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
    applyTheme(st.config.theme);
    S.names = st.update.names ?? {};
    S.left = st.config.base_currency; S.right = st.config.target_currency;
    applyUpdate(st.update);
    setRefreshBusy(false);
    startCooldownUI(st.cooldown_sec | 0);
    setMode("main");
  } catch (e) { console.error("init:", e); }
})();