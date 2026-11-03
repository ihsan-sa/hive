// A code block: the frame (language, title, Wrap, Copy), then its lines. The tree a poll builds holds only plain text; lines,
// numbers and colours are added once the block nears the screen, and Prism (local, by language) only when one needs it.
// Prism's tokenize() is walked into nodes here: its HTML output is never used. Copy always gives the exact source.
import { h, copyText } from "../core/dom.js";
import { lazyKind } from "./lazy.js";
import { sigOf } from "./math.js";
import { askQuote } from "./ask.js";

const FOLD_OVER = 24, FOLD_TO = 18, NUMS_OVER = 8, HUGE = 200000, HUGE_LINES = 200, LONGLINE = 20000;
const WRAP = new Set(), SHOWN = new Set();

// ---- Prism: what each language needs loaded first ----
const ALIAS = { py: "python", sh: "bash", shell: "bash", zsh: "bash", js: "javascript", mjs: "javascript", ts: "typescript", "c++": "cpp", cc: "cpp", yml: "yaml", make: "makefile", rs: "rust",
  v: "verilog", sv: "verilog", systemverilog: "verilog", cir: "spice", sp: "spice", ngspice: "spice" };
const NEEDS = { python: [], bash: [], c: [], cpp: ["c"], rust: [], javascript: [], typescript: ["javascript"], json: [], yaml: [], toml: [], makefile: [], tcl: [], verilog: [] };
const vendor = n => new URL("../../vendor/prism/prism-" + n + ".min.js", import.meta.url).href;
let core = null; const langs = {};
function prismCore() {
  return core ||= (async () => {
    self.Prism = { manual: true, disableWorkerMessageHandler: true };
    await import(vendor("core")); await import(vendor("clike"));
    self.Prism.languages.spice = {  // ours: Prism has none. Only line-start cards and comments, plus engineering numbers.
      comment: [/^[ \t]*\*.*$/m, /[;$].*$/m], keyword: /^[ \t]*\.[a-z]+/im, string: /"[^"\n]*"/,
      number: /(?<![\w.])[-+]?\d+(?:\.\d+)?(?:e[-+]?\d+|meg|[fpnumkgt])?(?![\w.])/i, function: /^[ \t]*[a-z][\w.]*/im,
    };
    return self.Prism;
  })();
}
async function grammar(lang) {
  const name = ALIAS[lang] || lang;
  if (name !== "spice" && !NEEDS[name]) return null;
  const P = await prismCore();
  if (name !== "spice") await (langs[name] ||= (async () => { for (const d of NEEDS[name]) await grammar(d); await import(vendor(name)); })());
  return P.languages[name] || null;
}

const CLS = { keyword: "k", string: "s", char: "s", "attr-value": "s", regex: "s", comment: "c", prolog: "c", doctype: "c", number: "n", boolean: "n", constant: "n", function: "f", "class-name": "f", builtin: "f" };
/** Prism tokens to one array of nodes per line. */
function tokenLines(tokens, n) {
  const lines = [[]];
  const put = (text, cls) => text.split("\n").forEach((t, i) => {
    if (i) lines.push([]);
    if (t) lines[lines.length - 1].push(cls ? h("span", { class: "hl-" + cls }, t) : t);
  });
  const walk = (t, cls) => {
    if (typeof t === "string") return put(t, cls);
    if (Array.isArray(t)) return t.forEach(x => walk(x, cls));
    const a = [t.type].concat(t.alias || []).map(k => CLS[k]).find(Boolean);
    walk(t.content, a || cls);
  };
  walk(tokens);
  while (lines.length < n) lines.push([]);
  return lines;
}

