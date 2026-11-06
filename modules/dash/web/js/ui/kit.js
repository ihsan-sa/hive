// The component kit. Each factory takes plain data and returns DOM; styles live in components.css under the same name.
import { h, copyText } from "../core/dom.js";
import { status as st } from "../core/status.js";
import { icon } from "./icons.js";
import { showTip, hideTip } from "./tip.js";
export { icon };

/** Page header: the answer first. {title, lead, tone, meta:[...], actions, after} */
export function pageHeader({ title, lead, tone, meta, actions, after, size, state }) {
  return h("header", { class: "ph", "data-size": size || null },
    state ? h("p", { class: "ph-state", "data-tone": state.tone || null }, h("span", { class: "sd", "data-tone": state.tone || null, "aria-hidden": "true" }), state.text) : null,
    title || actions ? h("div", { class: "ph-row" }, title ? h("h1", { class: "ph-title" }, title) : null, actions ? h("div", { class: "ph-actions" }, actions) : null) : null,
    lead ? h("p", { class: "ph-lead", "data-tone": tone || null }, lead) : null,
    after || null,
    meta && meta.filter(Boolean).length ? h("p", { class: "ph-meta" }, meta.filter(Boolean).map(m => h("span", null, m))) : null);
}

/** A line that says a refresh failed and the page shows what it read last (or leaves a part out). Goes in pageHeader's after. */
export const staleNote = text => h("p", { class: "fmsg", "data-tone": "warn", role: "status" }, text);

// An "i" that says its sentence on hover, and on a click or a tap too (a title alone never shows on a phone or from the keyboard).
let saying = null;
const stopSaying = () => { saying = null; hideTip(); removeEventListener("pointerdown", onAway, true); removeEventListener("keydown", onEsc, true); removeEventListener("scroll", stopSaying, true); };
const onAway = e => { if (!e.target.closest || !e.target.closest(".sec-info")) stopSaying(); };
const onEsc = e => { if (e.key === "Escape") stopSaying(); };
export function infoButton(name, text, { size } = {}) {
  const b = button(name, (e, n) => {
    const was = saying === text; stopSaying();
    if (was) return;
    const r = n.getBoundingClientRect(); saying = text; showTip(r.left, r.bottom - 8, text);
    addEventListener("pointerdown", onAway, true); addEventListener("keydown", onEsc, true); addEventListener("scroll", stopSaying, true);
  }, { kind: "icon", icon: "info", cls: "sec-info", title: text, size });
  b.setAttribute("aria-label", name);
  return b;
}

const FOLDS = new Map();
/** Section: a titled block. {title, note, actions, id, level} + body nodes. level 1: a part of a long page, with a rule under its title. */
export function section({ title, note, info, actions, id, key, level, fold, summary }, ...body) {
  // fold: a closed row that opens (summary is its quiet one-line count); info: the long sentence, behind an "i" button.
  // A fold remembers being opened (by key, else title): a refresh re-renders it, and morph drops an open the new tree lacks.
  const fk = fold && (key || id || title);
  if (fold) return h("details", { class: "fold-row", id: id || null, "data-key": key || null, open: FOLDS.get(fk) ? "" : null, ontoggle: e => { FOLDS.set(fk, e.target.open); } },
    h("summary", null, icon("chevron-right"), title, summary ? h("small", null, summary) : null), h("div", { class: "fold-in" }, body));
  return h("section", { class: "sec", id: id || null, "data-key": key || null, "data-level": level || null, "aria-label": title || null },
    title ? h("header", { class: "sec-h" }, h("h2", { class: "sec-t" }, title), info ? infoButton("About " + title, info) : null, note ? h("span", { class: "sec-n" }, note) : null, actions ? h("div", { class: "sec-a" }, actions) : null) : null,
    body);
}

