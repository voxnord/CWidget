import { S } from "./state.js";
import { t, tErr, providerName, preferredName } from "./i18n.js";

export const $ = id => document.getElementById(id);
export const BLANK = "\u2800"; // braille blank: keeps layout width, draws nothing

/* ------------------- frontend error bridge -------------------------------
 * A production build has no DevTools console: without this bridge a JS
 * failure leaves no trace. Forwarded to the backend log, capped per
 * session to survive a pathological error loop. */
let feLogCount = 0;
export function reportToLog(level, msg) {
  if (feLogCount++ > 60) return;
  try { window.go.app.App.LogFrontend(level, String(msg).slice(0, 1000)); } catch {}
}

export function showError(e) {
  reportToLog("error", "action failed: " + (e?.stack ?? e));
  alert(tErr(e));
}

let toastTimer = null;
export function showToast(text) {
  const tt = $("toast");
  tt.textContent = text;
  tt.classList.add("show");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => tt.classList.remove("show"), 1600);
}

export function applyTheme(th) {
  document.body.classList.toggle("light", th === "light");
}

export function setArr(el, dir) {
  el.textContent = dir === "up" ? "\u2191" : dir === "down" ? "\u2193" : BLANK;
  el.className = "arr " + (dir ?? "flat");
}

// Window presentation goes through the backend: size per mode, position
// near the tray / remembered spot, focus.
export function showMode(mode) {
  try { window.go.app.App.ShowMode(mode); }
  catch (e) { reportToLog("error", "ShowMode failed: " + (e?.stack ?? e)); }
}

// Degradation indicator: a warning mark near the source badge plus a
// dynamic tooltip explaining where the data actually comes from.
export function updateFallbackUI() {
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

/* --------------------------- refresh button (window) ---------------------- */
let coolTimer = null;
let cooling = false;

export function setRefreshBusy(busy) {
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

export function startCooldownUI(sec) {
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

export function endCooldownUI() {
  clearInterval(coolTimer); cooling = false;
  const b = $("btn-refresh");
  b.classList.remove("cool", "spin");
  b.disabled = false;
  b.textContent = "⟳";
  b.title = t("tray.refresh");
}