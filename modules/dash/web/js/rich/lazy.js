// Draw expensive things only near the screen, a few at a time, when the browser is idle.
// <rc-lazy data-kind=K> costs nothing in a tree that is built and thrown away (each poll builds one): only an element that
// reaches the page is watched, so a thread with no code or maths never starts any of this.
// Wrap it in a [data-own][data-sig] element: morph() then leaves what was drawn alone until the source changes.
const KINDS = {}, queue = [];
let io = null, busy = false;

export const lazyKind = (kind, draw) => { KINDS[kind] = draw; };

const idle = () => new Promise(r => (self.requestIdleCallback ? requestIdleCallback(r, { timeout: 300 }) : setTimeout(r, 30)));

async function pump() {
  if (busy) return;
  busy = true;
  try {
    while (queue.length) {
      await idle();
      const t = performance.now();
      while (queue.length && performance.now() - t < 8) {
        const el = queue.shift();
        if (!el.isConnected || el._drawn) continue;
        el._drawn = true;
        try { await KINDS[el.dataset.kind]?.(el); } catch (e) { console.warn("rich: couldn't draw", el.dataset.kind, e); }
      }
    }
  } finally { busy = false; }
}

function seen(entries) {
  for (const e of entries) if (e.isIntersecting) { io.unobserve(e.target); queue.push(e.target); }
  if (queue.length) pump();
}

class RcLazy extends HTMLElement {
  connectedCallback() {
    if (this._drawn || this.querySelector("mark")) return;  // search marks live in the source text: leave it as it is
    (io ||= new IntersectionObserver(seen, { rootMargin: "800px", scrollMargin: "800px" })).observe(this);
  }
  disconnectedCallback() { io?.unobserve(this); }
}
if (!customElements.get("rc-lazy")) customElements.define("rc-lazy", RcLazy);