/** Status dot: shape + colour from the STATUS table. */
export function statusDot(key, { live } = {}) {
  const S = st(key);
  return h("span", { class: "sd", "data-tone": S.tone, "data-shape": S.shape, "data-live": live ? "" : null, "aria-hidden": "true" });
}
/** A warden's mark, the hexagon the graphs draw around a warden. */
export const wardenMark = () => h("span", { class: "wm", "aria-hidden": "true" });
/** The word and the mark, for the head of a warden's page. */
export const wardenBadge = title => h("span", { class: "wb", title }, wardenMark(), "Warden");
/** Status label: dot and words, coloured by tone. text overrides the table's label. */
export function statusLabel(key, text, { live, title } = {}) {
  const S = st(key);
  return h("span", { class: "sl", "data-tone": S.tone, title: title || S.hint || null }, statusDot(key, { live }), h("span", null, text ?? S.label));
}

/** Tally: counts by status, e.g. 9 working · 12 in job queue. items: [{n, label, status}] */
export function tally(items) {
  return h("ul", { class: "tally" }, items.map(i => h("li", { "data-zero": i.n ? null : "", title: st(i.status).hint || null }, statusDot(i.status), h("b", null, i.n), h("span", null, i.label))));
}

/** Stats: figures in a strip. items: [{label, value, note, tone, meter:{value, stop, tone}}] */
export function stats(items, { card } = {}) {
  return h("dl", { class: ["stats", card ? "card" : null] }, items.filter(Boolean).map(i => h("div", { class: "stat", "data-tone": i.tone || null, title: i.title || null },
    h("dt", null, i.label), h("dd", { class: "stat-v" }, i.value),
    i.meter ? h("dd", null, meter(i.meter)) : null,
    i.worse ? h("dd", { class: "stat-w" }, i.worse) : null,
    i.note ? h("dd", { class: "stat-n" }, i.note) : null)));
}

/** Meter: value 0–100, optional stop line, tone (default by level). */
export function meter({ value, stop, tone, label }) {
  const v = Math.max(0, Math.min(100, value || 0));
  const t = tone || (v >= 90 ? "bad" : v >= 75 ? "warn" : "data");
  return h("div", { class: "meter", "data-tone": t, role: "meter", "aria-valuenow": Math.round(v), "aria-valuemin": 0, "aria-valuemax": 100, "aria-label": label || null, style: `--v:${v}%` + (stop != null ? `;--s:${stop}%` : "") },
    h("i"), stop != null ? h("u", { title: label ? `${label}: stop line at ${stop}%` : `stop line at ${stop}%` }) : null);
}

/** Button. kind: default | primary | danger | quiet. size: s. tone colours quiet text (e.g. bad for Kill). */
export function button(label, onClick, { kind, size, tone, title, disabled, type, cls, href, busy, icon: ic } = {}) {
  // icon: name from icons.js, shown before the label; kind "icon" shows the icon alone, the label becomes aria-label and title.
  const only = kind === "icon";
  const body = ic ? (only ? [icon(ic)] : [icon(ic), h("span", {}, label)]) : [label];
  const a = { class: ["btn", cls], "data-kind": kind || null, "data-size": size || null, "data-tone": tone || null,
    title: title || (only && typeof label === "string" ? label : null), "aria-label": only && typeof label === "string" ? label : null };
  if (href) return h("a", { ...a, href }, ...body);
  return h("button", { ...a, type: type || "button", disabled: disabled || busy || null, "data-busy": busy ? "" : null, "aria-busy": busy ? "true" : null, onclick: onClick }, ...body);
}

/** Segmented choice. options: [[key, label]] */
export function seg(options, value, onChange, { label } = {}) {
  return h("div", { class: "seg", role: "group", "aria-label": label || null },
    options.map(([k, l, t, n, ic]) => h("button", { type: "button", "aria-pressed": String(k === value), title: t || null, onclick: () => onChange(k) }, ic ? icon(ic) : null, l, n != null ? h("i", null, String(n)) : null)));
}

/** Form field: label, control, hint. */
export function field(label, control, hint) {
  return h("label", { class: "field" }, h("span", { class: "field-l" }, label), control, hint ? h("span", { class: "field-h" }, hint) : null);
}
export const input = props => h("input", { class: "input", ...props });

