// A view whose file loads when its page first opens, not before Chat. meta is what the bar needs without it (id, label, about, tab);
// load imports the file, whose default export gives enter and render (the only two a lazy view has today).
import { h } from "../core/dom.js";
import { empty } from "../ui/kit.js";

export function lazyView(meta, load) {
  let view = null, loading = null, failed = "", open = false;
  const get = ctx => loading ||= load().then(m => {
    view = m.default;
    if (open) { if (view.enter) view.enter(ctx); ctx.update(); }
  }, e => { loading = null; failed = e.message; if (open) ctx.update(); });
  return { ...meta,
    enter(ctx) { open = true; failed = ""; if (view) { if (view.enter) view.enter(ctx); } else get(ctx); },
    leave() { open = false; },
    render(ctx) {
      if (view) return view.render(ctx);
      return h("div", { class: "page", "data-width": "narrow" }, failed ? empty("Couldn't load this page. Reload to try again. " + failed, { kind: "error", title: meta.label }) : empty("Reading…", { kind: "loading" }));
    },
  };
}
