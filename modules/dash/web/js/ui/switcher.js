// Quick switcher (Ctrl+K) and the shortcuts sheet (?). Both are plain dialogs built once per open.
import { h } from "../core/dom.js";
import { S, can } from "../core/store.js";
import { href, agentHref, threadHref } from "../core/router.js";
import { threadTitle, threadKinds, SHOW } from "../core/model.js";
import { simpleOn } from "../core/simple.js";
import { SIMPLE_NAV } from "../core/nav.js";
import { pinned, important } from "./threadmeta.js";
import { button } from "./kit.js";
import { FEEDBACK_KEY } from "./feedback.js";

const MAC = /Mac|iPhone|iPad/.test(navigator.platform || "");
const MOD = MAC ? "⌘" : "Ctrl+";
let views = [], open = null;
const WHAT = () => simpleOn() ? "Find a conversation or page" : "Find a conversation, agent or page";
const typing = e => { const t = e.target; return t && (t.isContentEditable || /^(input|textarea|select)$/i.test(t.tagName)); };

function dialog(label, ...body) {
  if (open) open.close();
  const dlg = h("dialog", { class: "dlg qs", "aria-label": label }, ...body);
  dlg.addEventListener("close", () => { dlg.remove(); if (open === dlg) open = null; });
  dlg.addEventListener("click", e => { if (e.target === dlg) dlg.close(); });
  document.body.append(dlg); dlg.showModal(); open = dlg;
  return dlg;
}

/** Everything the box can jump to: pages, agents, conversations. */
function items() {
  const out = [], simple = simpleOn();
  for (const v of views) if (simple && !SIMPLE_NAV.main.includes(v.id)) continue; else if (v.label && v.id !== "agent" && v.id !== "deck" && v.tab !== false && (typeof v.tab !== "function" || v.tab())) out.push({ kind: "Page", name: typeof v.label === "function" ? v.label() : v.label, to: href(v.id) });
  if (!simple) for (const a of S.agents || []) out.push({ kind: "Agent", name: a, to: agentHref(a) });
  for (const d of S.threads.values()) if (!simple || pinned(d.name) || important(d.name) || (threadKinds(d) & (SHOW.you | SHOW.updates)) && d.posts.some(p => p.from === "person")) out.push({ kind: "Chat", name: threadTitle(d.name) || d.name, to: threadHref(d.name), last: d.last || 0 });
  return out;
}
const score = (it, q) => { const n = String(it.name).toLowerCase(); return !q ? 1 : n.startsWith(q) ? 3 : n.split(/[\s\-_/]+/).some(w => w.startsWith(q)) ? 2 : n.includes(q) ? 1 : 0; };

export function openSwitcher() {
  const all = items(); let sel = 0, shown = [];
  const box = h("input", { class: "input", type: "search", autocomplete: "off", spellcheck: "false", placeholder: WHAT(), "aria-label": WHAT(), role: "combobox", "aria-expanded": "true", "aria-controls": "qs-list" });
  const list = h("div", { class: "qs-list", id: "qs-list", role: "listbox" });
  const dlg = dialog("Quick switcher", h("div", { class: "qs-in" }, box), list);
  const go = it => { dlg.close(); if (it) location.hash = it.to; };
  const draw = () => {
    const q = box.value.trim().toLowerCase();
    shown = all.map(it => [it, score(it, q)]).filter(x => x[1]).sort((a, b) => b[1] - a[1] || (b[0].last || 0) - (a[0].last || 0)).slice(0, 12).map(x => x[0]);
    sel = Math.min(sel, Math.max(0, shown.length - 1));
    list.replaceChildren(...(shown.length ? shown.map((it, i) => h("button", { type: "button", id: `qs-o-${i}`, class: "qs-i", role: "option", "aria-selected": String(i === sel), onclick: () => go(it) }, h("b", null, it.name), h("span", null, it.kind)))
      : [h("p", { class: "qs-none" }, "Nothing matches.")]));
    if (shown.length) box.setAttribute("aria-activedescendant", `qs-o-${sel}`); else box.removeAttribute("aria-activedescendant");
  };
  box.addEventListener("input", () => { sel = 0; draw(); });
  box.addEventListener("keydown", e => {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") { e.preventDefault(); sel = (sel + (e.key === "ArrowDown" ? 1 : -1) + shown.length) % (shown.length || 1); draw(); list.children[sel]?.scrollIntoView({ block: "nearest" }); }
    else if (e.key === "Enter") { e.preventDefault(); if (shown[sel]) go(shown[sel]); }
  });
  draw(); box.focus();
}

export function openShortcuts() {
  const rows = [
    [MOD + "K", WHAT()], ["?", "This list"], [FEEDBACK_KEY, "Send feedback"],
    ["F", "Full screen"], simpleOn() ? ["↑", "Edit your last message, from the empty box"] : ["Tab, ↑", "Complete an @name in the box; ↑ edits your last message"],
    [MOD + "B / I / E / K", "In the message box: bold, italic, code, link"], ["Esc", "Close what is open"],
  ];
  const dlg = dialog("Keyboard shortcuts", h("h2", null, "Keyboard shortcuts"),
    h("dl", { class: "qs-keys" }, rows.map(([k, t]) => [h("dt", null, h("kbd", null, k)), h("dd", null, t)])),
    h("div", { class: "dlg-a" }, button("Close", () => dlg.close(), { kind: "primary" })));
}

export function initSwitcher(v) {
  views = v;
  addEventListener("keydown", e => {
    if (e.defaultPrevented || e.altKey || document.querySelector("dialog[open]:not(.qs)")) return;
    if ((e.ctrlKey || e.metaKey) && !e.shiftKey && e.key.toLowerCase() === "k" && !e.target.closest?.(".cp")) { e.preventDefault(); open && open.classList.contains("qs") && open.getAttribute("aria-label") === "Quick switcher" ? open.close() : openSwitcher(); }
    else if (e.key === "?" && !e.ctrlKey && !e.metaKey && !typing(e)) { e.preventDefault(); openShortcuts(); }
  });
}
