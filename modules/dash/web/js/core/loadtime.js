// R69: the person's own browser reports how long the first load took, so the figure through the public name is measured, not guessed.
// One report per page load, only on a real page (not fixtures/preview), after the chat's prompt box is drawn. The server keeps the last 200 in its loadtimes file.
import { post } from "./store.js";

export function reportLoad() {
  try {
    if (!/\/index\.html$|\/$/.test(location.pathname) || sessionStorage.getItem("loadtime.sent")) return;
    const nav = performance.getEntriesByType("navigation")[0]; if (!nav) return;
    const t0 = performance.now(), res = performance.getEntriesByType("resource");
    const send = () => {
      sessionStorage.setItem("loadtime.sent", "1");
      const chat = document.querySelector(".cm-home .cp-box, .cm-conv .cp-box") ? performance.now() : null;
      const body = { ttfb: nav.responseStart, dom: nav.domContentLoadedEventEnd, load: nav.loadEventEnd || performance.now(), boot: t0, chat: chat ?? performance.now(), files: res.length, bytes: res.reduce((a, r) => a + (r.transferSize || 0), 0), width: innerWidth, cold: res.length > 20 && res.every(r => r.transferSize > 0) };
      post("/api/loadtime", body).catch(() => {});
    };
    let n = 0; const tick = () => { if (document.querySelector(".cp-box") || ++n > 100) send(); else setTimeout(tick, 100); };
    tick();
  } catch (e) {}
}
