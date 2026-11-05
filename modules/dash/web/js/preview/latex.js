// A .tex file as a reading view: title block, sections, lists, tables, verbatim, quotes and figures' captions, with the maths
// drawn by the local KaTeX (rich/math.js). It's a reader for the common article subset, not TeX: a macro it doesn't know shows
// its argument, \input and \includegraphics show as a note, and nothing is fetched. The file's text only ever becomes text nodes.
import { h } from "../core/dom.js";
import { inlineMath, equation } from "../rich/math.js";
import { codeBlock } from "../rich/code.js";

const SECT = { part: 1, chapter: 1, section: 2, subsection: 3, subsubsection: 4, paragraph: 5, subparagraph: 6 };
const MATHENV = /^(equation|align|gather|multline|eqnarray|displaymath|math|flalign|alignat)\*?$/;
const VERB = /^(verbatim|lstlisting|minted|Verbatim|comment)\*?$/;
// Commands that print nothing here, with how many braced arguments they take.
const SKIP = Object.assign(Object.fromEntries(["newpage", "clearpage", "pagebreak", "centering", "noindent", "smallskip", "medskip", "bigskip", "tableofcontents",
  "listoffigures", "listoftables", "hline", "toprule", "midrule", "bottomrule", "small", "footnotesize", "scriptsize", "tiny", "large", "Large", "LARGE", "huge", "Huge",
  "normalsize", "maketitle", "raggedright", "raggedleft", "linewidth", "textwidth", "protect", "phantomsection", "appendix", "frontmatter", "mainmatter", "backmatter",
  "bfseries", "itshape", "ttfamily", "normalfont", "selectfont"].map(k => [k, 0])),
  Object.fromEntries(["label", "vspace", "hspace", "bibliographystyle", "bibliography", "pagestyle", "thispagestyle", "usepackage", "documentclass", "index", "nocite",
    "hypersetup", "graphicspath", "geometry", "cline", "cmidrule", "input", "include", "title", "author", "date", "color", "fontsize"].map(k => [k, 1])),
  { newcommand: 2, renewcommand: 2, providecommand: 2, setlength: 2, addtolength: 2, setcounter: 2, addcontentsline: 3, newenvironment: 3, renewenvironment: 3 });
const SYM = { LaTeX: "LaTeX", TeX: "TeX", ldots: "…", dots: "…", textendash: "–", textemdash: "—", S: "§", P: "¶", copyright: "©", textregistered: "®", texttrademark: "™",
  textbackslash: "\\", textasciitilde: "~", textasciicircum: "^", textdegree: "°", degree: "°", pounds: "£", euro: "€", quad: " ", qquad: "  ", today: "", and: " · ", par: "\n\n" };
const ESC = { "%": "%", "&": "&", "$": "$", "#": "#", "_": "_", "{": "{", "}": "}", " ": " ", ",": " ", ";": " ", "!": "", "-": "", "/": "", "@": "", "\\": "\n" };

/** The argument in braces starting at s[i] (spaces skipped): [text, index after it], or null. */
function arg(s, i, open = "{", close = "}") {
  while (s[i] === " " || s[i] === "\n" || s[i] === "\t") i++;
  if (s[i] !== open) return null;
  let d = 0;
  for (let j = i; j < s.length; j++) {
    if (s[j] === "\\") { j++; continue; }
    if (s[j] === open) d++;
    else if (s[j] === close && --d === 0) return [s.slice(i + 1, j), j + 1];
  }
  return [s.slice(i + 1), s.length];
}
const opt = (s, i) => arg(s, i, "[", "]");
/** Skips a command's optional and braced arguments; returns [last braced argument or "", index after]. */
function args(s, i, n) { let a = "", o; while ((o = opt(s, i))) i = o[1]; for (let k = 0; k < n; k++) { const g = arg(s, i); if (!g) break; [a, i] = g; } return [a, i]; }

