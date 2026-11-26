// Spanish for the line that says where a conversation stands (model.js threadStand; comms.js statusBar, titleSub and stMenu) and the More button
// that folds a phone's chat tools. Same rules as lang-es.js: exact English keys, and patterns for the lines built with a name, an age or a count.
// Merged last in lang.js, so a word the older tables already have keeps their Spanish.
// The state words follow the rail and the focus overview: "Needs you" is "Te necesita" and "Waiting on others" is "Esperando a otros" there already.
const WHO = { "The agent": "El agente", "An agent": "Un agente", "The Hive": "El Hive", "The warden": "El guardián", "You": "Tú" };
const LOW = { "the agent": "el agente", "an agent": "un agente", "the Hive": "el Hive", "the warden": "el guardián" };
const who = n => WHO[n] || n.replace(/^Agent ([A-Z]{1,2})$/, "Agente $1");
const OF = { "The agent": "del agente", "An agent": "de un agente", "The Hive": "del Hive", "The warden": "del guardián", "Your Hive": "de tu Hive" };
const of = n => OF[n] || `de ${who(n)}`;   // "de" + who wrote: "del agente", "de Agente B"
const when = t => (t === "just now" ? "ahora mismo" : `hace ${t.replace(/ ago$/, "")}`);   // format.js ago(): "just now", "12s ago", "5m ago", "2.5h ago", "3d ago"
const AGO = "(just now|\\d+(?:[.,]\\d)?[smhd] ago)";
const RETRY = { "no connection to Claude": "sin conexión con Claude", "the machine restarted": "la máquina se reinició", "went silent with nothing running": "se quedó en silencio sin nada en marcha",
  "ended before it finished": "terminó antes de acabar", "reached its budget or turn limit": "llegó a su presupuesto o límite de turnos",
  "the machine ran out of memory and killed it": "la máquina se quedó sin memoria y lo paró", "killed by a signal the starter didn't send": "lo paró una señal que no envió el arrancador",
  "ended mid-turn, most likely killed": "terminó a mitad de turno, seguramente lo pararon" };
