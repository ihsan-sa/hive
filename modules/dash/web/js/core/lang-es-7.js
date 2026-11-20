// Spanish for the wall display (display.html, js/display/app.js and tiles.js; product-test rung "Spanish on the display page") and for the words
// of the shared views it shows at wall size (mesh, timeline, usage), found by test/drive_display_lang.mjs. Same rules as lang-es.js: exact English
// keys, and patterns for the lines built with a name, a count or a time. Merged last in lang.js, so a word the older tables have keeps their Spanish.
// tr() below reads the whole Spanish table, so a line made of known parts ("acct-5: paused: no new runs") is put together from them.
import ES1 from "./lang-es.js";
import ES2 from "./lang-es-2.js";
import ES3 from "./lang-es-3.js";
import ES4 from "./lang-es-4.js";
import ES5 from "./lang-es-5.js";
import ES6 from "./lang-es-6.js";

const pl = (n, one, many) => (String(n) === "1" ? one : many);
let ALL = null;
const tr = s => {   // one part of a line, in Spanish if any table knows it
  if (!ALL) ALL = { keys: { ...SELF, ...ES6, ...ES5, ...ES4, ...ES3, ...ES2, ...ES1 }, pats: [...ES1.__patterns, ...ES2.__patterns, ...ES3.__patterns, ...ES4.__patterns, ...ES5.__patterns, ...ES6.__patterns, ...SELF.__patterns] };
  if (ALL.keys[s] !== undefined) return ALL.keys[s];
  for (const [re, to] of ALL.pats) if (re.test(s)) return s.replace(re, to);
  return s;
};
const LIMIT = { "Weekly": "Semanal", "5-hour": "5 horas" };
const WHAT = { data: "los datos", timeline: "la cronología", map: "el mapa" };
// the parts of a mesh card's or agent's label, after its name: "Everything else", "8 posts", "2 stuck", "now", "main thread project-20"
const part = p => {
  let m;
  if ((m = p.match(/^(\d+) posts?$/))) return `${m[1]} ${pl(m[1], "mensaje", "mensajes")}`;
  if ((m = p.match(/^(\d+) helpers?$/))) return `${m[1]} ${pl(m[1], "ayudante", "ayudantes")}`;
  if ((m = p.match(/^(\d+) stuck$/))) return `${m[1]} ${pl(m[1], "atascado", "atascados")}`;
  if ((m = p.match(/^main (thread|conversation) (.+)$/))) return `${m[1] === "thread" ? "hilo" : "conversación"} principal ${m[2]}`;
  return tr(p);
};