/** Index of the \end{env} matching a \begin{env} that ended just before i, or -1. */
function endOf(s, i, env) {
  const re = /\\(begin|end)\{([^}]*)\}/g; re.lastIndex = i; let d = 1, m;
  while ((m = re.exec(s))) if (m[2] === env && (m[1] === "begin" ? ++d : --d) === 0) return m.index;
  return -1;
}
/** Splits s at `sep` (a regex source) where it's at brace depth 0 and outside nested environments. */
function split(s, sep) {
  const out = [], re = new RegExp(`(${sep})|\\\\(?:begin|end)\\{[^}]*\\}|\\\\.|[{}]`, "g"); let d = 0, at = 0, m;
  while ((m = re.exec(s))) {
    const t = m[0];
    if (m[1] !== undefined) { if (d === 0) { out.push(s.slice(at, m.index)); at = m.index + t.length; } }
    else if (t === "{" || t.startsWith("\\begin")) d++;
    else if (t === "}" || t.startsWith("\\end")) d--;
  }
  out.push(s.slice(at));
  return out;
}

/** \includegraphics: the picture when the same message has it attached (served by /files/ with its usual checks), else a note. */
function graphic(name, ctx) {
  const note = () => h("span", { class: "pv-tex-fig meta" }, "[image: " + name + "]");
  const base = name.split("/").pop(), tries = /\.(png|jpe?g|gif|webp)$/i.test(base) ? [base] : /\./.test(base) ? [] : [base + ".png", base + ".jpg", base + ".jpeg"];
  const ok = tries.map(ctx.sibling || (() => null)).filter(Boolean);
  if (!ok.length) return note();
  const img = h("img", { class: "pv-tex-img", alt: name, src: ok[0] });
  let k = 0;
  img.addEventListener("error", () => { if (++k < ok.length) img.src = ok[k]; else img.replaceWith(note()); });
  return img;
}

/** Inline text: maths, the usual font commands, accents of quotes and dashes, escapes. */
function inline(s, ctx) {
  const out = []; let buf = "", i = 0;
  const text = () => { if (buf) out.push(buf.replace(/``|''/g, m => (m === "``" ? "“" : "”")).replace(/---/g, "—").replace(/--/g, "–").replace(/~/g, " ").replace(/[ \t]*\n[ \t]*/g, " ")); buf = ""; };
  while (i < s.length) {
    const c = s[i];
    if (c === "$") {
      const dbl = s[i + 1] === "$", close = s.indexOf(dbl ? "$$" : "$", i + (dbl ? 2 : 1));
      if (close < 0) { buf += c; i++; continue; }
      const tex = s.slice(i + (dbl ? 2 : 1), close); text();
      out.push(dbl ? equation(tex.trim()) : inlineMath(tex, s.slice(i, close + 1))); i = close + (dbl ? 2 : 1); continue;
    }
    if (c === "{") { const g = arg(s, i); text(); out.push(...inline(g[0], ctx)); i = g[1]; continue; }
    if (c === "}") { i++; continue; }
    if (c === "%") { const e = s.indexOf("\n", i); i = e < 0 ? s.length : e + 1; continue; }
    if (c !== "\\") { buf += c; i++; continue; }
    const m = /^\\([a-zA-Z]+)\*?|^\\(.)/.exec(s.slice(i, i + 40));
    if (!m) { buf += c; i++; continue; }
    i += m[0].length;
    if (m[2] !== undefined) {
      if (m[2] === "(") { const e = s.indexOf("\\)", i); if (e >= 0) { text(); out.push(inlineMath(s.slice(i, e), "\\(" + s.slice(i, e) + "\\)")); i = e + 2; } continue; }
      if (m[2] === "\\") { text(); out.push(h("br")); const o = opt(s, i); if (o) i = o[1]; continue; }
      if (m[2] in ESC) buf += ESC[m[2]];
      else if ("'`^\"~=.".includes(m[2])) { const g = arg(s, i); if (g) { buf += g[0]; i = g[1]; } }   // an accent: keep the letter
      continue;
    }
    const cmd = m[1];
    const wrap = tag => { const [a, j] = args(s, i, 1); text(); out.push(h(tag, null, inline(a, ctx))); i = j; };
    if (cmd === "textbf" || cmd === "mathbf") wrap("strong");
    else if (cmd === "emph" || cmd === "textit" || cmd === "textsl") wrap("em");
    else if (cmd === "texttt" || cmd === "path" || cmd === "code") wrap("code");
    else if (cmd === "underline" || cmd === "uline") wrap("u");
    else if (cmd === "textsc") wrap("span");
    else if (cmd === "textsuperscript") wrap("sup");
    else if (cmd === "textsubscript") wrap("sub");
    else if (cmd === "url") { const [a, j] = args(s, i, 1); text(); out.push(h("code", null, a)); i = j; }
    else if (cmd === "href") { const g1 = arg(s, i), g2 = g1 && arg(s, g1[1]); if (g2) { text(); out.push(...inline(g2[0], ctx), " (", h("code", null, g1[0]), ")"); i = g2[1]; } }
    else if (cmd === "verb") { const d = s[i], e = s.indexOf(d, i + 1); if (e > i) { text(); out.push(h("code", null, s.slice(i + 1, e))); i = e + 1; } }
    else if (cmd === "footnote" || cmd === "thanks") { const [a, j] = args(s, i, 1); text(); ctx.notes.push(a); out.push(h("sup", { class: "pv-tex-fn" }, String(ctx.notes.length))); i = j; }
    else if (cmd === "cite" || cmd === "citep" || cmd === "citet" || cmd === "parencite") { const [a, j] = args(s, i, 1); buf += "[" + a + "]"; i = j; }
    else if (cmd === "ref" || cmd === "eqref" || cmd === "autoref" || cmd === "cref" || cmd === "Cref" || cmd === "pageref") {   // filled in once every label is known
      const [a, j] = args(s, i, 1); text(); const r = h("span", { class: "pv-tex-ref" }); r._key = a; r._paren = cmd === "eqref"; ctx.refs.push(r); out.push(r); i = j;
    }
    else if (cmd === "textcolor" || cmd === "colorbox") { const [a, j] = args(s, i, 2); text(); out.push(...inline(a, ctx)); i = j; }
    else if (cmd === "includegraphics") { const [a, j] = args(s, i, 1); text(); out.push(graphic(a.trim(), ctx)); i = j; }
    else if (cmd in SKIP) i = args(s, i, SKIP[cmd])[1];
    else if (cmd in SYM) buf += SYM[cmd];
    else { const [a, j] = args(s, i, 1); text(); out.push(...inline(a, ctx)); i = j; }   // one it doesn't know: show its argument
  }
  text();
  return out;
}

