// es: Settings › Projects, export a project and bring one in (settings.js projectsBlock, projJob); product-test rung, 2026-11-27.
// Exact English keys; the patterns are the status lines with a project or file name in them.
const W = {
  "it stopped without saying why": "se detuvo sin decir por qué",
  "your Hive restarted while this ran; start it again": "tu Hive se reinició mientras esto se ejecutaba; vuelve a empezarlo",
  "this sign-in can't follow it any more (full access ended after a while idle); it goes on running on your Hive, so sign in again and look in Projects": "esta sesión ya no puede seguirlo (el acceso completo terminó tras un rato sin actividad); sigue ejecutándose en tu Hive, así que inicia sesión otra vez y mira en Proyectos",
  "your Hive stopped answering; it may still be running, so look in Projects in a while": "tu Hive dejó de responder; puede que siga ejecutándose, así que mira en Proyectos dentro de un rato",
  "Move a project to another Hive, with its history. Export saves one as a zip on this device; Bring in adds one from such a zip.": "Mueve un proyecto a otro Hive, con su historial. Exportar guarda uno como zip en este dispositivo; Traer añade uno desde un zip así.",
  "Project to export": "Proyecto para exportar",
  "No projects yet": "Aún no hay proyectos",
  "Couldn't read the projects": "No se pudieron leer los proyectos",
  "Export": "Exportar",
  "Export the project as a zip": "Exportar el proyecto como zip",
  "its name here (optional)": "su nombre aquí (opcional)",
  "Name for the project brought in": "Nombre del proyecto que se trae",
  "Bring in a project…": "Traer un proyecto…",
  "Bring in a project from a zip": "Traer un proyecto desde un zip",
  "What it did": "Lo que hizo",
  "Bring in a project": "Traer un proyecto",
  "Exporting": "Exportando",
  "Bringing in": "Trayendo",
};
const er = e => W[e] || e;   // the job's own error words, when they are these
export default {
  ...W,
  __patterns: [
    [/^Exporting (.+?)(…?)$/, (_, n, d) => `Exportando ${n}` + d],
    [/^Bringing in (.+?)(…?)$/, (_, n, d) => `Trayendo ${n}` + d],
    [/^(.+) is downloading\. Bring it in on the other Hive with "Bring in a project", here in its Settings\.$/, (_, f) => `${f} se está descargando. Tráelo en el otro Hive con «Traer un proyecto», en sus Ajustes.`],
    [/^(.+) wasn't exported: (.+)$/, (_, n, x) => { const e = er(x); return `${n} no se exportó: ${e}`; }],
    [/^Brought in as (.+): it's in Projects now\.$/, (_, n) => `Traído como ${n}: ya está en Proyectos.`],
    [/^(.+) wasn't brought in: (.+)$/, (_, n, x) => { const e = er(x); return `${n} no se trajo: ${e}`; }],
  ],
};