const SELF = {
  // the display's own words
  "Move the edge between tiles": "Mover el borde entre paneles",
  "Drag to resize, or click to step it (from the far end it starts again); double-click to reset":
    "Arrastra para cambiar el tamaño o haz clic para avanzar un paso (desde el extremo vuelve a empezar); doble clic para restablecerlo",
  "Ledger": "Registro",
  "How it's working": "Cómo trabaja",
  "Can't reach the data.": "No llegan los datos.",
  "It keeps trying.": "Sigue intentándolo.",
  "Starts held": "Inicios en espera",
  "No account has room": "Ninguna cuenta tiene margen",
  "nothing new starts": "no empieza nada nuevo",
  "of its 5-hour limit": "de su límite de 5 horas",
  "account has room": "cuenta tiene margen",
  "accounts has room": "cuentas tiene margen",
  "accounts have room": "cuentas tienen margen",
  "Hive display": "Pantalla de Hive",
  "Hive display · preview": "Pantalla de Hive · vista previa",
  // the shared views at wall size
  "Zoom": "Ampliación",
  "Zoom in (or Ctrl + wheel, or pinch)": "Acercar (o Ctrl + rueda, o pellizca)",
  "Fit the graph to the panel and let dragged nodes go": "Ajustar el grafo al panel y soltar los nodos arrastrados",
  "Map: loading": "Mapa: cargando",
  "This month (UTC)": "Este mes (UTC)",
  "Free models today (UTC)": "Modelos gratuitos hoy (UTC)",
  "5-hour": "5 horas",
  "job running": "tarea en marcha",
  "hasn't run": "no ha corrido",
  "Each row is a thread, each bar an agent's session (filled with a status mark at its end while running; pale and dashed once ended; rows with someone running come first, the hive's own upkeep rows last and one line each), each tick along the top of a row a post. Stacked bars mean agents worked in parallel. A bar's name shows when it fits: hover for any. Click a bar for the agent, a row to read the thread. To zoom: Ctrl+scroll or pinch, or the − and + buttons. Drag sideways to move through time.":
    "Cada fila es un hilo, cada barra la sesión de un agente (rellena y con una marca de estado al final mientras corre; pálida y discontinua cuando ha terminado; primero las filas con alguien en marcha, al final las de mantenimiento del propio hive, una línea cada una), cada marca en lo alto de una fila un mensaje. Las barras apiladas son agentes que trabajaron en paralelo. El nombre de una barra se ve cuando cabe: pasa el puntero por encima para ver cualquiera. Haz clic en una barra para ver el agente y en una fila para leer el hilo. Para ampliar: Ctrl+rueda o pellizca, o los botones − y +. Arrastra de lado para moverte en el tiempo.",
  __patterns: [
    // the tiles' heads and "more" notes (display/tiles.js)
    [/^\+(\d+) more, quietest last$/, "+$1 más, los más tranquilos al final"],
    [/^\+(\d+) more$/, "+$1 más"],
    [/^\+(\d+) not shown$/, "+$1 sin mostrar"],
    [/^\+(\d+) figures? not shown$/, (_, n) => `+${n} ${pl(n, "cifra", "cifras")} sin mostrar`],
    [/^\+(\d+) charts? not shown$/, (_, n) => `+${n} ${pl(n, "gráfico", "gráficos")} sin mostrar`],
    [/^(\d+) events? in the last hour$/, (_, n) => `${n} ${pl(n, "evento", "eventos")} en la última hora`],
    [/^(\d+) open$/, (_, n) => `${n} ${pl(n, "abierto", "abiertos")}`],
    [/^(\S+) of work in 24h$/, "$1 de trabajo en 24 h"],
    [/^Couldn't read the (data|timeline|map)\. It keeps trying\.$/, (_, w) => `No se pudo leer ${WHAT[w]}. Sigue intentándolo.`],
    // the top bar (display/app.js room, lost, trouble and the size note)
    [/^(accounts? (?:has|have) room); (\S+) stops at (\d+)% in (\S+)$/, (_, a, n, p, d) => `${tr(a)}; ${n} se para al ${p}% en ${d}`],
    [/^No new data for (.+)\.$/, "Sin datos nuevos desde hace $1."],
    [/^Everything below is from (.+)\.$/, "Todo lo de abajo es de las $1."],
    [/^until (\d{1,2}:\d{2}(?: ?[AaPp]\.? ?[Mm]\.?)?)$/, "hasta las $1"],
    [/^(\d+) stuck$/, (_, n) => `${n} ${pl(n, "atascado", "atascados")}`],
    [/^(\S+) at (\d+)%$/, "$1 al $2%"],
    [/^Size (\d+)% · Ctrl 0 resets$/, "Tamaño $1% · Ctrl 0 lo restablece"],
    // an agent's state line (core/model.js agentState), alone or after its name in a timeline bar's title
    [/^(?:(\d+) jobs|job) running(?:, (\d+) more queued)?(?:, (\d+) new posts? when it returns)?$/, (_, j, q, n) =>
      (j ? `${j} tareas en marcha` : "tarea en marcha") + (q ? `, ${q} más en cola` : "") + (n ? `, ${n} ${pl(n, "mensaje nuevo", "mensajes nuevos")} al volver` : "")],
    [/^(?:(\d+) jobs|job) in the queue (\S+)(?:, (\d+) new posts? when it returns)?$/, (_, j, d, n) =>
      (j ? `${j} tareas en la cola` : "tarea en la cola") + ` ${d}` + (n ? `, ${n} ${pl(n, "mensaje nuevo", "mensajes nuevos")} al volver` : "")],
    // the timeline's running note (views/timeline.js runningNote)
    [/^(\d+) running · (\d+) more below(?: · (\d+) of (\d+) that ended)?$/, (_, r, b, d, a) => `${r} en marcha · ${b} más abajo` + (d ? ` · ${d} de ${a} que terminaron` : "")],
    // usage at wall size (views/usage.js usageAnswer, the gauges and their meters)
    [/^Only (\S+) can take new runs\. At the last 6 hours' pace, (\S+) reaches its (\d+)% stop in about (\S+), (\S+) before it resets\.$/,
      "Solo $1 puede aceptar ejecuciones nuevas. Al ritmo de las últimas 6 horas, $2 llega a su parada del $3% en unas $4, $5 antes de reiniciarse."],
    [/^(\d+) of (\d+) logins can take new runs\. At the last 6 hours' pace, (\S+) reaches its (\d+)% stop in about (\S+), (\S+) before it resets\.$/,
      "$1 de $2 cuentas pueden aceptar ejecuciones nuevas. Al ritmo de las últimas 6 horas, $3 llega a su parada del $4% en unas $5, $6 antes de reiniciarse."],
    [/^Only (\S+) can take new runs, and none reaches its stop line before it resets at the current pace\.$/,
      "Solo $1 puede aceptar ejecuciones nuevas, y ninguna llega a su línea de parada antes de reiniciarse al ritmo actual."],
    [/^(\d+) of (\d+) logins can take new runs, and none reaches its stop line before it resets at the current pace\.$/,
      "$1 de $2 cuentas pueden aceptar ejecuciones nuevas, y ninguna llega a su línea de parada antes de reiniciarse al ritmo actual."],
    [/^(· )?last (\d+) hours$/, (_, d, n) => `${d || ""}últimas ${n} horas`],
    [/^of \$(\S+)$/, "de $$$1"],
    [/^of (\d+) requests$/, "de $1 solicitudes"],
    [/^(Weekly|5-hour): stop line at (\d+)%$/, (_, l, p) => `${LIMIT[l]}: línea de parada en ${p}%`],
    // the mesh: a card's count line and the labels a screen reader hears (views/mesh.js)
    [/^(\d+) posts? · (.+)$/, (_, n, s) => `${n} ${pl(n, "mensaje", "mensajes")} · ${tr(s)}`],
    [/^(\S+) also here · (\d+) posts?$/, (_, a, n) => `${a} también aquí · ${n} ${pl(n, "mensaje", "mensajes")}`],
    [/^(\S+) also posted in (\S+)$/, "$1 también escribió en $2"],
    [/^Thread (\S+), (.+)$/, (_, t, r) => `Hilo ${t}, ${r.split(", ").map(part).join(", ")}`],
    [/^(\S+), (.+), (main (?:thread|conversation) \S+)$/, (_, a, s, m) => `${a}, ${tr(s)}, ${part(m)}`],
    // a name and its state: a timeline bar ("a1b2: job running"), a usage row ("acct-5: paused: no new runs"), an upkeep row ("watchdog: hasn't run")
    [/^([a-z][\w.@-]{1,30}): (.{1,80})$/, (all, n, s) => { const v = tr(s); return v === s ? all : `${n}: ${v}`; }],
  ],
};
export default SELF;
