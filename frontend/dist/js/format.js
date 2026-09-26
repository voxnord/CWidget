import { S } from "./state.js";

// money: locale-aware output. Thousands separator is a SPACE in both
// languages (matches the input pipeline), the decimal separator follows
// the UI language (ru: ",", en: "."). Trailing zeros are trimmed.
export function money(v) {
  if (!isFinite(v)) return "—";
  const a = Math.abs(v);
  const maxD = a < 1 ? 6 : a < 1000 ? 4 : 2;
  let s = v.toFixed(maxD);
  if (s.includes(".")) s = s.replace(/0+$/, "").replace(/\.$/, "");
  let [int, frac] = s.split(".");
  const neg = int.startsWith("-");
  if (neg) int = int.slice(1);
  int = int.replace(/\B(?=(\d{3})+(?!\d))/g, " ");
  const dec = S.lang === "en" ? "." : ",";
  return (neg ? "-" : "") + int + (frac ? dec + frac : "");
}

// parseAmt: accepts both separator conventions. When both "." and "," are
// present the LAST one is decimal and the other is grouping; a single
// separator is decimal — unless it matches the exact grouping pattern
// (1,234 / 1.234). Unambiguous and locale-independent.
export function parseAmt(s) {
  s = String(s).replace(/\s|\u00A0/g, "");
  if (!s) return NaN;
  const lc = s.lastIndexOf(","), ld = s.lastIndexOf(".");
  if (lc !== -1 && ld !== -1) {
    if (lc > ld) s = s.replace(/\./g, "").replace(",", ".");
    else s = s.replace(/,/g, "");
  } else if (lc !== -1) {
    if (/^\d{1,3}(,\d{3})+$/.test(s)) s = s.replace(/,/g, "");
    else s = s.replace(",", ".");
  } else if (ld !== -1) {
    if (/^\d{1,3}(\.\d{3})+$/.test(s)) s = s.replace(/\./g, "");
  }
  const v = parseFloat(s);
  return isFinite(v) ? v : NaN;
}

export function fmtTime(d) { // 24h in both languages — matches the tray label
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

export function fmtQuoteDate(iso) { // dd.mm (ru) / mm.dd (en) — native conventions
  if (!iso || iso.length < 10) return null;
  const d = new Date(iso + "T00:00:00");
  return d.toLocaleDateString(S.lang === "en" ? "en-US" : "ru-RU", { day: "2-digit", month: "2-digit" });
}