const THEN = " y entonces lee tu mensaje";   // the paused run reads the person's held message when it carries on (model.js threadStand)
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
  "Not picked up": "Sin recoger",
  // the one line after the word
  "on it now": "en ello ahora",
  "on it again since the summary": "otra vez en ello desde el resumen",
  "still on it after the summary": "sigue en ello después del resumen",
  "carrying on after your reply": "sigue tras tu respuesta",
  "not picked up yet": "nadie lo ha recogido aún",
  "taking a break; it carries on by itself": "se toma un descanso; sigue por sí solo",
  "taking a break; it carries on by itself and reads your message then": "se toma un descanso; sigue por sí solo y entonces lee tu mensaje",
  // the two halves model.js joins into the lines above and the patterns below: never shown alone, listed so the scan of words with no Spanish
  // (test/lang_left.mjs) doesn't count a half as left in English
  "it carries on by itself": "sigue por sí solo",
  "and reads your message then": "y entonces lee tu mensaje",
  ...RETRY,
  "stopped, carries on by itself": "parado; sigue por sí solo",
  "is taking a break and carries on by itself.": "se toma un descanso y sigue por sí solo.",
  "It carries on by itself.": "Sigue por sí solo.",
  "It reads your message then.": "Entonces lee tu mensaje.",   // a message of the person's is held for the paused run (model.js heldForYou)
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
  // a job's step in plain words (model.js plainStep) and the job line around it
  "Reading files": "Leyendo archivos",
  "Looking through the files": "Buscando en los archivos",
  "Saving the changes": "Guardando los cambios",
  "Syncing the code": "Sincronizando el código",
  "Checking the history": "Revisando el historial",
  "Running the tests": "Pasando las pruebas",
  "Taking screenshots": "Haciendo capturas",
  "Running a program": "Ejecutando un programa",
  "Fetching a page": "Descargando una página",
  "Restarting a service": "Reiniciando un servicio",
  "Checking a service": "Revisando un servicio",
  "Checking what's running": "Mirando qué está en marcha",
  "Moving files around": "Moviendo archivos",
  "Editing files": "Editando archivos",
  "Stopping a process": "Deteniendo un proceso",
  "Removing files": "Borrando archivos",
  "Checking GitHub": "Revisando GitHub",
  "Reading the board": "Leyendo el tablón",
  "Looking things up": "Buscando información",
  "Running a command": "Ejecutando un comando",
  "Running:": "Ejecutando:",
  "no output yet": "aún sin salida",
  // now.js: an agent card whose bar said Done reads "last finished: the clean-up"
  "last finished:": "lo último que terminó:",
  // Agents: the housekeeping button's parts and the Totals line's
  "Also show": "Mostrar también",
  "housekeeping run": "tarea de mantenimiento",
  "at API prices, not billed": "a precios de la API, no se cobra",
  __patterns: [
    // plainStep with a file name: "Reading away.py", "Running build.py"
    [/^Reading ([\w.~-]+\.\w{1,5})$/, "Leyendo $1"],
    [/^Editing ([\w.~-]+)$/, "Editando $1"],
    [/^Running ([\w.~-]+\.\w{1,5})$/, "Ejecutando $1"],
    // a run waiting to start behind its agent's work in another conversation (threadStand, friction-dash-review2)
    [/^(.+) finishes what it's on first$/, (_, n) => `${who(n)} termina primero lo que está haciendo`],
    // the second fact: the summary's own ask, or the list's entry that is out of the count; the words after the label are the agent's and stay
    [/^(Asked|Asked earlier|Earlier|Later|Not needed): (.+)$/s, (_, l, x) => `${LABEL[l]}: ${x}`],
    // the bar's age and author
    [new RegExp(`^updated ${AGO} · (\\d+) messages? since$`), (_, t, n) => `actualizado ${when(t)} · ${n} ${n === "1" ? "mensaje" : "mensajes"} desde entonces`],
    [/^Summary by (.+)$/, "Resumen de $1"],
    // what an agent did, by name
    [new RegExp(`^(.+) wrote back ${AGO}$`), (_, n, t) => `${who(n)} respondió ${when(t)}`],
    // a conversation with nothing written about it: who wrote last and when, and the run that read it and wrote nothing (threadStand case 7)
    // "Último mensaje de …" and not "… escribió el último mensaje": at 390 the longer one ran past a phone's home row and its age was cut off (HA-19)
    [new RegExp(`^(.+) wrote last, ${AGO}(?:; (.+) picked it up and (wrote nothing back|has written nothing back yet))?$`), (_, n, t, a, y) => `Último mensaje ${n === "You" ? "tuyo" : of(n)}, ${when(t)}${a ? `; ${LOW[a] || who(a)} lo recogió y ${y[0] === "w" ? "no respondió nada" : "aún no ha respondido nada"}` : ""}`],
    // a finished chat's head: an agent has the last word (threadStand case 7, E203n)
    [/^(just now|\d+(?:[.,]\d)?[smhd] ago), from (.+)$/, (_, t, n) => { const N = n[0].toUpperCase() + n.slice(1); return `${when(t)}, ${OF[N] || of(n)}`; }],
    [/^(.+) picked it up and has written nothing back yet$/, (_, n) => `${who(n)} lo recogió y aún no ha respondido nada`],
    [/^(.+) waits for your answer$/, (_, n) => `${who(n)} espera tu respuesta`],
    [/^(.+) stopped answering$/, (_, n) => `${who(n)} dejó de responder`],
    // a run that ended badly after the summary was written
    // with when it carries on, the run's own reset or the Hive's (model.js carryOn, HA-15)
    [new RegExp(`^stopped at a usage limit ${AGO}; it carries on by itself(?: after (.+?))?( and reads your message then)?$`),
      (_, t, at, m) => `se paró por un límite de uso ${when(t)}; sigue por sí solo${at ? ` después del ${at}` : ""}${m ? THEN : ""}`],
    [/^taking a break; it carries on by itself after (.+?)( and reads your message then)?$/, (_, at, m) => `se toma un descanso; sigue por sí solo después del ${at}${m ? THEN : ""}`],
    // a run the starter resumes by itself (lost connection, a restart): starter.py's WHY_WORDS, model.js retryWords
    [new RegExp(`^stopped \\((.+)\\) ${AGO}; it carries on by itself( and reads your message then)?$`), (_, w, t, m) => `se paró (${RETRY[w] || w}) ${when(t)}; sigue por sí solo${m ? THEN : ""}`],
    [new RegExp(`^stopped \\((.+)\\) ${AGO} and carries on by itself\\.$`), (_, w, t) => `se paró (${RETRY[w] || w}) ${when(t)} y sigue por sí solo.`],
    [/^(.+) stopped \((.+)\)$/, (_, n, w) => `${who(n)} se paró (${RETRY[w] || w})`],
    [new RegExp(`^stopped with an error ${AGO}; a message wakes it to try again$`), (_, t) => `se paró con un error ${when(t)}; un mensaje lo despierta para intentarlo de nuevo`],
  ],
};
