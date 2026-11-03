// Which dollars are maths. Pure text, no DOM, so test/test_mathscan.mjs runs it in node.
// A post about money or shell variables must stay as written, so `$...$` has to earn it (rules in design_pack/chat/SPEC.md).
// `\(..\)`, `\[..\]`, a `$$` block and a math fence are always maths. Inline code and bare links are never scanned.
const MAXTEX = 4000, MAXSCAN = 20000;

function closes(s, a, b) {
  const body = s.slice(a + 1, b);
  if (!body || b - a > 300 || /^\s/.test(body) || /\s$/.test(body) || body.includes("\n")) return false;
  const nx = s.slice(b + 1, b + 5);
  if (/^[0-9({]/.test(nx)) return false;
  if (/^[A-Za-z]/.test(nx) && !/^(th|st|nd|rd|s)(?![A-Za-z])/.test(nx)) return false;
  if (/^[A-Z]{3,}$/.test(body) || /^[\d.,]+[kKmMbB%]?$/.test(body)) return false;
  if (/(?:^|[\s,])[a-z]{3,}[\s,]+[a-z]{3,}(?:[\s,]|$)/.test(body.replace(/\\[A-Za-z]+(\{[^}]*\})?/g, " "))) return false;
  return true;
}

/** The maths in one run of inline text: [{from, to, tex}], in order. */
export function mathScan(s) {
  const out = [];
  if (s.length > MAXSCAN || s.includes("")) return out;
  for (let i = 0; i < s.length;) {
    const c = s[i];
    if (c === "\\") {
      if (s[i + 1] === "(") {
        const j = s.indexOf("\\)", i + 2);
        if (j > 0 && j - i <= MAXTEX && !s.slice(i, j).includes("`")) { out.push({ from: i, to: j + 2, tex: s.slice(i + 2, j) }); i = j + 2; continue; }
      }
      i += 2; continue;
    }
    if (c === "`") {
      let n = 1; while (s[i + n] === "`") n++;
      const tick = "`".repeat(n); let j = i + n;
      for (;;) { // the closer is a run of exactly n
        j = s.indexOf(tick, j); if (j < 0) break;
        let e = j + n; while (s[e] === "`") e++;
        if (e - j === n) break;
        j = e;
      }
      i = j < 0 ? i + n : j + n; continue;
    }
    if (c === "h" && /^https?:\/\//.test(s.slice(i, i + 8))) { while (i < s.length && !/\s/.test(s[i])) i++; continue; }
    if (c === "$" && s[i + 1] === "$") {  // `$$x$$` in the middle of a sentence: drawn in line, since the sentence goes on around it
      const j = s.indexOf("$$", i + 2), body = j > 0 ? s.slice(i + 2, j) : "";
      if (body && j - i <= MAXTEX && !/^\s|\s$|`/.test(body)) { out.push({ from: i, to: j + 2, tex: body }); i = j + 2; continue; }
      i += 2; continue;
    }
    if (c === "$" && !/[A-Za-z0-9$]/.test(s[i - 1] || "") && s[i + 1] !== "$") {
      let j = i + 1;
      for (; j < s.length && j - i <= 300; j++) { if (s[j] === "\\") { j++; continue; } if (s[j] === "$") break; }
      if (s[j] === "$" && closes(s, i, j)) { out.push({ from: i, to: j + 1, tex: s.slice(i + 1, j) }); i = j + 1; continue; }
    }
    i++;
  }
  return out;
}

/** Quick test: can this line open a display equation at all? */
export const opensDisplay = l => { const t = l.trimStart(); return t.startsWith("$$") || t.startsWith("\\["); };

/** A `$$` or `\[` block starting at line i: {tex, end} with end the line after it, or null. Unclosed stays text. */
export function displayMath(ls, i) {
  const t = ls[i].trim(), open = t.startsWith("$$") ? "$$" : t.startsWith("\\[") ? "\\[" : null;
  if (!open) return null;
  const close = open === "$$" ? "$$" : "\\]", parts = []; let size = 0;
  for (let j = i; j < ls.length && j < i + 60; j++) {
    let x = j === i ? t.slice(2) : ls[j].trim();
    if (j > i && !x) return null;
    const done = x.endsWith(close);
    if (done) x = x.slice(0, -2);
    if (x.includes(close)) return null;  // `$$a$$ and $$b$$` is not one block
    parts.push(x); size += x.length;
    if (size > MAXTEX) return null;
    if (done) { const tex = parts.join("\n").trim(); return tex ? { tex, end: j + 1 } : null; }
  }
  return null;
}
