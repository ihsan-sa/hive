// it: Settings > Connectors > Add a connector by address (views/settings.js mcpUrlBlock, E120a) and the server's reasons for a refusal
// (dash.py mcp_url_check / mcp_url_do), shown as they come. Exact English keys; patterns for the lines with a name or a count.
const SELF = {
  "Add a connector by address": "Aggiungi un connettore dal suo indirizzo",
  "A remote MCP server, like a custom connector in Claude. Your agents can use its tools from their next run, so only add a server you trust.": "Un server MCP remoto, come un connettore personalizzato in Claude. I tuoi agenti possono usarne gli strumenti dalla loro prossima esecuzione, quindi aggiungi solo un server di cui ti fidi.",
  "Added. Agents can use it from their next run.": "Aggiunto. Gli agenti possono usarlo dalla loro prossima esecuzione.",
  "Address": "Indirizzo",
  "Agents stop seeing its tools from their next run.": "Gli agenti smettono di vederne gli strumenti dalla loro prossima esecuzione.",
  "has a token": "ha un token",
  "Token (if the server needs one)": "Token (se il server ne chiede uno)",
  "paste the server's address": "incolla l'indirizzo del server",
  "that address has spaces in it or is too long": "quell'indirizzo contiene spazi o è troppo lungo",
  "that isn't a web address": "non è un indirizzo web",
  "leave the sign-in out of the address; paste a token in its own box": "lascia l'accesso fuori dall'indirizzo; incolla il token nella sua casella",
  "the address must start with https:// (plain http only for a server on this computer)": "l'indirizzo deve iniziare con https:// (http semplice solo per un server su questo computer)",
  "no connector added by address has that name": "nessun connettore aggiunto per indirizzo ha quel nome",
  "give it a short name: letters, digits, - and _ (up to 40)": "dagli un nome breve: lettere, cifre, - e _ (fino a 40)",
  "a connector with that name is already here": "c'è già un connettore con quel nome",
  "that token can't be used": "quel token non si può usare",
  __patterns: [
    [/^Remove (.+)\?$/, "Rimuovere $1?"],
    [/^(\d+) is the most\. Remove one to add another\.$/, "$1 è il massimo. Rimuovine uno per aggiungerne un altro."],
    [/^(\d+) is the most; remove one first$/, "$1 è il massimo; rimuovine prima uno"],
    [/^a connector added by address already has this name: (.+)$/, "un connettore aggiunto per indirizzo ha già questo nome: $1"],
  ],
};
export default SELF;
