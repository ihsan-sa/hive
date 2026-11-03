// The small formula language of the plot fence. A hand-written parser that builds closures: no eval, and a name it doesn't
// know is an error, so post text can only ever reach arithmetic and the functions listed here.
const num = /^(?:\d+\.?\d*|\.\d+)(?:[eE][-+]?\d+)?(meg|[pnumkMGT])?(?![\w.])/;
const SI = { p: 1e-12, n: 1e-9, u: 1e-6, m: 1e-3, k: 1e3, M: 1e6, G: 1e9, T: 1e12, meg: 1e6 };
const MAXLEN = 2000, MAXDEPTH = 60;

function lex(t) {
  const out = []; let i = 0;
  while (i < t.length) {
    const c = t[i];
    if (c === " " || c === "\t") { i++; continue; }
    const m = /[\d.]/.test(c) && num.exec(t.slice(i, i + 48));
    if (m) { out.push({ n: parseFloat(m[0]) * (m[1] ? SI[m[1]] : 1) }); i += m[0].length; continue; }
    const id = /^[A-Za-z_]\w*/.exec(t.slice(i, i + 64));
    if (id) { out.push({ id: id[0] }); i += id[0].length; continue; }
    if ("+-*/^(),".includes(c)) { out.push({ op: c }); i++; continue; }
    throw new Error(`"${t.slice(i, i + 12)}" isn't something a formula can use`);
  }
  return out;
}

const fns = deg => {
  const k = deg ? Math.PI / 180 : 1;
  return Object.assign(Object.create(null), {
    abs: Math.abs, sqrt: Math.sqrt, exp: Math.exp, log: Math.log, log10: Math.log10,
    sin: x => Math.sin(x * k), cos: x => Math.cos(x * k), tan: x => Math.tan(x * k), atan: x => Math.atan(x) / k,
    min: Math.min, max: Math.max,
  });
};

/** Compile text to f(env). names: the Set of names it may read. deg: sin, cos, tan and atan work in degrees. */
export function compile(text, names, deg = true) {
  if (text.length > MAXLEN) throw new Error("That formula is too long.");
  const T = lex(text), F = fns(deg); let p = 0, depth = 0;
  const peek = o => T[p]?.op === o, need = o => { if (!peek(o)) throw new Error(`"${text}": expected ${o}`); p++; };
  const nest = f => { if (++depth > MAXDEPTH) throw new Error("That formula is nested too deeply."); const r = f(); depth--; return r; };
  const sum = () => { let a = prod(); while (peek("+") || peek("-")) { const o = T[p++].op, l = a, r = prod(); a = o === "+" ? e => l(e) + r(e) : e => l(e) - r(e); } return a; };
  const prod = () => { let a = unary(); while (peek("*") || peek("/")) { const o = T[p++].op, l = a, r = unary(); a = o === "*" ? e => l(e) * r(e) : e => l(e) / r(e); } return a; };
  const unary = () => nest(() => { if (peek("-")) { p++; const a = unary(); return e => -a(e); } if (peek("+")) { p++; return unary(); } return pow(); });
  const pow = () => { const b = atom(); if (!peek("^")) return b; p++; const x = unary(); return e => Math.pow(b(e), x(e)); };
  const atom = () => {
    const t = T[p++];
    if (!t) throw new Error(`"${text}" ends too soon`);
    if (t.n !== undefined) { const v = t.n; return () => v; }
    if (t.op === "(") { const a = nest(sum); need(")"); return a; }
    if (t.id) {
      if (peek("(")) {
        const f = F[t.id]; if (!f) throw new Error(`There's no function called ${t.id}`);
        p++; const args = [nest(sum)]; while (peek(",")) { p++; args.push(nest(sum)); } need(")");
        if (args.length === 1) { const a = args[0]; return e => f(a(e)); }
        if (t.id !== "min" && t.id !== "max") throw new Error(`${t.id} takes one value`);
        return e => f(...args.map(a => a(e)));
      }
      if (names.has(t.id)) { const k = t.id; return e => e[k]; }
      if (t.id === "pi") return () => Math.PI;
      throw new Error(`Nothing here is called ${t.id}`);
    }
    throw new Error(`"${text}": unexpected ${t.op}`);
  };
  const a = sum();
  if (p < T.length) throw new Error(`"${text}": unexpected ${T[p].op || T[p].id || T[p].n}`);
  return a;
}

/** The number a range or a mark gives: 120k, -3, 1.5e6. */
export function constant(text) { return compile(text, new Set())({}); }
export const usesTrig = text => /\b(?:sin|cos|tan|atan)\s*\(/.test(text);
