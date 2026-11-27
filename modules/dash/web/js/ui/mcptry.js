// Settings > Connectors > Try your own tools (E205b): a tool an agent made and connected, run by hand. Pick a server, see its tools,
// fill in one tool's arguments, run it, read the answer or the error. dash.py /api/mcp-try runs it, in the sandbox where there is one.
// Nothing runs until the person presses a button: listing a server's tools starts its code too.
import { h, tr } from "../core/dom.js";
import { jget, post } from "../core/store.js";
import { block, button, field, fieldErr, input, invalid, empty } from "./kit.js";
import { confirm } from "./dialog.js";

const T = {};
const BARE = "This Hive runs without the sandbox: a tool you run here can read your files and sign-ins.";
/** Leaving Settings drops what was typed and the answers. */
export function forgetTry() { Object.assign(T, { list: null, may: false, bare: false, agreed: false, why: "", err: "", name: "", tools: null, toolsErr: "", loading: false, tool: "", args: {}, bad: {}, out: {}, busy: "", runId: "", stopping: false, stopErr: "", turn: (T.turn || 0) + 1 }); }
forgetTry();

export async function loadTry(update) {
  const turn = T.turn;
  let got = null, err = "";
  try { got = await jget("/api/mcp-try"); } catch (e) { err = e.message; }
  if (turn !== T.turn) return;
  T.err = err;
  T.list = got && Array.isArray(got.tools) ? got.tools : null;   // a preview or an older dash answers without a list: no block
  T.may = !!(got && got.may); T.bare = !!got && got.sandboxed === false; T.why = (got && got.why) || "";
  if (T.name && !(T.list || []).some(x => x.name === T.name)) Object.assign(T, { name: "", tools: null, toolsErr: "", loading: false, tool: "" });
  update();
}

/** With no sandbox a tool's code runs as this account, and listing its tools starts that code: ask once before the first of either. */
const agreed = go => (T.bare && !T.agreed ? confirm({ title: "Run this on the computer itself?", body: [BARE], ok: "Go on", onOk: () => { T.agreed = true; go(); } }) : go());

async function openServer(name, update) {
  const turn = ++T.turn;
  Object.assign(T, { name, tools: null, toolsErr: "", loading: !!name, tool: "", bad: {}, busy: "" });
  update();
  if (!name) return;
  let r = null, err = "";
  try { r = await post("/api/mcp-try", { do: "tools", name }); } catch (e) { err = e.message; }
  if (turn !== T.turn) return;
  T.loading = false;
  T.tools = r && Array.isArray(r.tools) ? r.tools : null;
  T.toolsErr = T.tools ? "" : err || (r && r.error) || "it didn't say which tools it has";
  if (T.tools && T.tools.length === 1) T.tool = T.tools[0].name;
  update();
}

/** The tool's own words: a text node, so the app's language table never rewrites them (dom.js add()). */
const raw = s => document.createTextNode(String(s));
const obj = v => (v && typeof v === "object" && !Array.isArray(v) ? v : {});
/** The one JSON type to draw a control for: ["string", "null"] counts as a string; "" when the schema names none. */
const typeOf = s => [].concat(s.type || []).find(t => t !== "null") || "";
const choices = s => (Array.isArray(s.enum) && s.enum.length ? s.enum : typeOf(s) === "boolean" ? [true, false] : null);
const schemaOf = t => { const s = obj(t.inputSchema); return { props: Object.entries(obj(s.properties)).map(([k, v]) => [k, obj(v)]), need: Array.isArray(s.required) ? s.required : [] }; };

/** A list of plain words or numbers is typed one item on each line: its items' type, "any" when the schema names none, else "". */
const lineType = s => {
  if (typeOf(s) !== "array") return "";
  const i = obj(s.items), t = typeOf(i);
  if (i.enum || i.properties || i.anyOf || i.oneOf || i.allOf || i.$ref) return "";
  return ["string", "number", "integer"].includes(t) ? t : t ? "" : "any";
};
const numberOf = (text, type) => { const n = text.trim() === "" ? NaN : Number(text); return Number.isFinite(n) && (type !== "integer" || Number.isInteger(n)) ? n : null; };
const readJson = text => { try { return [true, JSON.parse(text)]; } catch (e) { return [false]; } };
/** The lines of a list box -> the list, or null when one isn't the items' type. A list typed as JSON is taken as it is. */
function readLines(text, type) {
  const [ok, v] = text.trim().startsWith("[") ? readJson(text) : [false];
  if (ok && Array.isArray(v) && (type === "any" || v.every(x => (type === "string" ? typeof x === "string" : typeof x === "number" && numberOf(String(x), type) !== null)))) return v;
  const lines = text.split(/\r?\n/).filter(l => l.trim() !== "");
  if (type === "string") return lines;
  const all = lines.map(l => numberOf(l, type));
  if (type === "any") return all.includes(null) ? lines : all;   // all numbers, or all words
  return all.includes(null) ? null : all;
}

