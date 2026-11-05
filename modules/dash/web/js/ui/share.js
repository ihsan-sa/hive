// Share this result: one conversation as one page its owner can post. The dash builds it (share.py) and the browser saves it; nothing is uploaded.
// First a preview with every message and a Remove button on each, and one plain line that what's on the page becomes public if it is posted.
import { h } from "../core/dom.js";
import { jget, post } from "../core/store.js";
import { button } from "./kit.js";
import { confirm } from "./dialog.js";

export async function shareDialog(th) {
  let pv;
  try { pv = await jget("/api/share-preview?thread=" + encodeURIComponent(th)); }
  catch { return confirm({ title: "Share this result", body: ["Nothing to share here yet."], ok: "Close", onOk: () => {} }); }
  const gone = new Set(), withFiles = h("input", { type: "checkbox", checked: "" });
  const rows = pv.items.map(i => {
    const text = h("div", { class: "sh-text" }, i.text || (i.files || []).map(f => "File: " + f.name).join(", "));
    const b = button("Remove", () => { gone.has(i.id) ? gone.delete(i.id) : gone.add(i.id); row.toggleAttribute("data-gone", gone.has(i.id)); b.textContent = gone.has(i.id) ? "Put back" : "Remove"; }, { size: "s", kind: "quiet", title: "Leave this message out of the page" });
    const row = h("div", { class: "sh-row" }, h("div", { class: "sh-who" }, h("b", null, i.who === "asker" ? "You" : i.who), b), text);
    return row;
  });
  const hasFiles = pv.files.length > 0;
  return confirm({
    title: "Share this result", ok: "Save the page", failText: "Not saved",
    body: [h("p", { class: "sh-notice" }, pv.notice),
      h("p", { class: "meta" }, `${pv.agents.length} agent${pv.agents.length === 1 ? "" : "s"}, ${pv.items.length} messages. The page is one file; you choose where to put it.`),
      hasFiles ? h("label", { class: "opt" }, withFiles, "Include pictures and small text files you were sent") : null,
      h("div", { class: "sh-list", tabindex: "0", "aria-label": "Messages on the page" }, rows)],
    onOk: async () => {
      const r = await post("/api/share-build", { thread: th, remove: [...gone], files: withFiles.checked });
      const u = URL.createObjectURL(new Blob([r.html], { type: "text/html;charset=utf-8" }));
      const a = h("a", { href: u, download: r.name }); document.body.append(a); a.click(); a.remove();
      setTimeout(() => URL.revokeObjectURL(u), 5000);
      return `Saved as ${r.name} in your downloads. It is a single page that works offline.`;
    },
  });
}