// ---- terminal colours ----
const ESC = /\x1b\[[0-9;]*[A-Za-z]/g, SGR = /\x1b\[([0-9;]*)m/g;
const ANSI = { 31: "r", 91: "r", 32: "g", 92: "g", 33: "y", 93: "y", 34: "b", 94: "b", 36: "b", 96: "b", 2: "d", 90: "d" };
function ansiLine(l, st) {
  const out = []; let at = 0;
  const put = t => { if (t) out.push(st.c || st.bold ? h("span", { class: [st.c && "an-" + st.c, st.bold && "an-B"].filter(Boolean).join(" ") }, t) : t); };
  for (const m of l.matchAll(SGR)) {
    put(l.slice(at, m.index).replace(ESC, "")); at = m.index + m[0].length;
    for (const k of (m[1] || "0").split(";").map(Number)) {
      if (k === 0 || k === 39) { st.c = null; if (k === 0) st.bold = false; }
      else if (k === 1) st.bold = true; else if (k === 22) st.bold = false;
      else if (ANSI[k]) st.c = ANSI[k];
    }
  }
  put(l.slice(at).replace(ESC, ""));
  return out;
}
const diffKind = l => (/^(--- |\+\+\+ |diff )/.test(l) ? "file" : l.startsWith("@@") ? "hunk" : l[0] === "+" ? "add" : l[0] === "-" ? "del" : null);

// ---- drawing ----
const isAnsi = lang => lang === "ansi" || lang === "console";
const plainOf = (src, lang) => (isAnsi(lang) ? src.replace(ESC, "") : src);

function drawLines(pre, kids) {
  const { lines, lang, nums } = pre._c, st = {};
  const row = pre.querySelector(":scope>code"), box = h("code", { class: "rc-ls" });
  lines.forEach((l, i) => {
    const content = kids ? kids[i] : isAnsi(lang) ? ansiLine(l, st) : l;
    box.append(h("span", { class: "rc-l", "data-k": lang === "diff" ? diffKind(l) : null },
      nums ? h("button", { type: "button", class: "rc-ln", tabindex: "-1", title: "Ask about this line", onclick: (e, b) => askQuote(b, l, `line ${i + 1}`) }, String(i + 1)) : null,
      h("span", { class: "rc-lc" }, content)));
  });
  row.replaceWith(box);
}

async function paint(pre) {
  const c = pre._c;
  drawLines(pre);
  if (isAnsi(c.lang) || c.lang === "diff" || !c.lang || c.huge || c.lines.some(l => l.length > LONGLINE)) return;
  const g = await grammar(c.lang);
  if (!g || !pre.isConnected) return;
  const text = c.lines.join("\n");
  drawLines(pre, tokenLines(self.Prism.tokenize(text, g), c.lines.length));
}
lazyKind("code", el => paint(el.querySelector("pre")));

/** A fence as a code block. lang is lowercased; title may be null. */
export function codeBlock(lang, title, code) {
  const sig = sigOf(lang + "\0" + code), plain = plainOf(code, lang);
  const all = plain === "" ? [""] : code.split("\n"), huge = code.length > HUGE;
  const opened = SHOWN.has(sig), cut = opened ? null : huge ? Math.min(all.length, HUGE_LINES) : all.length > FOLD_OVER ? FOLD_TO : null;
  const shown = cut && cut < all.length ? all.slice(0, cut) : all;
  const pre = h("pre", { class: "rc-code", tabindex: "0", "data-wrap": WRAP.has(sig) ? "" : null },
    h("code", { class: "rc-pl" }, plainOf(shown.join("\n"), lang)));
  pre._src = plain;
  pre._c = { lines: shown, lang, nums: all.length > NUMS_OVER && !isAnsi(lang), huge };
  const lz = h("rc-lazy", { "data-kind": "code" }, pre);
  const more = shown.length < all.length ? h("button", { type: "button", class: "rc-foldb", onclick: (e, b) => {
    SHOWN.add(sig); pre._c.lines = all; b.remove();
    pre.querySelector(":scope>code").replaceWith(h("code", null));
    paint(pre);
  } }, huge ? `Show all ${all.length} lines` : `Show ${all.length - shown.length} more lines`) : null;
  return h("div", { class: "rc-block", "data-block": "code", "data-own": "", "data-sig": sig + (title || "") },
    h("div", { class: "rc-bar" },
      lang ? h("span", { class: "rc-lang" }, lang) : null,
      title ? h("span", { class: "rc-ttl" }, title) : null,
      h("span", { class: "rc-tools" },
        all.length > NUMS_OVER ? h("span", { class: "rc-byline" }, all.length + " lines") : null,
        h("button", { type: "button", class: "rc-t", "aria-pressed": String(WRAP.has(sig)), onclick: (e, b) => {
          const on = !WRAP.has(sig);
          if (on) WRAP.add(sig); else WRAP.delete(sig);
          pre.toggleAttribute("data-wrap", on); b.setAttribute("aria-pressed", String(on));
        } }, "Wrap"),
        h("button", { type: "button", class: "rc-t", "aria-label": "Copy this code", onclick: (e, b) => {
          copyText(pre._src).then(() => { b.dataset.ok = ""; b.textContent = "Copied"; setTimeout(() => { delete b.dataset.ok; b.textContent = "Copy"; }, 1400); }, () => {});
        } }, "Copy"))),
    lz, more);
}