/** What the boxes hold -> {args} to send, or {bad: {name: why}}. An empty optional box is left out, so the tool's own default applies. */
export function readArgs(tool, typed) {
  const { props, need } = schemaOf(tool), args = [], bad = {};
  for (const [k, s] of props) {
    const raw = typed.get(k) ?? startOf(s, need.includes(k)), type = typeOf(s), opts = choices(s), each = lineType(s);
    if (raw === "" || (each && raw.trim() === "")) { if (need.includes(k)) bad[k] = "This one is needed."; continue; }
    if (opts) { if (opts[+raw] !== undefined) args.push([k, opts[+raw]]); continue; }
    if (type === "string") { args.push([k, raw]); continue; }
    if (type === "number" || type === "integer") {
      const n = numberOf(raw, type);
      if (n === null) bad[k] = type === "integer" ? "Type a whole number." : "Type a number.";
      else args.push([k, n]);
      continue;
    }
    if (each) {
      const v = readLines(raw, each);
      if (v === null) bad[k] = each === "integer" ? "Type one whole number on each line." : "Type one number on each line.";
      else args.push([k, v]);
      continue;
    }
    const [ok, v] = readJson(raw);
    if (type === "array" && !(ok && Array.isArray(v))) bad[k] = "Type a list in JSON, inside [ ].";
    else if (type === "object" && !(ok && v && typeof v === "object" && !Array.isArray(v))) bad[k] = "Type it in JSON, inside { }.";
    else args.push([k, ok ? v : raw]);   // no type named: JSON if it reads as JSON, else the words themselves
  }
  return Object.keys(bad).length ? { bad } : { args: Object.fromEntries(args) };   // fromEntries: a box named __proto__ stays a plain key
}

/** What a value could look like, from the schema: its own example or default, else one built from its type. */
function sample(s, depth = 0) {
  if (Array.isArray(s.examples) && s.examples.length) return s.examples[0];
  if (s.default !== undefined) return s.default;
  if (Array.isArray(s.enum) && s.enum.length) return s.enum[0];
  const type = typeOf(s);
  if (type === "number" || type === "integer") return 1;
  if (type === "boolean") return true;
  if (type === "array") return depth > 2 ? [] : [sample(obj(s.items), depth + 1), sample(obj(s.items), depth + 1)];
  if (type === "object") {
    const props = Object.entries(obj(s.properties));
    if (depth > 2) return {};
    if (props.length) return Object.fromEntries(props.map(([k, v]) => [k, sample(obj(v), depth + 1)]));
    return Object.keys(obj(s.additionalProperties)).length ? Object.fromEntries([["name", sample(s.additionalProperties, depth + 1)]]) : {};
  }
  return "text";
}
/** The greyed words in an empty box: never made up when the schema says nothing. */
function hintOf(s) {
  const type = typeOf(s), given = (Array.isArray(s.examples) && s.examples.length) || s.default !== undefined;
  const known = given || (type === "array" && typeOf(obj(s.items))) || (type === "object" && (s.properties || Object.keys(obj(s.additionalProperties)).length));
  if (!known) return null;
  const v = sample(s);
  return lineType(s) && Array.isArray(v) ? v.join("\n") : pretty(v);
}
/** A needed object with named parts starts as its empty shape, so only the values are left to type. */
function startOf(s, needed) {
  const props = Object.entries(obj(s.properties));
  if (!needed || typeOf(s) !== "object" || !props.length) return "";
  const blank = { string: "", number: 0, integer: 0, boolean: false, array: [], object: {} };
  return pretty(Object.fromEntries(props.map(([k, v]) => { const p = obj(v); return [k, p.default ?? blank[typeOf(p)] ?? ""]; })));
}
const LONG = /text|body|content|message|prompt|code|script|markdown|notes?\b|lines|paragraph|query|sql/i;
const isLong = (k, s) => s.maxLength > 120 || !!s.contentMediaType || String(s.default ?? "").includes("\n") || LONG.test(k);

const typedFor = key => (T.args[key] ||= new Map());