/** Empty, loading and error states. kind: empty | loading | error | page */
/** A page that needs the files right: one wording, one way back. */
export function needFull(what, title) {
  return h("div", { class: "page" }, h("div", { class: "empty", "data-kind": "page" }, title ? h("div", { class: "empty-t" }, title) : null, `${what} needs a full sign-in here.`,
    h("p", null, h("a", { class: "btn", "data-kind": "primary", href: "#settings" }, "Sign in again"))));
}

export function empty(text, { kind = "empty", title } = {}) {
  return h("div", { class: "empty", "data-kind": kind, role: kind === "error" ? "alert" : null }, title ? h("div", { class: "empty-t" }, title) : null, text);
}

/** A page's data hasn't come yet: "Reading…" while it may still come, the plain failure once a read has failed. Pages keep polling, so it clears itself. */
export function waiting(failed, text = "Reading…", what = "the data") {
  return failed ? empty(`Couldn't read ${what}. It keeps trying.`, { kind: "error" }) : empty(text, { kind: "loading" });
}

/** A copy button's press: copy t, then say on the button, for a moment, how it went. It worked: data-ok, and "Copied" on a worded button.
 *  It didn't (no clipboard, the browser refused): data-fail, "Couldn't copy" on a worded button and the reason in its title; an icon
 *  button has only its title and colour to say it with. Before this every copy button kept still when the copy failed. */
export function copyPress(b, t) {
  const word = b.dataset.kind === "icon" ? null : b.querySelector(":scope>span") || (b.childElementCount ? null : b);
  b._was ??= { word: word?.textContent, title: b.title };
  const mark = (k, say, why) => {
    delete b.dataset.ok; delete b.dataset.fail; b.dataset[k] = ""; if (word) word.textContent = say; b.title = why; clearTimeout(b._cpT);
    b._cpT = setTimeout(() => { if (!b.isConnected) return; delete b.dataset[k]; if (word) word.textContent = b._was.word; b.title = b._was.title; }, k === "ok" ? 1400 : 3000);
  };
  return copyText(t).then(() => mark("ok", "Copied", "Copied"), e => mark("fail", "Couldn't copy", e.message));
}

/** List: rows separated by hairlines. rows: [{key, href, title, sub, meta, text, lead, current, onClick, dim}] (dim greys the row); limit + onMore add "Show N more". */
export function list(rows, { empty: none = "Nothing here.", limit, onMore, moreText = n => `Show ${n} more`, dense } = {}) {
  if (!rows.length) return empty(none);
  const shown = limit ? rows.slice(0, limit) : rows;
  return h("div", null,
    h("ul", { class: "list", "data-dense": dense ? "" : null }, shown.map(r => h("li", { class: "lr", "data-key": r.key || null, "data-dim": r.dim ? "" : null },
      h(r.href ? "a" : r.onClick ? "button" : "div", { class: "lr-in", style: r.depth ? `--d:${r.depth}` : null, href: r.href || null, type: !r.href && r.onClick ? "button" : null, "aria-current": r.current ? "true" : null, "aria-expanded": r.expanded == null ? null : String(r.expanded), onclick: r.onClick || null },
        h("div", { class: "lr-t" }, r.lead || null, h("b", { title: typeof r.title === "string" ? r.title : null }, r.title), r.sub ? h("span", { class: "muted" }, r.sub) : null, r.meta ? h("span", { class: "meta" }, r.meta) : null),
        r.text ? h("div", { class: r.whole ? "lr-x" : "lr-x clamp-2" }, r.text) : null),
      r.acts ? h("span", { class: "lr-acts" }, r.acts) : null))),
    rows.length > shown.length ? h("div", { class: "lr-more" }, onMore ? button(moreText(rows.length - shown.length), onMore, { kind: "quiet", size: "s" }) : h("span", { class: "meta" }, `and ${rows.length - shown.length} more`)) : null);
}

