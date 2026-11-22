// Spanish for Documents and the document viewer (views/docs.js, ui/docviewer.js: the library's PDFs, notes on a page, revisions),
// added 2026-11-22. Same rules as lang-es.js: exact English keys, __patterns for a line with a number, a page or a revision in it.
// The two kinds of note are a segmented pair: "Ask" is already "Preguntar", so "Comment" is the verb too ("Comentar").
// A note's place: "whole document", "p. 3", "box, p. 3" (a box drawn on page 3). revision = revisión; box = recuadro.
const KIND = { Comment: "Comentar", Ask: "Preguntar" };
const where = w => w === "whole document" ? "todo el documento" : w.replace(/^box, /, "recuadro, ");
const ago = a => a === "just now" ? "ahora mismo" : a.replace(/^(.+) ago$/, "hace $1");
const W = "(whole document|(?:box, )?p\\. \\d+)";
export default {
  // the Documents page
  "Documents": "Documentos",
  "‹ Documents": "‹ Documentos",
  "The library's PDFs: comment on one and an agent revises it": "Los PDF de la biblioteca: comenta uno y un agente lo revisa",
  "The library's PDFs. Open one to comment on it like in the library: select text or draw a box. An agent edits the file and files the new revision, and it shows up here.":
    "Los PDF de la biblioteca. Abre uno para comentarlo como en la biblioteca: selecciona texto o dibuja un recuadro. Un agente edita el archivo y archiva la nueva revisión, y aparece aquí.",
  "Find a document": "Buscar un documento",
  "No document matches.": "Ningún documento coincide.",
  "The library has no PDFs yet.": "La biblioteca aún no tiene PDF.",
  "Couldn't read the library:": "No se pudo leer la biblioteca:",
  "new, being filed": "nueva, archivándose",
  // the viewer: the page, its tools and the list of notes
  "Opening…": "Abriendo…",
  "Couldn't open this document:": "No se pudo abrir este documento:",
  "Document pages": "Páginas del documento",
  "Revision": "Revisión",
  "An older revision.": "Una revisión anterior.",
  "Show the latest": "Mostrar la última",
  "Draw a box": "Dibujar un recuadro",
  "Drawing: drag, or arrows + Enter · esc": "Dibujando: arrastra, o flechas + Intro · esc",
  "or select text on the page": "o selecciona texto en la página",
  "A note on the whole document": "Una nota sobre todo el documento",
  "↵ add to the list": "↵ añadir a la lista",
  "↵ add · ⇧↵ new line · esc": "↵ añadir · ⇧↵ nueva línea · esc",
  "Not sent yet": "Sin enviar",
  "Select text or draw a box, then press Enter: notes wait here until you send them.": "Selecciona texto o dibuja un recuadro y pulsa Intro: las notas esperan aquí hasta que las envíes.",
  "With the agent": "Con el agente",
  "Show it on the page": "Mostrarla en la página",
  "Mark it done yourself": "Márcala como hecha tú mismo",
  "Reopen": "Reabrir",
  "When you send notes, an agent picks them up here, edits the file and files the new revision in the library. It shows up on this page by itself.":
    "Cuando envías notas, un agente las recoge aquí, edita el archivo y archiva la nueva revisión en la biblioteca. Aparece en esta página por sí sola.",
  "Reply to the agent": "Responder al agente",
  // a note's card
  "Comment": "Comentar",
  "Kind of note": "Tipo de nota",
  "What should change": "Qué debería cambiar",
  "Draft, not sent yet": "Borrador, aún sin enviar",
  "Marked done": "Marcada como hecha",
  "Mark done": "Marcar como hecha",
  "whole document": "todo el documento",
  "by hand": "a mano",
  "done by hand": "hecha a mano",
  // pieces the source joins to a page or a revision (the whole line is a pattern below)
  "box,": "recuadro,",
  "· from rev": "· de la rev.",
  "Addressed in revision": "Resuelta en la revisión",
  "This passage changed in this revision.": "Este pasaje cambió en esta revisión.",
  __patterns: [
    [new RegExp(`^${W}( · from rev (.+))?$`), (_, w, f, r) => where(w) + (f ? ` · de la rev. ${r}` : "")],
    [new RegExp(`^(Comment|Ask) · ${W}( · (done by hand|rev (.+)))?$`), (_, k, w, s, d, r) => `${KIND[k]} · ${where(w)}` + (s ? ` · ${d === "done by hand" ? "hecha a mano" : `rev. ${r}`}` : "")],
    [/^Addressed in revision (.+)$/, (_, r) => `Resuelta en la revisión ${r}`],
    [/^Sent (.+), waiting for the agent$/, (_, a) => `Enviada ${ago(a)}, esperando al agente`],
    [/^Couldn't open this document: ([\s\S]*)$/, (_, e) => `No se pudo abrir este documento: ${e}`],
    [/^Couldn't read the library: ([\s\S]*)$/, (_, e) => `No se pudo leer la biblioteca: ${e}`],
    [/^Couldn't save: ([\s\S]*)$/, (_, e) => `No se pudo guardar: ${e}`],
    [/^Couldn't send: ([\s\S]*)$/, (_, e) => `No se pudo enviar: ${e}`],
    [/^Draw a box on the page \((.+)\)$/, (_, k) => `Dibujar un recuadro en la página (${k})`],
    [/^(\S+) \(latest\)$/, (_, r) => `${r} (la última)`],
    [/^Send all \((\d+)\)$/, (_, n) => `Enviar todas (${n})`],
    [/^(Not sent yet|With the agent|Done) \((\d+)\)$/, (_, t, n) => `${{ "Not sent yet": "Sin enviar", "With the agent": "Con el agente", Done: "Hechas" }[t]} (${n})`],
    [/^(\S+) · revision (.+?)(?: · (\d+) not sent)?(?: · (\d+) with the agent)?$/, (_, id, r, d, o) => `${id} · revisión ${r === "new, being filed" ? "nueva, archivándose" : r}`
      + (d ? ` · ${d} sin enviar` : "") + (o ? ` · ${o} con el agente` : "")],
    [/^This passage changed in this revision\. (Comment|Ask): ([\s\S]*)$/, (_, k, t) => `Este pasaje cambió en esta revisión. ${KIND[k]}: ${t}`],
    [/^Note (\d+), (Comment|Ask): ([\s\S]*)$/, (_, n, k, t) => `Nota ${n}, ${KIND[k]}: ${t}`],
  ],
};