async function run(tool, update) {
  const name = T.name, key = name + "/" + tool.name;
  const { args, bad } = readArgs(tool, typedFor(key));
  T.bad = bad || {};
  if (bad) { delete T.out[key]; update(); return; }
  const turn = T.turn;
  const go = async () => {
    const id = Array.from(crypto.getRandomValues(new Uint8Array(16)), b => b.toString(16).padStart(2, "0")).join("");
    T.busy = key; T.runId = id; T.stopping = false; T.stopErr = ""; delete T.out[key]; update();
    let r;
    try { r = await post("/api/mcp-try", { do: "call", name, tool: tool.name, args, run: id }); } catch (e) { r = { error: e.message }; }
    if (turn !== T.turn) return;
    T.busy = ""; T.out[key] = r; update();
  };
  // A tool's labels are its own claim, and one that says nothing may change things (the MCP default): only "only reads" runs at once.
  const a = obj(tool.annotations);
  if (a.readOnlyHint === true && a.destructiveHint !== true) return agreed(go);
  const says = a.destructiveHint === true ? "This tool says it can change or delete things. It runs for real." : "This tool doesn't say it only reads. It may change or delete things, and it runs for real.";
  return confirm({ title: `Run ${tool.name}?`, body: [says, T.bare && !T.agreed ? BARE : null], ok: "Run it", kind: "danger", onOk: () => { T.agreed = true; go(); } });
}

/** Stop the run on screen; its own call then answers "stopped". */
async function stopRun(update) {
  const id = T.runId;
  T.stopping = true; T.stopErr = ""; update();
  try { await post("/api/mcp-try", { do: "stop", run: id }); }
  catch (e) { if (T.runId === id) { T.stopping = false; T.stopErr = e.message; update(); } }
}

