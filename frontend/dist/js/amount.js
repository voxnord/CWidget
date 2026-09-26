// Pure amount-input formatting utilities (no side effects, no recalc —
// the caller triggers it).

export function cleanAmountString(s) {
  s = s.replace(/[^\d.,]/g, "").replace(/\./g, ","); // digits and separators only
  const c = s.indexOf(",");                          // at most one comma
  if (c !== -1) s = s.slice(0, c + 1) + s.slice(c + 1).replace(/,/g, "");
  return s;
}

export function groupThousands(s) {
  const c = s.indexOf(",");
  const intPart = c === -1 ? s : s.slice(0, c);
  const frac = c === -1 ? "" : s.slice(c); // fraction as typed
  return intPart.replace(/\B(?=(\d{3})+(?!\d))/g, " ") + frac;
}

// formatAmountInput(el, caretPos): caretPos is the caret offset in el.value
// BEFORE the rewrite (on an input event — the current selectionStart). The
// caret is re-anchored to the same digit count, so typing in the middle of
// a number never jumps to the end.
export function formatAmountInput(el, caretPos) {
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