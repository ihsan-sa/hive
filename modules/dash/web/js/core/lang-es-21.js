// es: the skills words in Settings (settings.js modules and connectors), a reply's tool and skill lines (message.js),
// and the stop words (actions.js, model.js). Exact English keys.
// E203n: the Stop and send receipt (ui/message.js); rcA names the agent inside it, rcC the same at the start of a line
const rcA = a => /^the agent$/i.test(a) ? "el agente" : a.replace(/\bAgent ([A-Z]{1,2})\b/g, "Agente $1").replace(/ and /g, " y ");
const rcC = a => { const w = rcA(a); return w.charAt(0).toUpperCase() + w.slice(1); };
const to = w => w.startsWith("el ") ? "al " + w.slice(3) : "a " + w;
const SELF = {
  "A skill your agents can use.": "Una habilidad que tus agentes pueden usar.",
  "Adding…": "Añadiendo…",
  "Agents can use it from their next run.": "Los agentes pueden usarlo desde su próxima ejecución.",
  "Brings the skill": "Trae la habilidad",
  "Brings the skills": "Trae las habilidades",
  "Code it ran": "Código que ejecutó",
  "Comes with the": "Viene con el",
  "From the": "Del",
  "Its skill": "Su habilidad",
  "Its skills": "Sus habilidades",
  "Skill": "Habilidad",
  "Skills": "Habilidades",
  "Steps it took": "Pasos que siguió",
  "This stops it right away, without letting it save what it was in the middle of. It won't carry on by itself: send a message in the conversation when you want it to.": "Esto lo detiene de inmediato, sin dejarle guardar lo que estaba haciendo. No seguirá por sí solo: envía un mensaje en la conversación cuando quieras que siga.",
  "Turn on": "Activar",
  "You stopped it. Send a message to carry on.": "Lo detuviste. Envía un mensaje para que siga.",
  "tools and skills your agents can use": "herramientas y habilidades que tus agentes pueden usar",
  "Move to a focus": "Mover a un enfoque",
  "Its conversations, page and summary stay as they are. Agents still know it as": "Sus conversaciones, su página y su resumen se quedan como están. Los agentes todavía lo conocen como",
  "Couldn't bring it back:": "No se pudo recuperar:",
  // E203h: making and uploading a skill (settings.js)
  "1. Ask where, when and the budget.\n2. …": "1. Pregunta adónde, cuándo y el presupuesto.\n2. …",
  "Add skill": "Añadir habilidad",
  "Added by you": "Añadida por ti",
  "Adding the skill…": "Añadiendo la habilidad…",
  "Agents read this to decide when to use it.": "Los agentes leen esto para decidir cuándo usarla.",
  "New skill": "Nueva habilidad",
  "No skills yet.": "Todavía no hay habilidades.",
  "Plan a trip: dates, budget and who's going": "Planear un viaje: fechas, presupuesto y quién va",
  "Short and lowercase, with hyphens between words, like trip-planner.": "Corto y en minúsculas, con guiones entre palabras, como trip-planner.",
  "What it's for": "Para qué sirve",
  "What the agent should do, in order.": "Lo que el agente debe hacer, en orden.",
  "You added it on this Hive, so this deletes its file too. Agents stop using it from their next run.": "La añadiste en este Hive, así que esto también borra su archivo. Los agentes dejan de usarla desde su próxima ejecución.",
  "your skills are under Skills": "tus habilidades están en Habilidades",
  "Stop and send": "Parar y enviar",
  "Send waits for the current step to end. Stop and send tries to cut that step short.": "Enviar espera a que termine el paso actual. Parar y enviar intenta cortar ese paso.",
  __patterns: [
    [/^Interrupt sent · asking (.+) to stop its step$/, (_, a) => `Interrupción enviada · pidiendo ${to(rcA(a))} que pare su paso`],
    [/^Sent · asking (.+) to stop its step$/, (_, a) => `Enviado · pidiendo ${to(rcA(a))} que pare su paso`],
    [/^(.+?) stopped its step · it reads this now$/, (_, a) => `${rcC(a)} paró su paso · lo lee ahora`],
    [/^Sent · (.+) reads it when its current step ends$/, (_, a) => `Enviado · ${rcA(a)} lo lee cuando termine su paso actual`],
    [/^Read · (.+?) stopped its step at (.+)$/, (_, a, t) => `Leído · ${rcA(a)} paró su paso a las ${t}`],
    [/^Read · (.+?) stopped its step$/, (_, a) => `Leído · ${rcA(a)} paró su paso`],
    [/^Stop .+ now and send this, without waiting for its step to end$/, () => "Pararlo ya y enviar esto, sin esperar a que termine su paso"],
  ],
};
export default SELF;