const pretty = v => (typeof v === "string" ? v : JSON.stringify(v, null, 2));
const PICTURES = ["image/png", "image/jpeg", "image/gif", "image/webp"];
// A file the tool says it saved, under ~/projects or a post's ~/files: a link into the app's preview (preview.js takes the click).
const SAVED = /(?<![\w/.-])(?:~\/|\/home\/[\w.-]+\/)?(?:(projects\/[\w.-]+(?:\/[\w.-]+)*\.(?:md|markdown|txt|csv|json|ya?ml|py|sh|html|pdf|png|jpe?g|gif|webp))|files\/([0-9a-f]{12})\/([A-Za-z0-9_](?:[\w.-]*[A-Za-z0-9_])?))(?![\w/-]|\.\w)/g;
function fileLink(m, words) {
  const [, kb, id, name] = m;
  return kb ? h("a", { class: "ref", "data-kind": "kbfile", "data-pv-kb": kb, "data-pv-key": "kb:" + kb, href: "#knowledge/" + encodeURIComponent(kb), title: "Open " + kb }, words)
    : h("a", { class: "ref", "data-kind": "file", "data-pv-key": id + "/" + name, href: `/files/${id}/${encodeURIComponent(name)}`, title: "Preview " + name }, words);
}
const savedIn = text => [...String(text).matchAll(SAVED)].filter(m => !m[0].split("/").includes(".."));
/** The tool's words with its saved files as links. */
function linked(text) {
  const s = String(text), out = []; let at = 0;
  for (const m of savedIn(s)) { out.push(raw(s.slice(at, m.index)), fileLink(m, raw(m[0]))); at = m.index + m[0].length; }
  out.push(raw(s.slice(at)));
  return out;
}
/** The pictures among them, shown under the words (four at most). One that isn't there stays hidden. */
const pictures = text => [...new Map(savedIn(text).filter(m => /\.(png|jpe?g|gif|webp)$/i.test(m[0])).map(m => [m[0], m])).values()].slice(0, 4).map(m => {
  const [, kb, id, name] = m, src = kb ? "/api/kb/raw?path=" + encodeURIComponent(kb) : `/files/${id}/${encodeURIComponent(name)}`;
  return fileLink(m, h("img", { class: "mcp-try-img", alt: "Picture the tool saved", src, "data-saved": "", "data-keep": "hidden" }));
});
addEventListener("error", e => { const i = e.target; if (i && i.matches && i.matches("img.mcp-try-img[data-saved]")) i.hidden = true; }, true);   // an error doesn't bubble, so it isn't delegated
/** A resource_link part: a file here opens in the preview, a web address in a new tab, anything else is shown as written. */
function resourceLink(p) {
  const uri = String(p.uri || ""), local = uri.replace(/^file:\/\//, ""), m = savedIn(local)[0], words = raw(p.title || p.name || uri);
  if (m && m.index === 0 && m[0] === local) return fileLink(m, words);
  if (/^https?:\/\//i.test(uri)) return [h("a", { href: uri, target: "_blank", rel: "noopener noreferrer" }, words), " ", h("span", { class: "meta" }, raw(uri))];
  return [words, " ", h("span", { class: "meta" }, raw(uri))];
}
// The Hive's own reasons (dash.py mcp_try, mcptool.py tryit) are translated; what a tool's server said is shown as it is.
const OURS = /^(no answer in \d+ s|two tools are|no connected tool|that isn't a tool|the sandbox isn't|the answer is too big|the run ended with|it didn't say which)/;
const said = s => (OURS.test(s) ? String(s) : raw(s));
function answer(r) {
  const took = typeof r.seconds === "number" ? h("span", { class: "meta" }, `${r.seconds} s`) : null;
  const head = (words, tone) => h("p", { class: "mcp-try-state", "data-tone": tone }, h("b", null, words), took ? " " : null, took);
  if (r.stopped) return h("div", { class: "mcp-try-out", role: "status", "data-key": "out" }, head("Stopped", null), h("p", { class: "muted" }, "It may have changed things before it stopped."));
  if (r.error || !r.result) return h("div", { class: "mcp-try-out", role: "alert", "data-key": "out" }, head("It didn't work", "bad"), h("pre", { class: "mcp-try-pre" }, said(r.error || "")));
  const res = obj(r.result), parts = Array.isArray(res.content) ? res.content.map(obj) : [], data = res.structuredContent;
  const part = (p, i) => {
    if (p.type === "text") { const shown = pictures(p.text ?? ""); return h("div", { class: "mcp-try-part", "data-key": "p" + i }, h("pre", { class: "mcp-try-pre" }, linked(p.text ?? "")), shown); }
    if (p.type === "resource_link") return h("p", { class: "mcp-try-link", "data-key": "p" + i }, resourceLink(p));
    if (p.type === "image" && PICTURES.includes(p.mimeType) && typeof p.data === "string") return h("img", { class: "mcp-try-img", "data-key": "p" + i, alt: "Picture the tool sent back", src: `data:${p.mimeType};base64,${p.data}` });
    return h("pre", { class: "mcp-try-pre", "data-key": "p" + i }, raw(pretty(typeof p.data === "string" ? { ...p, data: "…" } : p)));
  };
  return h("div", { class: "mcp-try-out", role: "status", "data-key": "out" },
    res.isError ? head("The tool answered with an error", "bad") : head("The answer", "good"),
    parts.map(part),
    data === undefined ? null : parts.length ? h("details", { class: "mcp-try-data" }, h("summary", null, "The same answer as data"), h("pre", { class: "mcp-try-pre" }, linked(pretty(data)))) : [h("pre", { class: "mcp-try-pre" }, linked(pretty(data))), pictures(pretty(data))],
    !parts.length && data === undefined ? h("p", { class: "muted" }, "It sent back nothing.") : null);
}

function argBox(tool, key, k, s, needed, i) {
  const typed = typedFor(key), type = typeOf(s), opts = choices(s), id = "mcp-try-e" + i, why = T.bad[k];
  const put = (e, n) => { typed.set(k, n.value); };
  const was = typed.get(k) ?? startOf(s, needed), each = lineType(s), hint = hintOf(s);
  const common = { "data-arg": k, "aria-required": needed ? "true" : null, ...invalid(id, why) };
  const control = opts ? h("select", { class: "input", value: was, onchange: put, ...common },
      h("option", { value: "" }, needed ? "Pick one" : "Leave it out"),
      opts.map((v, n) => h("option", { value: String(n), selected: was === String(n) }, v === true ? "Yes (true)" : v === false ? "No (false)" : raw(pretty(v)))))
    : type === "number" || type === "integer" ? input({ value: was, inputmode: type === "integer" ? "numeric" : "decimal", autocomplete: "off", placeholder: hint, oninput: put, ...common })
    : type === "string" && !isLong(k, s) ? input({ value: was, autocomplete: "off", placeholder: hint, oninput: put, ...common })
    : h("textarea", { class: "input", rows: type === "string" ? 2 : 3, value: was, spellcheck: type === "string" || each === "string" ? null : "false", placeholder: hint, oninput: put, ...common });
  // the argument's name and description are the tool's own words
  const label = h("span", null, raw(k));
  return h("div", { class: "mcp-try-arg", "data-key": key + "/" + k },
    field(needed ? [label, " ", h("span", { class: "meta" }, "needed")] : label, control,
      each ? [s.description ? [raw(s.description), " "] : null, h("span", null, "One on each line, or a JSON list.")] : s.description ? raw(s.description) : null),
    fieldErr(id, why));
}

function toolRow(tool, update) {
  const key = T.name + "/" + tool.name, open = T.tool === tool.name, { props, need } = schemaOf(tool), a = obj(tool.annotations);
  const toggle = () => { T.tool = open ? "" : tool.name; T.bad = {}; update(); };
  const b = button(open ? "Close" : "Try it", toggle, { size: "s", kind: "quiet" });
  b.setAttribute("aria-expanded", String(open));
  b.setAttribute("aria-label", tr(open ? "Close" : "Try it") + ": " + tool.name);
  return h("li", { class: "lr mcp-try-tool", "data-key": key },
    h("div", { class: "lr-in" },
      h("div", { class: "mcp-try-head" }, h("b", { class: "mcp-try-name" }, raw(tool.name)),
        h("span", { class: "meta" }, a.destructiveHint === true ? "says it can change or delete" : a.readOnlyHint === true ? "says it only reads" : "doesn't say what it changes"), b),
      tool.description ? h("p", { class: "muted mcp-try-about", "data-open": open ? "" : null }, raw(tool.description)) : null,
      open ? h("form", { class: "mcp-try-form", novalidate: true, onsubmit: e => { e.preventDefault(); if (!T.busy) run(tool, update); } },
        props.length ? props.map(([k, s], i) => argBox(tool, key, k, s, need.includes(k), i)) : h("p", { class: "muted" }, "It takes no arguments."),
        h("div", { class: "frow" }, button("Run it", null, { type: "submit", size: "s", kind: "primary", busy: T.busy === key, disabled: !!T.busy }),
          T.busy === key ? button(T.stopping ? "Stopping…" : "Stop", () => stopRun(update), { size: "s", disabled: T.stopping }) : null),
        T.busy === key ? h("p", { class: "muted", role: "status", "data-key": "wait" }, "Running. It's stopped if it takes too long.") : null,
        T.busy === key && T.stopErr ? h("p", { class: "field-err", role: "alert", "data-key": "stoperr" }, T.stopErr) : null,
        T.out[key] ? answer(T.out[key]) : null) : null));
}

export function tryBlock(update) {
  if (!T.list) return T.err ? block({ title: "Try your own tools", key: "mcp-try" }, empty(T.err, { kind: "error" }), button("Try again", () => { T.err = ""; update(); return loadTry(update); }, { size: "s" })) : null;
  const server = x => {
    const open = T.name === x.name;
    const b = T.may ? button(open ? "Close" : "See its tools", () => (open ? openServer("", update) : agreed(() => openServer(x.name, update))), { size: "s", kind: "quiet" }) : null;
    if (b) { b.setAttribute("aria-expanded", String(open)); b.setAttribute("aria-label", tr(open ? "Close" : "See its tools") + ": " + x.name); }
    return h("li", { class: "lr mcp-try-server", "data-key": x.name },
      h("div", { class: "lr-in" },
        h("div", { class: "mcp-try-head" }, h("b", { class: "mcp-try-name" }, raw(x.name)), x.from ? h("span", { class: "meta mcp-url" }, raw(x.from)) : null, b),
        !open ? null : T.loading ? empty("Starting it to read its tools…", { kind: "loading" })
          : T.toolsErr ? [h("div", { class: "mcp-try-out", role: "alert" }, h("p", { class: "mcp-try-state", "data-tone": "bad" }, h("b", null, "Couldn't read its tools")), h("pre", { class: "mcp-try-pre" }, said(T.toolsErr))),
              button("Try again", () => openServer(x.name, update), { size: "s" })]
          : T.tools && T.tools.length ? h("ul", { class: "list mcp-try-tools" }, T.tools.map(t => toolRow(t, update))) : h("p", { class: "muted" }, "It has no tools.")));
  };
  return block({ title: "Try your own tools", sub: "Tools your agents made and connected. Run one yourself to see what it answers.", key: "mcp-try" },
    T.why ? empty(T.why, { kind: "error" })
      : !T.list.length ? h("p", { class: "muted" }, "None connected yet. Ask an agent to make you a tool, and it shows here.")
      : [T.bare ? h("p", { class: "mcp-try-bare" }, BARE) : null,
         T.may ? null : h("p", { class: "muted" }, "Running a tool from here needs the full sign-in."),
         h("ul", { class: "list mcp-try-list" }, T.list.map(server))]);
}