/** Rank: ranked bars. rows: [{key, label, value, text, tone, href}] */
export function rank(rows, { empty: none = "Nothing yet.", max } = {}) {
  if (!rows.length) return empty(none);
  const mx = max || Math.max(...rows.map(r => r.value)) || 1;
  return h("ul", { class: "rank" }, rows.map(r => h("li", { "data-key": r.key },
    h(r.href ? "a" : "div", { class: "rank-in", href: r.href || null, onclick: r.onClick || null, "data-tone": r.tone || null, style: `--v:${(100 * r.value) / mx}%` },
      h("span", null, r.label), h("span", null, r.text), h("span", { class: "rank-tr" }, h("i"))))));
}

/** Share: one stacked bar with a legend. parts: [{key, label, value, tone, text}] */
export function share(parts, { total, label, legend: withLegend = true } = {}) {
  const sum = total || parts.reduce((a, p) => a + p.value, 0) || 1;
  return h("div", null,
    h("div", { class: "share-bar", role: "img", "aria-label": label || parts.map(p => `${p.label} ${p.text}`).join(", ") },
      parts.filter(p => p.value > 0).map(p => h("i", { "data-tone": p.tone, style: `flex:${p.value / sum} 0 0`, title: `${p.label}: ${p.text}` })),
      total && total > parts.reduce((a, p) => a + p.value, 0) ? h("i", { style: `flex:${(total - parts.reduce((a, p) => a + p.value, 0)) / sum} 0 0;background:var(--line)` }) : null),
    withLegend ? legend(parts) : null);
}
export function legend(parts) {
  return h("div", { class: "legend" }, parts.map(p => h("span", { "data-tone": p.tone }, h("i", { class: "sw" }), `${p.label}: ${p.text}`)));
}

/** Note: something to read before acting. tone info | warn | bad; title is one line, body short points. */
export function note({ tone = "info", title, actions, key }, ...body) {
  const shape = { info: "dot", warn: "square", bad: "tri" }[tone] || "ring";
  return h("aside", { class: "note", "data-tone": tone, "data-key": key || null },
    h("span", { class: "sd", "data-shape": shape, "aria-hidden": "true" }),
    h("div", { class: "note-in" }, title ? h("p", { class: "note-t" }, title) : null,
      body.flat().filter(Boolean).length ? h("div", { class: "note-b" }, body) : null,
      actions ? h("div", { class: "note-a" }, actions) : null));
}

/** Block: one setting or account, in a .blks group. {title, sub, state, actions, key, fold, open, onToggle}.
 *  fold makes it a <details>: keep open in view state and pass onToggle, or a refresh closes it. Put actions in the body when folded. */
export function block({ title, sub, state, actions, key, fold, open, onToggle }, ...body) {
  const head = [h("b", { class: "blk-t" }, title), sub ? h("span", { class: "blk-s" }, sub) : null, state || null, actions ? h("span", { class: "blk-a" }, actions) : null];
  const b = body.flat().filter(Boolean), inner = b.length ? h("div", { class: "blk-b" }, b) : null;
  if (fold) return h("details", { class: "blk", "data-key": key || null, open: open ? "" : null, ontoggle: onToggle ? e => onToggle(e.target.open) : null }, h("summary", { class: "blk-h" }, head), inner);
  return h("div", { class: "blk", "data-key": key || null }, h("div", { class: "blk-h" }, head), inner);
}
export const blocks = (...kids) => h("div", { class: "blks" }, kids);

/** Command: a line to copy, with a Copy button. */
export function cmd(text) {
  return h("div", { class: "cmd" }, h("code", null, text),
    button("Copy", (e, b) => { navigator.clipboard?.writeText(text).then(() => { b.textContent = "Copied"; setTimeout(() => (b.textContent = "Copy"), 1200); }); }, { size: "s" }));
}

/** Toolbar: a row of filters and controls above a list or a canvas. */
export const toolbar = (...kids) => h("div", { class: "toolbar" }, kids);

/** Definitions: a strip of short label/value pairs. rows: [{k, v, hint}] */
export function defs(rows) {
  if (!rows.length) return null;
  return h("dl", { class: "defs" }, rows.map(r => h("div", { "data-key": r.k, title: r.hint || null }, h("dt", null, r.k), h("dd", null, r.v))));
}
