"use strict";

import { S, getConfirmCtx } from "./js/state.js";
import { t, providerLabel } from "./js/i18n.js";
import { $, reportToLog, applyTheme, showMode, setRefreshBusy, startCooldownUI, updateFallbackUI } from "./js/ui.js";
import { currencySelects, enhanceCurrencySelect, closeDropdowns, renderDropdownList, fillSelect, wireDismiss } from "./js/dropdown.js";
import { applyUpdate, recalc, wireConverter } from "./js/converter.js";
import { openConfirm, closeConfirm, openSettings, updateDirtyUI, refreshProviderStatuses, refreshSettingsCurrencyLabels, wireSettings, settingsDirty, openSettingsPopulate } from "./js/settings.js";

/* Entry point: orchestration only. Domain logic lives in the js/* modules;
 * this file wires the DOM events that cross modules, subscribes to backend
 * events, and boots the app. */

/* Popup behavior: hide on focus loss (two anti-race guards).
 * 1) Right after clicking a tray menu item Windows briefly returns focus
 *    to the taskbar — the grace period absorbs it.
 * 2) The 120ms deferred hide must not fire after a NEWER focus (the user
 *    re-opened the widget within the window): every focus bumps focusSeq,
 *    the blur captures it, and a mismatch cancels the hide. Without this a
 *    stale timer could hide a freshly re-opened window — the classic
 *    "click, flicker, click again" symptom. */
let hideGuardUntil = 0;
let focusSeq = 0;
const armHideGuard = (ms = 300) => { hideGuardUntil = Date.now() + ms; };

window.addEventListener("blur", () => {
  const seq = focusSeq; // captured: a newer focus cancels this hide
  setTimeout(() => {
    if (S.stayOpen) return;
    if (focusSeq !== seq) return;
    if (Date.now() < hideGuardUntil) return;
    if (document.hasFocus()) return;
    try { window.runtime.WindowHide(); } catch {}
  }, 120);
});
window.addEventListener("focus", () => { focusSeq++; armHideGuard(300); });

// Apply a dictionary to all marked elements, then redraw dynamic texts.
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
  // Dropdown chrome is rendered by us: search placeholders and star
  // tooltips must follow the language immediately.
  for (const sel of currencySelects) {
    sel._cwidget.search.placeholder = t("ui.search");
    renderDropdownList(sel);
  }

  if (S.err) $("upd-badge").textContent = t("rates.error");
  else applyUpdate({});
  updateFallbackUI();
  refreshProviderStatuses();
  // An open confirmation dialog keeps its dynamic texts in sync too.
  if (!$("confirm-overlay").hidden && getConfirmCtx()) openConfirm(getConfirmCtx());
}

// Mode switching: DOM visibility here; size/position/show — on the backend.
function setMode(mode) {
  $("view-main").hidden = mode === "settings";
  $("view-settings").hidden = mode === "main";
  if (mode === "main") {
    closeDropdowns();
    closeConfirm();
    recalc();
  }
}

function wireMainButtons() {
  $("btn-refresh").addEventListener("click", () => window.go.app.App.ManualRefresh());
  $("btn-settings").addEventListener("click", () => openSettings());
  $("btn-close").addEventListener("click", () => {
    try { window.runtime.WindowHide(); } catch {}
  });
}

function subscribeBackendEvents() {
  // Mode commands come from the backend (tray -> ShowMode -> event).
  window.runtime?.EventsOn?.("ui:mode", m => {
    setMode(m);
    if (m === "settings") {
      if (settingsDirty()) openConfirm("reopen"); // returned to unsent edits — ask
      else openSettingsPopulate();
    }
  });

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
  window.runtime?.EventsOn?.("i18n:changed", p => {
    S.lang = p?.lang ?? S.lang;
    applyDict(p?.dict);
  });
  window.runtime?.EventsOn?.("stayopen:changed", on => { S.stayOpen = !!on; });
  window.runtime?.EventsOn?.("rememberpos:changed", on => {
    $("set-rememberpos").setAttribute("aria-checked", String(!!on));
  });
  window.runtime?.EventsOn?.("favorites:changed", favs => {
    S.favorites = favs ?? [];
    for (const sel of currencySelects) renderDropdownList(sel);
  });
  window.runtime?.EventsOn?.("autostart:changed", on => {
    $("set-autostart").setAttribute("aria-checked", String(!!on));
  });
  window.runtime?.EventsOn?.("defaults:changed", cfg => {
    S.left = cfg.base_currency; S.right = cfg.target_currency;
    S.pairOverride = null; // the user pinned the pair deliberately
    fillSelect($("sel-left"), S.left); fillSelect($("sel-right"), S.right);
    updateFallbackUI();
  });
}

/* --------------------------------- boot ----------------------------------- */
(async function init() {
  try {
    document.querySelectorAll("select.ccurrency").forEach(enhanceCurrencySelect);
    wireConverter();
    wireDismiss();
    wireSettings();
    wireMainButtons();
    subscribeBackendEvents();

    const st = await window.go.app.App.GetInitState();
    S.lang = st.lang ?? "ru";
    applyDict(st.i18n);        // the dictionary BEFORE the first render
    applyTheme(st.config.theme);
    S.names = st.update.names ?? {};
    S.favorites = st.config.favorites ?? [];
    S.stayOpen = !!st.config.stay_open;
    S.provider = st.provider ?? "";
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