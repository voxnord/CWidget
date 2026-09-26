import { S, getSaved, setSaved, getConfirmCtx, setConfirmCtx } from "./state.js";
import { t, tErr, providerLabel } from "./i18n.js";
import { fmtTime } from "./format.js";
import { $, showToast, showError, showMode } from "./ui.js";
import { fillSelect } from "./dropdown.js";

/* Settings: dirty tracking, the confirmation dialog (settings-leaving AND
 * uninstall scenarios), population, provider statuses, tabs, instant
 * toggles. Every confirmable field appears in exactly four places:
 * snapshotFromState, computeDirty, applyPending, revertPending (plus its
 * control in populateSettings). Instant toggles live outside that flow. */

export function snapshotFromState(st) {
  return {
    base: st.config.base_currency,
    target: st.config.target_currency,
    interval: String(st.config.refresh_interval_minutes),
    provider: st.provider ?? "",
    theme: st.config.theme === "light" ? "light" : "dark",
    lang: st.lang ?? "",
  };
}

function computeDirty() {
  const saved = getSaved();
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

export function updateDirtyUI() {
  $("btn-save").classList.toggle("dirty", computeDirty());
}

// The dialog serves two scenarios: leaving settings with unsaved edits
// ("back"/"reopen") and the irreversible uninstall. Texts are set
// dynamically (which also lets applyDict re-translate an open dialog).
export function openConfirm(ctx) {
  setConfirmCtx(ctx);
  const m = ctx === "uninstall"
    ? { t: "uninstall.title", x: "uninstall.text", y: "uninstall.yes", n: "uninstall.no" }
    : { t: "confirm.title", x: "confirm.text", y: "confirm.yes", n: "confirm.no" };
  $("confirm-title").textContent = t(m.t);
  $("confirm-text").textContent = t(m.x);
  $("confirm-yes").textContent = t(m.y);
  $("confirm-no").textContent = t(m.n);
  $("confirm-overlay").hidden = false;
}

export function closeConfirm() {
  $("confirm-overlay").hidden = true;
  setConfirmCtx(null);
}

// Apply everything selected (except instant toggles). Exactly what the
// controls show is saved — nothing is silently rewritten on the way to the
// backend.
async function applyPending() {
  const base = $("set-left").value;
  const target = $("set-right").value;
  const minutes = parseInt($("set-interval").value, 10);
  await window.go.app.App.SetDefaults(base, target, minutes);

  const provider = $("set-provider").value;
  if (provider !== getSaved().provider) await window.go.app.App.SetProvider(provider);

  const theme = $("set-theme").value;
  if (theme !== getSaved().theme) await window.go.app.App.SetTheme(theme);

  const lang = $("set-lang").value;
  if (lang !== getSaved().lang) await window.go.app.App.SetLanguage(lang);

  setSaved({ base, target, interval: String(minutes), provider, theme, lang });
  updateDirtyUI();
}

// Roll the controls back to the saved values (the "No" button).
function revertPending() {
  const saved = getSaved();
  fillSelect($("set-left"), saved.base);
  fillSelect($("set-right"), saved.target);
  $("set-interval").value = saved.interval;
  $("set-provider").value = saved.provider;
  $("set-theme").value = saved.theme;
  $("set-lang").value = saved.lang;
  updateDirtyUI();
}

// Fill the settings "from scratch" (no unsaved edits). The saved pair is
// shown AS IS — the settings reflect the config, not momentary network
// availability.
export async function populateSettings() {
  const st = await window.go.app.App.GetInitState();
  setSaved(snapshotFromState(st));
  S.names = st.update.names ?? {};
  S.favorites = st.config.favorites ?? [];
  fillSelect($("set-left"), getSaved().base);
  fillSelect($("set-right"), getSaved().target);

  $("set-interval").value = getSaved().interval;
  $("set-autostart").setAttribute("aria-checked", String(st.autostart_enabled));
  $("set-stayopen").setAttribute("aria-checked", String(!!st.config.stay_open));
  $("set-rememberpos").setAttribute("aria-checked", String(!!st.config.remember_position));

  // The provider list is built from the backend registry — a newly
  // registered provider appears here automatically.
  const provSel = $("set-provider");
  provSel.innerHTML = "";
  for (const p of st.providers ?? []) {
    const o = document.createElement("option");
    o.value = p.key;
    o.textContent = providerLabel(p);
    o.dataset.name = p.name; // canonical name — for renaming on a language switch
    provSel.appendChild(o);
  }
  provSel.value = getSaved().provider;

  $("set-theme").value = getSaved().theme;

  const langSel = $("set-lang");
  langSel.innerHTML = "";
  for (const l of st.languages ?? []) {
    const o = document.createElement("option");
    o.value = l.code;
    o.textContent = l.name; // endonym — never translated
    langSel.appendChild(o);
  }
  langSel.value = getSaved().lang;

  renderProviderStatuses(st.provider_statuses);
  $("ver").textContent = "v" + (st.version || "dev");
  updateDirtyUI();
}

export async function openSettings() {
  await populateSettings();
  showMode("settings");
}

// Refresh ONLY the currency labels in the open settings (fresh data
// arrived). Selected values are never touched.
export function refreshSettingsCurrencyLabels() {
  if ($("view-settings").hidden) return;
  if (!$("confirm-overlay").hidden) return;
  for (const id of ["set-left", "set-right"]) {
    const sel = $(id);
    if (sel.value) fillSelect(sel, sel.value);
  }
  updateDirtyUI();
}

// Provider status line: available / unavailable (time) / not checked yet.
// The full text of the last error is in the tooltip.
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

export async function refreshProviderStatuses() {
  if ($("view-settings").hidden) return;
  try {
    const st = await window.go.app.App.GetInitState();
    renderProviderStatuses(st.provider_statuses);
  } catch (e) { showError(e); }
}

// Tiny bridges for main.js: dirty-state check and a populate-only reopen
// (used by the ui:mode event, where showMode is driven by the backend).
export function settingsDirty() { return computeDirty(); }
export function openSettingsPopulate() { populateSettings(); }

export function wireSettings() {
  // Leaving the settings: with edits — via confirmation, without — instantly.
  $("btn-back").addEventListener("click", () => {
    if (computeDirty()) openConfirm("back");
    else showMode("main");
  });

  // Any change of a confirmable setting only marks the form dirty.
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
    const ctx = getConfirmCtx();
    if (ctx === "uninstall") {
      closeConfirm();
      try { await window.go.app.App.Uninstall(); } // the backend quits; cleanup takes over
      catch (e) { showError(e); }
      return;
    }
    try {
      await applyPending();
      closeConfirm();
      showToast(t("toast.applied"));
      if (ctx === "back") showMode("main");
    } catch (e) { showError(e); } // a validation error keeps the dialog open
  });
  $("confirm-no").addEventListener("click", () => {
    const ctx = getConfirmCtx();
    if (ctx === "uninstall") { closeConfirm(); return; } // nothing changes
    revertPending();
    closeConfirm();
    if (ctx === "back") showMode("main");
  });

  // Tabs.
  document.querySelectorAll(".tab").forEach(btn => btn.addEventListener("click", () => {
    document.querySelectorAll(".tab").forEach(b => b.classList.remove("active"));
    btn.classList.add("active");
    $("tab-basic").hidden = btn.dataset.tab !== "basic";
    $("tab-about").hidden = btn.dataset.tab !== "about";
  }));

  // Instant toggles flip their VISIBLE state optimistically; the backend is
  // the source of truth and confirms via events (the catch reverts). No
  // GetInitState round-trip — it was both needless IPC and a stale-read
  // race on double clicks.
  const wireToggle = (id, apply) => {
    $(id).addEventListener("click", async () => {
      const el = $(id);
      const nxt = el.getAttribute("aria-checked") !== "true";
      el.setAttribute("aria-checked", String(nxt));
      try { await apply(nxt); }
      catch (e) { el.setAttribute("aria-checked", String(!nxt)); showError(e); }
    });
  };
  wireToggle("set-autostart", v => window.go.app.App.SetAutoStart(v));
  wireToggle("set-stayopen", v => window.go.app.App.SetStayOpen(v));
  wireToggle("set-rememberpos", v => window.go.app.App.SetRememberPos(v));

  // Irreversible uninstall — through the shared confirmation dialog.
  $("btn-uninstall").addEventListener("click", () => openConfirm("uninstall"));
}