/** Block-level: paragraphs split at blank lines, sections and environments. */
function blocks(s, ctx) {
  const out = []; let para = "";
  const flush = () => {
    for (const chunk of para.split(/\n[ \t]*\n/)) { const t = inline(chunk, ctx); if (t.some(n => typeof n !== "string" || n.trim())) out.push(h("p", null, t)); }
    para = "";
  };
  const re = /\\(begin)\{([^}]*)\}|\\(part|chapter|section|subsection|subsubsection|paragraph|subparagraph)(\*?)|\\\[|\$\$|\\(maketitle|caption|item|bibitem)\b|%|\\\\|\\%/g;
  let at = 0, m;
  while ((m = re.exec(s))) {
    if (m[0] === "%") { para += s.slice(at, m.index); const e = s.indexOf("\n", m.index); at = re.lastIndex = e < 0 ? s.length : e + 1; continue; }
    if (m[0] === "\\\\" || m[0] === "\\%") continue;   // inline business: leave it in the paragraph
    para += s.slice(at, m.index);
    let i = re.lastIndex;
    if (m[1]) {
      const env = m[2], e = endOf(s, i, env), body = e < 0 ? s.slice(i) : s.slice(i, e);
      i = e < 0 ? s.length : e + `\\end{${env}}`.length;
      if (env === "document") { flush(); out.push(...blocks(body, ctx)); }
      else if (MATHENV.test(env)) {
        flush();
        const base = env.replace("*", ""), inner = body.replace(/\\label\{[^}]*\}/g, "").replace(/\\(nonumber|notag)\b/g, "").trim();
        let tag = "";
        if (base === "equation" && !env.endsWith("*")) { tag = String(++ctx.eq); for (const l of body.matchAll(/\\label\{([^}]*)\}/g)) ctx.labels[l[1]] = tag; }
        const tex = /^(align|eqnarray|flalign|alignat)$/.test(base) ? `\\begin{aligned}${inner}\\end{aligned}` : /^(gather|multline)$/.test(base) ? `\\begin{gathered}${inner}\\end{gathered}` : inner;
        out.push(equation(tag ? tex + `\\tag{${tag}}` : tex));
      } else if (VERB.test(env)) {
        flush(); if (env === "comment") { at = re.lastIndex = i; continue; }
        let lang = "", code = body.replace(/^\[([^\]]*)\]/, (x, o) => { lang = (/language=(\w+)/i.exec(o) || [])[1] || ""; return ""; });
        if (env === "minted") code = code.replace(/^\{([^}]*)\}/, (x, l) => { lang = l; return ""; });
        out.push(codeBlock(lang.toLowerCase(), "", code.replace(/^[ \t]*\n/, "").replace(/\n[ \t]*$/, "")));
      }
      else if (/^(itemize|enumerate|description)$/.test(env)) {
        flush();
        const items = split(body, "\\\\item\\b").slice(1).map(it => {
          const o = opt(it, 0), lbl = o && env === "description" ? o[0] : null, rest = o ? it.slice(o[1]) : it;
          return h("li", null, lbl ? h("strong", null, inline(lbl, ctx), " ") : null, blocks(rest, ctx));
        });
        out.push(h(env === "enumerate" ? "ol" : "ul", { class: env === "description" ? "pv-tex-dl" : null }, items));
      } else if (/^(tabular|tabularx|tabular\*|array|longtable)$/.test(env)) { flush(); out.push(table(body, env, ctx)); }
      else if (/^(figure|table|figure\*|table\*|wrapfigure)$/.test(env)) {
        flush();
        const kind = env.startsWith("table") ? "Table" : "Figure", n = kind === "Table" ? ++ctx.tab : ++ctx.fig;
        for (const l of body.matchAll(/\\label\{([^}]*)\}/g)) ctx.labels[l[1]] = String(n);
        const was = ctx.float; ctx.float = kind + " " + n + ". ";
        out.push(h("figure", { class: "pv-tex-float" }, blocks(body.replace(/^\s*\[[^\]]*\]/, ""), ctx)));
        ctx.float = was;
      }
      else if (env === "abstract") { flush(); out.push(h("div", { class: "pv-tex-abs" }, h("p", { class: "pv-tex-abs-h" }, "Abstract"), blocks(body, ctx))); }
      else if (/^(quote|quotation|verse)$/.test(env)) { flush(); out.push(h("blockquote", null, blocks(body, ctx))); }
      else if (env === "thebibliography") { flush(); out.push(h("h2", null, "References"), h("ul", { class: "pv-tex-bib" }, split(args(body, 0, 1)[1] ? body.slice(args(body, 0, 1)[1]) : body, "\\\\bibitem\\b").slice(1).map(b => { const [k, j] = args(b, 0, 1); return h("li", null, h("strong", null, "[" + k + "] "), inline(b.slice(j), ctx)); }))); }
      else if (/^(theorem|lemma|proof|definition|corollary|proposition|remark|example)$/.test(env)) { flush(); out.push(h("div", { class: "pv-tex-thm" }, h("strong", null, env[0].toUpperCase() + env.slice(1) + ". "), blocks(body, ctx))); }
      else { flush(); out.push(...blocks(body, ctx)); }   // center, minipage, and the rest: their contents
    } else if (m[3]) {
      flush();
      const [a, j] = args(s, i, 1); i = j;
      const lvl = SECT[m[3]], num = !m[4] && lvl >= 2 && lvl <= 4 ? ctx.count(lvl) : "", lb = /^\s*\\label\{([^}]*)\}/.exec(s.slice(i, i + 200));
      if (lb) { if (num) ctx.labels[lb[1]] = num; i += lb[0].length; }
      out.push(h("h" + lvl, null, num ? num + " " : null, inline(a, ctx)));
    } else if (m[0] === "\\[" || m[0] === "$$") {
      const close = s.indexOf(m[0] === "\\[" ? "\\]" : "$$", i);
      if (close < 0) { para += m[0]; at = i; continue; }
      flush(); out.push(equation(s.slice(i, close).trim())); i = close + 2;
    } else if (m[5] === "maketitle") {
      flush();
      const t = ctx.meta;
      if (t.title || t.author) out.push(h("header", { class: "pv-tex-title" }, t.title ? h("h1", null, inline(t.title, ctx)) : null,
        t.author ? h("p", { class: "pv-tex-author" }, inline(t.author, ctx)) : null, t.date && t.date.trim() !== "\\today" ? h("p", { class: "meta" }, inline(t.date, ctx)) : null));
    } else if (m[5] === "caption") {
      flush(); const [a, j] = args(s, i, 1); i = j;
      out.push(h("figcaption", null, ctx.float ? h("strong", null, ctx.float) : null, inline(a, ctx)));
    } else { para += m[0]; at = i; continue; }   // a stray \item or \bibitem outside its list
    at = re.lastIndex = i;
  }
  para += s.slice(at); flush();
  return out;
}

