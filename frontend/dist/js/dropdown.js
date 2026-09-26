import { S } from "./state.js";
import { t } from "./i18n.js";
import { showError } from "./ui.js";

/* Favorites & custom currency dropdowns.
 * A native <select> cannot host buttons inside its options, so each
 * currency select is enhanced: the select itself is hidden (it remains the
 * state holder and keeps firing change events for all existing handlers),
 * and a custom list is drawn on top — favorites duplicated at the top (the
 * provider's own order below is untouched), a star per row, a search
 * filter. The list lives in <body> with position:fixed: never clipped by
 * the window, may extend past its bottom edge (like native comboboxes).
 * Favorites are a GLOBAL list of ISO codes: provider-agnostic by design,
 * unavailable codes are just not rendered for the current source. */

export const currencySelects = [];

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

// Rebuild the native options (the state holder) from S.names and refresh
// the custom button label.
export function fillSelect(sel, value) {
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

export function enhanceCurrencySelect(sel) {
  const wrap = document.createElement("div");
  wrap.className = "cwrap no-drag";
  sel.parentNode.insertBefore(wrap, sel);
  wrap.appendChild(sel);

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
  document.body.appendChild(list); // fixed positioning — above everything

  sel.classList.add("chidden");
  sel._cwidget = { wrap, btn, list, rows, search };
  currencySelects.push(sel);
  syncDropdown(sel);
}

export function syncDropdown(sel) {
  if (!sel._cwidget) return;
  const o = sel.selectedOptions[0];
  sel._cwidget.btn.textContent = o ? o.textContent : "—";
}

// Sync every enhanced dropdown's visible button with its hidden select.
// Call after programmatic select.value changes that bypass fillSelect
// (the swap buttons are the current example).
export function syncAllDropdowns() {
  for (const sel of currencySelects) syncDropdown(sel);
}

// The list height is fitted to the actual free space (to the screen edge /
// taskbar), so at least 4-5 rows are always visible. If there is no room
// below, the list opens upward with its bottom aligned to the button.
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

function positionDropdown(sel) {
  const w = sel._cwidget;
  const r = w.wrap.getBoundingClientRect();
  const vh = window.innerHeight;
  const vw = window.innerWidth;

  const chrome = 56; // search input + paddings + borders
  const minRows = 4, rowH = 27, maxH = 240;
  const spaceBelow = vh - r.bottom - 6;
  const spaceAbove = r.top - 6;

  let h, below;
  if (spaceBelow >= chrome + minRows * rowH || spaceBelow >= spaceAbove) {
    below = true;
    h = Math.min(maxH, Math.max(minRows * rowH + chrome, spaceBelow));
  } else {
    below = false;
    h = Math.min(maxH, Math.max(minRows * rowH + chrome, spaceAbove));
  }
  w.rows.style.maxHeight = Math.max(80, h - chrome) + "px";

  const left = Math.max(4, Math.min(r.left, vw - r.width - 4));
  w.list.style.left = Math.round(left) + "px";
  w.list.style.width = Math.round(r.width) + "px";
  w.list.classList.toggle("up", !below);
  if (below) {
    w.list.style.top = Math.round(r.bottom + 4) + "px";
    w.list.style.bottom = "";
  } else {
    w.list.style.top = "";
    w.list.style.bottom = Math.round(vh - r.top + 4) + "px";
  }
}

export function closeDropdowns() {
  for (const sel of currencySelects) sel._cwidget.list.classList.remove("open");
}

export function renderDropdownList(sel) {
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

// Global dismiss handlers (outside click, Escape).
export function wireDismiss() {
  document.addEventListener("click", e => {
    if (!e.target.closest(".cwrap") && !e.target.closest(".clist")) closeDropdowns();
  });
  document.addEventListener("keydown", e => { if (e.key === "Escape") closeDropdowns(); });
}