// Export a conversation (the person, 11041325e278): Markdown with simple formatting, downloaded as a file;
// PDF laid out as a clean transcript (a short header with the dates and who speaks, a line per day, speakers in a column
// on the left; the person, 110413284527, wanted the script layout but not the play trimmings), through the browser's print dialog, so it works on every install with no PDF tool on the server.
import { h } from "../core/dom.js";
import { S, SYS } from "../core/store.js";
import { hiveName } from "../core/simple.js";
import { displayText, threadTitle, btwParent, btwText } from "../core/model.js";
import { clock, short } from "../core/format.js";
import { markdown } from "./markdown.js";

const day = t => new Date(t).toLocaleDateString([], { weekday: "long", year: "numeric", month: "long", day: "numeric" });
const dayKey = t => new Date(t).toDateString();
const posts = th => (S.threads.get(th) || { posts: [] }).posts.filter(p => p.text || (p.files || []).length);
// Who said it, as the script names them: you, the Hive (system notes and a side question's opener), or the agent.
function speaker(p) {
  if (SYS.has(p.from)) return { name: hiveName(), stage: true };
  if (p.from === "person") return btwParent(p.th) && posts(p.th)[0] === p ? { name: hiveName(), stage: true } : { name: "You" };
  return { name: p.from };
}
const words = p => { const t = String(displayText(p)).trim(); return btwParent(p.th) && posts(p.th)[0] === p ? btwText(t) : t; };
const fileName = (th, ext) => (short(threadTitle(th), 60).replace(/[^\w.-]+/g, "-").replace(/^-+|-+$/g, "") || "conversation") + "." + ext;

export function toMarkdown(th) {
  const ps = posts(th), out = [`# ${threadTitle(th)}`, ""];
  if (ps.length) out.push(`*${ps.length} message${ps.length === 1 ? "" : "s"}, ${day(ps[0].t)}${dayKey(ps[0].t) !== dayKey(ps.at(-1).t) ? " to " + day(ps.at(-1).t) : ""}. Exported from ${hiveName()}.*`, "");
  let last = "";
  for (const p of ps) {
    if (dayKey(p.t) !== last) { last = dayKey(p.t); out.push(`## ${day(p.t)}`, ""); }
    const s = speaker(p), files = (p.files || []).map(f => f.name);
    out.push(`**${s.name}** · ${clock(p.t)}`, "", words(p) || "", ...(files.length ? ["", "Attached: " + files.join(", ")] : []), "");
  }
  return out.join("\n").replace(/\n{3,}/g, "\n\n").trim() + "\n";
}

export function downloadMarkdown(th) {
  const u = URL.createObjectURL(new Blob([toMarkdown(th)], { type: "text/markdown;charset=utf-8" }));
  const a = h("a", { href: u, download: fileName(th, "md") }); document.body.append(a); a.click(); a.remove();
  setTimeout(() => URL.revokeObjectURL(u), 5000);
}

// The transcript, as a page of its own that only print shows (css: .xp in tail.css).
export function scriptPage(th) {
  const ps = posts(th), cast = [...new Set(ps.map(p => speaker(p)).filter(s => !s.stage).map(s => s.name))];
  const when = ps.length ? day(ps[0].t) + (dayKey(ps[0].t) !== dayKey(ps.at(-1).t) ? " to " + day(ps.at(-1).t) : "") : "";
  const kids = [h("header", { class: "xp-head" },
    h("h1", null, threadTitle(th)),
    h("p", { class: "xp-meta" }, [hiveName(), `${ps.length} message${ps.length === 1 ? "" : "s"}`, when].filter(Boolean).join(" · ")),
    cast.length ? h("p", { class: "xp-meta" }, "With " + cast.join(", ")) : null)];
  let last = "";
  for (const p of ps) {
    if (dayKey(p.t) !== last) { last = dayKey(p.t); kids.push(h("h2", { class: "xp-day" }, day(p.t))); }
    const s = speaker(p), files = (p.files || []).map(f => f.name);
    kids.push(h("section", { class: "xp-turn", "data-stage": s.stage ? "" : null },
      h("div", { class: "xp-who" }, h("b", null, s.name), h("time", null, clock(p.t))),
      h("div", { class: "xp-says" }, markdown(words(p)), files.length ? h("p", { class: "xp-files" }, "Attached: " + files.join(", ")) : null)));
  }
  return h("article", { class: "xp", "aria-hidden": "true" }, kids);
}

export function printScript(th) {
  document.querySelectorAll(".xp").forEach(x => x.remove());
  const page = scriptPage(th), was = document.title;
  document.body.append(page); document.documentElement.classList.add("xp-on");
  document.title = fileName(th, "pdf").replace(/\.pdf$/, "");   // the browser names the PDF from the title
  const done = () => { page.remove(); document.documentElement.classList.remove("xp-on"); document.title = was; removeEventListener("afterprint", done); };
  addEventListener("afterprint", done);
  setTimeout(() => window.print(), 50);
}
