// On-demand inventory of loaded messages. Reading placeholders never draws a figure.
import { h } from "../core/dom.js";

export function openRail() {
  if (document.querySelector("dialog.rc-rail")) return;
  let tab = "Outline", entries = [];
  const list = h("div", { class: "rc-rail-b" }), tabs = h("div", { class: "rc-rail-tabs" });
  const find = h("input", { class: "input rc-find-q", type: "search", placeholder: "Find in contents", "aria-label": "Find in contents", oninput: render });
  const dlg = h("dialog", { class: "rc-rail", "aria-label": "What's in this thread" },
    h("div", { class: "rc-rail-head" }, h("b", null, "What's in this thread"),
      h("button", { type: "button", class: "rc-t", onclick: () => dlg.close() }, "Close")),
    h("p", { class: "meta" }, "Loaded messages · open an item to jump to it"), tabs, find, list,
    h("button", { type: "button", class: "rc-t", onclick: refresh }, "Refresh contents"));
  function refresh() {
    entries = [];
    for (const post of document.querySelectorAll(".cm-msgs article.msg")) {
      const author = post.getAttribute("aria-label") || [...(post.querySelector(".msg-h")?.children || [])].map(n => n.textContent.trim()).filter(Boolean).join(" · ") || "Message";
      const add = (type, el, label) => entries.push({ type, el, label: label.trim().slice(0, 180), author });
      for (const el of post.querySelectorAll(".md .md-h1,.md .md-h2,.md .md-h3,.md .md-h4,.md .md-h5,.md .md-h6")) add("Outline", el, el.textContent);
      for (const el of post.querySelectorAll(".rc-fig,.rc-block,.md-tw")) {
        const lang = el.querySelector(".rc-lang")?.textContent || "";
        const type = el.matches(".rc-block:not(.rc-fig),.md-tw") || /^(csv|tsv)$/i.test(lang) ? "Code & tables" : "Figures";
        add(type, el, el.querySelector(".rc-ttl")?.textContent || lang || (el.matches(".md-tw") ? "Table" : "Code"));
      }
      for (const el of post.querySelectorAll(".msg-files .msg-file,.msg-files .msg-thumb")) add("Files", el, el.querySelector(".msg-fn,img")?.getAttribute("alt") || el.querySelector(".msg-fn")?.textContent || el.textContent);
    }
    render();
  }
  function jump(el) {
    if (!el.isConnected) { refresh(); return; }
    for (let n = el.parentElement; n; n = n.parentElement) if (n.tagName === "DETAILS") n.open = true;
    dlg.close();
    el.scrollIntoView({ block: "center", behavior: "smooth" });
    if (!el.hasAttribute("tabindex")) el.setAttribute("tabindex", "-1");
    el.focus({ preventScroll: true });
  }
  function render() {
    tabs.replaceChildren(...["Outline", "Figures", "Code & tables", "Files"].map(name => h("button", { type: "button", "aria-pressed": String(tab === name), onclick: () => { tab = name; render(); } }, name)));
    const q = find.value.trim().toLowerCase();
    const rows = entries.filter(x => x.type === tab && (!q || (x.label + " " + x.author + " " + x.el.textContent).toLowerCase().includes(q)));
    list.replaceChildren(...(rows.length ? rows.map(x => h("button", { type: "button", class: "rc-ri", onclick: () => jump(x.el) }, x.label, h("small", null, x.author))) : [h("p", { class: "meta" }, "No matching items in loaded messages.")]));
  }
  dlg.addEventListener("close", () => dlg.remove());
  document.body.append(dlg); refresh(); dlg.showModal();
}
