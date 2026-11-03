// The address bar holds the view: #now, #jobs, #agent/<name>, #comms/<thread>. Back works; pages can be linked.
export function parse(hash = location.hash) {
  const [v, ...rest] = hash.slice(1).split("/");
  return { id: v || "now", arg: rest.length ? decodeURIComponent(rest.join("/")) : null };
}
export const href = (id, arg) => "#" + id + (arg != null ? "/" + encodeURIComponent(arg) : "");
export function go(id, arg) { const h = href(id, arg); if (location.hash === h) dispatchEvent(new HashChangeEvent("hashchange")); else location.hash = h; }

// Where things open. Views and components call these, never location directly.
export const agentHref = n => href("agent", n);
export const threadHref = th => href("comms", th);
