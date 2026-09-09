"use strict";

const $ = id => document.getElementById(id);

const S = {
  left: "RUB", right: "KZT", amount: "100",
  table: null, names: {},
};

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
  if (p?.names) S.names = p.names;
  const badge = $("upd-badge");
  if (S.table?.fetched_at) {
    let txt = `обн. ${fmtTime(new Date(S.table.fetched_at))}`;
    const qd = fmtQuoteDate(S.table.quote_date);
    if (qd) txt += ` · кот. ${qd}`;
    badge.textContent = txt;
    badge.classList.remove("err");
  }
  $("src-badge").textContent = S.table?.source ?? "—";
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
  if (mode === "main") recalc();
}

async function populateSettings() {
  const st = await window.go.app.App.GetInitState();
  S.names = Object.assign({}, st.update.names ?? {}, S.names);
  fillSelect($("set-left"), st.config.base_currency);
  fillSelect($("set-right"), st.config.target_currency);
  $("set-interval").value = String(st.config.refresh_interval_minutes);
  $("set-autostart").setAttribute("aria-checked", String(st.autostart_enabled));

  const provSel = $("set-provider");
  provSel.innerHTML = "";
  for (const p of st.providers ?? []) {
    const o = document.createElement("option");
    o.value = p.key;
    o.textContent = p.name;
    provSel.appendChild(o);
  }
  provSel.value = st.provider ?? "";

  $("set-theme").value = st.config.theme === "light" ? "light" : "dark";

  $("ver").textContent = "v0.1.0";
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
 $("btn-back").addEventListener("click", () => showMode("main"));

/* Смена источника применяется сразу; данные нового провайдера подтянутся сами. */
 $("set-provider").addEventListener("change", async e => {
  const key = e.target.value;
  const name = e.target.selectedOptions[0]?.textContent ?? key;
  try {
    await window.go.app.App.SetProvider(key);
    showToast(`Источник: ${name}`);
  } catch (err) {
    alert(err);
    const st = await window.go.app.App.GetInitState();
    e.target.value = st.provider ?? "";
  }
});

/* Смена темы применяется сразу. */
 $("set-theme").addEventListener("change", async e => {
  const t = e.target.value;
  try {
    await window.go.app.App.SetTheme(t);
    applyTheme(t); // событие theme:changed придёт и так — это мгновенный отклик
    showToast(t === "light" ? "Тема: светлая" : "Тема: тёмная");
  } catch (err) { alert(err); }
});

/* команды режима приходят с бэкенда (трей → ShowMode → событие) */
window.runtime?.EventsOn?.("ui:mode", m => {
  setMode(m);
  if (m === "settings") populateSettings();
});

/* события от бэкенда */
window.runtime?.EventsOn?.("rates:update", p => applyUpdate(p));
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

 $("btn-save").addEventListener("click", async () => {
  try {
    await window.go.app.App.SetDefaults(
      $("set-left").value, $("set-right").value, parseInt($("set-interval").value, 10));
    showToast("Сохранено");
  } catch (e) { alert(e); }
});
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
    applyTheme(st.config.theme); // тема до первого показа окна
    S.names = st.update.names ?? {};
    S.left = st.config.base_currency; S.right = st.config.target_currency;
    applyUpdate(st.update);
    setRefreshBusy(false);
    startCooldownUI(st.cooldown_sec | 0);
    setMode("main");
  } catch (e) { console.error("init:", e); }
})();