function table(body, env, ctx) {
  let [spec, j] = env === "tabular*" || env === "tabularx" ? args(body, 0, 2) : args(body, 0, 1);
  const rows = split(body.slice(j), "\\\\\\\\").map(r => r.replace(/\\(hline|toprule|midrule|bottomrule)\b|\\c?midrule(\([^)]*\))?\{[^}]*\}|\\cline\{[^}]*\}/g, "").trim()).filter(Boolean);
  const cells = rows.map(r => split(r, "&").map(c => {
    const mc = /^\s*\\multicolumn\{(\d+)\}\{[^}]*\}/.exec(c);
    if (!mc) return { span: 1, c };
    const g = arg(c, mc[0].length); return { span: +mc[1], c: g ? g[0] : "" };
  }));
  const right = spec ? [...spec.replace(/\{[^}]*\}|[|@!]/g, "")].map(x => x === "r") : [];
  return h("div", { class: "md-tw" }, h("table", null, h("tbody", null, cells.map((r, ri) => h("tr", null, r.map((x, ci) =>
    h(ri === 0 && rows.length > 1 ? "th" : "td", { colspan: x.span > 1 ? String(x.span) : null, style: right[ci] ? "text-align:right" : null }, inline(x.c.trim(), ctx))))))));
}

/** The reading view of a .tex file's text. sibling(name) gives the URL of a file sent alongside it, or null. */
export function latex(src, { sibling } = {}) {
  src = src.split(/(\\begin\{(?:verbatim|lstlisting|minted)\}[\s\S]*?\\end\{(?:verbatim|lstlisting|minted)\})/)   // comments, except in verbatim
    .map((part, k) => (k % 2 ? part : part.replace(/(^|[^\\])%.*$/gm, "$1"))).join("");
  const meta = {};
  for (const k of ["title", "author", "date"]) { const at = src.search(new RegExp(`\\\\${k}\\s*[[{]`)); if (at >= 0) { const [a] = args(src, at + k.length + 1, 1); meta[k] = a; } }
  const begin = src.indexOf("\\begin{document}"), body = begin >= 0 ? src.slice(begin) : src;
  const counts = [0, 0, 0, 0, 0];
  const ctx = { meta, notes: [], labels: {}, refs: [], sibling, eq: 0, fig: 0, tab: 0, float: "", count: lvl => { counts[lvl]++; counts.fill(0, lvl + 1); return counts.slice(2, lvl + 1).join("."); } };
  const out = blocks(body, ctx);
  if (begin >= 0 && !/\\maketitle/.test(body) && meta.title) out.unshift(h("h1", null, inline(meta.title, ctx)));
  if (ctx.notes.length) out.push(h("ol", { class: "pv-tex-notes meta" }, ctx.notes.map(n => h("li", null, inline(n, ctx)))));
  for (const r of ctx.refs) r.textContent = r._key in ctx.labels ? (r._paren ? "(" + ctx.labels[r._key] + ")" : ctx.labels[r._key]) : "??";
  return h("div", { class: "md pv-tex" }, out.length ? out : h("p", { class: "meta" }, "No text in it."));
}
