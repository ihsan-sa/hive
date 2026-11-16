// Spanish for the line that says where a conversation stands (model.js threadStand; comms.js statusBar, titleSub and stMenu) and the More button
// that folds a phone's chat tools. Same rules as lang-es.js: exact English keys, and patterns for the lines built with a name, an age or a count.
// Merged last in lang.js, so a word the older tables already have keeps their Spanish.
// The state words follow the rail and the focus overview: "Needs you" is "Te necesita" and "Waiting on others" is "Esperando a otros" there already.
const WHO = { "The agent": "El agente", "An agent": "Un agente", "The Hive": "El Hive", "The warden": "El guardián", "You": "Tú" };
const who = n => WHO[n] || n.replace(/^Agent ([A-Z]{1,2})$/, "Agente $1");
const when = t => (t === "just now" ? "ahora mismo" : `hace ${t.replace(/ ago$/, "")}`);   // format.js ago(): "just now", "12s ago", "5m ago", "2.5h ago", "3d ago"
const AGO = "(just now|\\d+(?:[.,]\\d)?[smhd] ago)";
const LABEL = { "Asked": "Pedido", "Asked earlier": "Pedido antes", "Earlier": "Antes", "Later": "Después", "Not needed": "No hace falta" };
export default {
  // the state words the older tables don't have
  "Working": "Trabajando",
  "Working quietly": "Trabajando en silencio",
  "Waiting its turn": "Esperando su turno",
  "Asked earlier": "Pedido antes",
  "Stuck": "Atascado",
  "You replied": "Respondiste",
  "Quiet": "Tranquila",
  // the one line after the word
  "on it now": "en ello ahora",
  "on it again since the summary": "otra vez en ello desde el resumen",
  "still on it after the summary": "sigue en ello después del resumen",
  "about to carry on from the summary": "a punto de seguir desde el resumen",
  "carrying on after your reply": "sigue tras tu respuesta",
  "not picked up yet": "nadie lo ha recogido aún",
  "taking a break; it carries on by itself": "se toma un descanso; sigue por sí solo",
  "ran into a problem; a message makes it try again": "tuvo un problema; un mensaje hace que lo intente de nuevo",
  // why the count doesn't have a conversation that is on the list
  "You put this off": "Lo dejaste para después",
  "In Stuff to address": "En Cosas por atender",
  "You said it isn't needed": "Dijiste que no hace falta",
  // the bar's and the head's controls
  "Show all of it": "Mostrarlo todo",
  "Hide the summary": "Ocultar el resumen",
  "Reply about this summary": "Responder sobre este resumen",
  "More tools": "Más herramientas",
  // the tour's step about the line (tour.js): one sentence where the bar shows, one on a phone, then what both end with
  "The line under a conversation's title says where it stands: working, waiting, done, or needing you. Click it for the whole summary.":
    "La línea bajo el título de una conversación dice cómo va: trabajando, esperando, hecha o si te necesita. Haz clic en ella para ver el resumen completo.",
  "The line under a conversation's title says where it stands: working, waiting, done, or needing you. Tap the title for the whole summary.":
    "La línea bajo el título de una conversación dice cómo va: trabajando, esperando, hecha o si te necesita. Toca el título para ver el resumen completo.",
  "The agent on it keeps it current. Home, a focus, this line, then the conversation itself: each is one click deeper.":
    "El agente que la lleva la mantiene al día. Inicio, un enfoque, esta línea y luego la conversación: cada uno está un clic más adentro.",
  __patterns: [
    // the second fact: the summary's own ask, or the list's entry that is out of the count; the words after the label are the agent's and stay
    [/^(Asked|Asked earlier|Earlier|Later|Not needed): (.+)$/s, (_, l, x) => `${LABEL[l]}: ${x}`],
    // the bar's age and author
    [new RegExp(`^updated ${AGO} · (\\d+) messages? since$`), (_, t, n) => `actualizado ${when(t)} · ${n} ${n === "1" ? "mensaje" : "mensajes"} desde entonces`],
    [/^Summary by (.+)$/, "Resumen de $1"],
    // what an agent did, by name
    [new RegExp(`^(.+) wrote back ${AGO}$`), (_, n, t) => `${who(n)} respondió ${when(t)}`],
    [/^(.+) picked it up and has written nothing back yet$/, (_, n) => `${who(n)} lo recogió y aún no ha respondido nada`],
    [/^(.+) waits for your answer$/, (_, n) => `${who(n)} espera tu respuesta`],
    [/^(.+) stopped answering$/, (_, n) => `${who(n)} dejó de responder`],
    // a run that ended badly after the summary was written
    [new RegExp(`^stopped at a usage limit ${AGO}; it carries on by itself$`), (_, t) => `se paró por un límite de uso ${when(t)}; sigue por sí solo`],
    [new RegExp(`^stopped with an error ${AGO}; a message wakes it to try again$`), (_, t) => `se paró con un error ${when(t)}; un mensaje lo despierta para intentarlo de nuevo`],
  ],
};
