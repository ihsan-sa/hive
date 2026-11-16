// Spanish for Settings > Wipe the knowledge base (js/ui/kbwipe.js) and the reasons the server gives for a refusal (dash.py kb_wipe_post),
// which the window shows as they come. Same rules as lang-es.js: exact English keys, and patterns for the lines with a name or count in them.
// The server's own reasons for one note that couldn't go back, after the note's path. They come alone ("What couldn't be moved") and
// inside "no note could go back (...)", so both read them from here.
const WHY = [
  [/^(.+): its folder in the knowledge base is read-only$/, "$1: su carpeta en la base de conocimiento es de solo lectura"],
  [/^(.+): the knowledge base folder is read-only$/, "$1: la carpeta de la base de conocimiento es de solo lectura"],
  [/^(.+): it is more than (\d+) folders deep$/, "$1: está a más de $2 carpetas de profundidad"],
  // a note that went back while its name in the copy couldn't be removed
  [/^(.+): it is back, but its name in the copy couldn't be removed \((.+)\)$/, "$1: ya volvió, pero no se pudo quitar su nombre de la copia ($2)"],
  // a note that can go back neither by a link nor by a rename that refuses to replace (dash.py kb_rename_new)
  [/^(.+): this system can't move a file without the risk of replacing one$/, "$1: este sistema no puede mover un archivo sin riesgo de reemplazar otro"],
];
const why = s => { for (const [re, to] of WHY) if (re.test(s)) return s.replace(re, to); return s; };
const SELF = {
  "Wipe the knowledge base": "Borrar la base de conocimiento",
  "Wipe the knowledge base…": "Borrar la base de conocimiento…",
  "Moves every note in the knowledge base into a private copy on this machine, so agents start again without what they learned. Your priorities and standing directions stay. Nothing is deleted, and a copy can be put back.":
    "Mueve todas las notas de la base de conocimiento a una copia privada en esta máquina, así los agentes empiezan de nuevo sin lo que aprendieron. Tus prioridades e indicaciones permanentes se quedan. No se borra nada y una copia se puede devolver.",
  "Copies from earlier wipes": "Copias de borrados anteriores",
  "Put back…": "Devolver…",
  "Put a copy back": "Devolver una copia",
  "Putting the notes back…": "Devolviendo las notas…",
  "Nothing was put back": "No se devolvió nada",
  "Each note goes back where it was. A note of the same name that is in the knowledge base now stays as it is, and the one from the copy stays in the copy.":
    "Cada nota vuelve a donde estaba. Una nota con el mismo nombre que ya está en la base de conocimiento se queda como está, y la de la copia se queda en la copia.",
  "stayed in the copy because a note of the same name is there now": "se quedaron en la copia porque ya hay una nota con el mismo nombre",
  "returned": "devueltos",
  "copy": "copia",
  "A wipe from outside needs a sign-in from the last 15 minutes. Sign in again, then open this again.":
    "Un borrado desde fuera necesita un inicio de sesión de los últimos 15 minutos. Vuelve a iniciar sesión y abre esto otra vez.",
  "Nothing can be wiped now:": "Ahora no se puede borrar nada:",
  "This is the Hive I mean to wipe.": "Esta es la Hive que quiero borrar.",
  "Agents lose what they learned here until I put the copy back.": "Los agentes pierden lo que aprendieron aquí hasta que devuelva la copia.",
  "The copy stays on this machine and still takes up its space.": "La copia se queda en esta máquina y sigue ocupando su espacio.",
  "Step 1 of 3: what moves": "Paso 1 de 3: qué se mueve",
  "Step 2 of 3: tick each one": "Paso 2 de 3: marca cada una",
  "Step 3 of 3: type the words": "Paso 3 de 3: escribe las palabras",
  "Moves": "Se mueve",
  "Stays": "Se queda",
  "Only the knowledge base moves. The board, the projects, the core and the modules with their own notes stay as they are.": "Solo se mueve la base de conocimiento. El tablero, los proyectos, el núcleo y los módulos con sus propias notas se quedan como están.",
  "Running now": "En marcha ahora",
  "no agents": "ningún agente",
  "top-level entry": "elemento de primer nivel",
  "top-level entries": "elementos de primer nivel",
  "nothing: neither kept file is there": "nada: no está ninguno de los dos archivos que se quedan",
  "Agents running now may be reading or writing notes while they move. A note one of them writes after the wipe stays in the knowledge base.":
    "Los agentes en marcha pueden estar leyendo o escribiendo notas mientras se mueven. Una nota que uno de ellos escriba después del borrado se queda en la base de conocimiento.",
  "Everything moves into a private copy on this machine, which Settings can put back. Search stops returning the moved notes.":
    "Todo se mueve a una copia privada en esta máquina, que Ajustes puede devolver. La búsqueda deja de mostrar las notas movidas.",
  "A wipe or a put back is running now.": "Ahora hay un borrado o una devolución en marcha.",
  "Tick each one": "Marca cada una",
  "To wipe, type": "Para borrar, escribe",
  "exactly.": "tal cual.",
  "The words": "Las palabras",
  "Type the words shown to confirm": "Escribe las palabras que se muestran para confirmar",
  "Wipe now": "Borrar ahora",
  "Moving the notes…": "Moviendo las notas…",
  "Done. The notes moved into a private copy, which Settings can put back.": "Hecho. Las notas se movieron a una copia privada, que Ajustes puede devolver.",
  "It stopped part way, so some notes are still in place.": "Se detuvo a medias, así que algunas notas siguen en su sitio.",
  "Where it stopped": "Dónde se detuvo",
  "Nothing was moved": "No se movió nada",
  "Start again": "Empezar de nuevo",
  "this sign-in is too old, so sign in again first": "este inicio de sesión es demasiado antiguo, así que vuelve a iniciar sesión primero",
  // the server's reasons (dash.py kb_wipe_may, kb_wipe_post, kb_wipe_entries, kb_wipe, kb_put_back, KB_SEARCH_LATE)
  "this needs a full sign-in with the files and settings rights": "esto necesita un inicio de sesión completo con los permisos de archivos y ajustes",
  "this isn't offered on a site's own name": "esto no se ofrece en el nombre propio de un sitio",
  "this connection belongs to another user on the machine": "esta conexión pertenece a otro usuario de la máquina",
  "this sign-in is more than 15 minutes old, so sign in again first": "este inicio de sesión tiene más de 15 minutos, así que vuelve a iniciar sesión primero",
  "the window was open for less than 15 seconds or more than 10 minutes, or was used already, so open it again":
    "la ventana estuvo abierta menos de 15 segundos o más de 10 minutos, o ya se usó, así que ábrela otra vez",
  "the typed words don't match": "las palabras escritas no coinciden",
  "every box must be ticked": "hay que marcar todas las casillas",
  "a wipe or a put back is already running": "ya hay un borrado o una devolución en marcha",
  "the knowledge base folder is a link to somewhere else, so it isn't wiped from here": "la carpeta de la base de conocimiento es un enlace a otro sitio, así que no se borra desde aquí",
  "there is no knowledge base folder": "no hay carpeta de base de conocimiento",
  "the folder for copies is a link to somewhere else": "la carpeta de las copias es un enlace a otro sitio",
  "there is nothing in the knowledge base but the files that stay": "en la base de conocimiento solo están los archivos que se quedan",
  "the knowledge base is on another disk from the folder for copies, so it can't be moved in one step": "la base de conocimiento está en otro disco que la carpeta de las copias, así que no se puede mover de una vez",
  "couldn't make a folder for the copy": "no se pudo crear una carpeta para la copia",
  "no such copy": "no existe esa copia",
  "a folder here is a link to somewhere else": "una carpeta de aquí es un enlace a otro sitio",
  "couldn't write the copy's record": "no se pudo escribir el registro de la copia",
  "the knowledge base is on another disk from the copy, so it can't be put back from here": "la base de conocimiento está en otro disco que la copia, así que no se puede devolver desde aquí",
  "there is nothing left in this copy": "no queda nada en esta copia",
  "Search hasn't caught up yet: for a while it may answer as if this hadn't happened.": "La búsqueda todavía no se ha puesto al día: durante un rato puede responder como si esto no hubiera pasado.",
  "the notes were gone before they could be moved": "las notas ya no estaban cuando se iban a mover",
  "the dashboard stopped while it was moving the notes": "el panel se detuvo mientras movía las notas",
  // when the answer to a wipe or a put back is lost, or a put back stops part way (kbwipe.js)
  "Its folder": "Su carpeta",
  "left from a put back": "resto de una devolución",
  "from a wipe that stopped part way": "de un borrado que se detuvo a medias",
  "from a wipe that hasn't finished": "de un borrado que no ha terminado",
  "Checking what happened…": "Comprobando qué pasó…",
  "Reload the page": "Recargar la página",
  "What this page got instead of an answer": "Lo que esta página recibió en lugar de una respuesta",
  "the answer wasn't one this page can read": "la respuesta no era una que esta página pueda leer",
  "The answer didn't arrive, so this page can't tell whether the notes moved. Reload the page and look at the copies in this section before you try again.":
    "La respuesta no llegó, así que esta página no puede saber si las notas se movieron. Recarga la página y mira las copias de esta sección antes de intentarlo otra vez.",
  "The answer didn't arrive, but the notes moved: there is a new copy, which Settings can put back.": "La respuesta no llegó, pero las notas se movieron: hay una copia nueva, que Ajustes puede devolver.",
  "The answer didn't arrive, and the wipe stopped part way: some notes moved into a new copy, and the rest are still in place.":
    "La respuesta no llegó y el borrado se detuvo a medias: algunas notas se movieron a una copia nueva y el resto sigue en su sitio.",
  "The answer didn't arrive, but notes moved into a new copy. The copy doesn't say whether the wipe finished, so some notes may still be in place.":
    "La respuesta no llegó, pero se movieron notas a una copia nueva. La copia no dice si el borrado terminó, así que puede que algunas notas sigan en su sitio.",
  "The answer didn't arrive, and there is no new copy, so nothing seems to have moved.": "La respuesta no llegó y no hay ninguna copia nueva, así que parece que no se movió nada.",
  "The answer didn't arrive, so this page can't tell whether the notes went back. Reload the page and look at the copies in this section before you try again.":
    "La respuesta no llegó, así que esta página no puede saber si las notas volvieron. Recarga la página y mira las copias de esta sección antes de intentarlo otra vez.",
  "The answer didn't arrive, but the notes went back: nothing is left in this copy.": "La respuesta no llegó, pero las notas volvieron: no queda nada en esta copia.",
  "The answer didn't arrive, but some notes went back. Still in the copy": "La respuesta no llegó, pero algunas notas volvieron. Siguen en la copia",
  "The answer didn't arrive, and the copy is as it was, so nothing seems to have gone back.": "La respuesta no llegó y la copia está como estaba, así que parece que no volvió nada.",
  "Not everything could be moved, and the rest is still in the copy": "No se pudo mover todo, y el resto sigue en la copia",
  "What couldn't be moved": "Lo que no se pudo mover",
  "Others that couldn't be moved": "Otros que no se pudieron mover",
  "The last put back of this copy couldn't move everything, and what it couldn't move is still here.": "La última devolución de esta copia no pudo moverlo todo, y lo que no pudo mover sigue aquí.",
  "This copy was put back before. What is in it now stayed here because notes of the same names were in the knowledge base.":
    "Esta copia ya se devolvió antes. Lo que tiene ahora se quedó aquí porque había notas con los mismos nombres en la base de conocimiento.",
  "The line on the board that tells agents about this couldn't be written.": "No se pudo escribir la línea del tablero que avisa de esto a los agentes.",
  __patterns: [
    // the server's reasons that end in the system's own words for what went wrong (a file name and the error, left as they come)
    [/^couldn't take the lock for this \((.+)\)$/, "no se pudo tomar el bloqueo para esto ($1)"],
    [/^couldn't get the copy ready: (.+)$/, "no se pudo preparar la copia: $1"],
    [/^the first note couldn't be moved \((.+)\)$/, "no se pudo mover la primera nota ($1)"],
    [/^couldn't read the copy or the knowledge base folder: (.+)$/, "no se pudo leer la copia o la carpeta de la base de conocimiento: $1"],
    [/^no note could go back \((.+), and (\d+) more\)$/, (_, a, n) => `ninguna nota pudo volver (${why(a)}, y ${n} más)`],
    [/^no note could go back \((.+)\)$/, (_, a) => `ninguna nota pudo volver (${why(a)})`],
    ...WHY,
  ],
};
export default SELF;
