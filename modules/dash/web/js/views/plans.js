// Plans: each project's goals.md. Saving can start an agent on it; a copy that went stale is refused by the server.
import { h } from "../core/dom.js";
import { S, can, jget, post } from "../core/store.js";
import { button, empty, field, input, list, pageHeader, section } from "../ui/kit.js";

const F = { items: null, open: null, text: "", version: null, isNew: false, name: "", say: null, tone: null, asked: false };
const say = (text, tone) => { F.say = text; F.tone = tone || null; };

async function load(ctx) {
  try { F.items = await jget("/api/plans"); } catch (e) { F.items = F.items || []; say(e.message, "bad"); }
  ctx.update();
}
function open(p, ctx) { Object.assign(F, { open: p.name, text: p.text, version: p.version, isNew: false, say: null }); ctx.update(); }
function fresh(ctx) { Object.assign(F, { open: "", text: "", version: null, isNew: true, name: "", say: null }); ctx.update(); }

export default {
  id: "plans", label: "Plans", tab: () => S.app.app && can("plans"),
  render(ctx) {
    if (!S.app.app || !can("plans")) return h("div", { class: "page" }, empty("Plans need full mode. Sign in again if your hour is up.", { kind: "page", title: "Plans" }));
    if (!F.items) { if (!F.asked) { F.asked = true; load(ctx); } return h("div", { class: "page" }, empty("Reading…", { kind: "loading" })); }
    const save = async start => {
      const name = F.isNew ? F.name.trim() : F.open;
      try {
        const r = await post("/api/plan", { name, text: F.text, version: F.version, start });
        Object.assign(F, { open: name, version: r.version, isNew: false });
        say(start ? "Saved, and an agent is starting on it." : "Saved. Nothing was started.", "good");
        await load(ctx);
      } catch (e) { say(e.message, "bad"); ctx.update(); }
    };
    const editing = F.open !== null;
    return h("div", { class: "page" },
      pageHeader({ title: "Plans", lead: "What each project is for, in its goals.md. Agents read and edit these too.", actions: button("New project", () => fresh(ctx), { size: "s" }) }),
      editing ? section({ title: F.isNew ? "New project" : F.open },
        F.isNew ? field("Name", input({ value: F.name, autocomplete: "off", oninput: (e, n) => { F.name = n.value; } })) : null,
        h("textarea", { class: "input", rows: 18, style: "width:100%", "aria-label": "Plan", value: F.text, oninput: (e, n) => { F.text = n.value; } }),
        h("div", { class: "frow" }, button("Save", () => save(false), { kind: "primary" }), button("Save and start an agent", () => save(true)),
          button("Close", () => { Object.assign(F, { open: null, isNew: false, say: null }); ctx.update(); }, { kind: "quiet" }),
          F.say ? h("p", { class: "fmsg", "data-tone": F.tone, role: "status" }, F.say) : null)) : null,
      section({ title: "Projects" }, list(F.items.map(p => ({
        key: p.name, title: p.name, onClick: () => open(p, ctx),
        text: p.version ? p.text.split("\n").find(l => l.trim()) || "" : "No goals.md yet",
      })), { empty: "No projects yet." })));
  },
};
