// ja: Settings > Connectors > Add a connector by address (views/settings.js mcpUrlBlock, E120a) and the server's reasons for a refusal
// (dash.py mcp_url_check / mcp_url_do), shown as they come. Exact English keys; patterns for the lines with a name or a count.
const SELF = {
  "Add a connector by address": "アドレスでコネクタを追加",
  "A remote MCP server, like a custom connector in Claude. Your agents can use its tools from their next run, so only add a server you trust.": "リモートの MCP サーバーです(Claude のカスタムコネクタと同じ)。エージェントは次の実行からそのツールを使えるので、信頼できるサーバーだけを追加してください。",
  "Added. Agents can use it from their next run.": "追加しました。エージェントは次の実行から使えます。",
  "Address": "アドレス",
  "Agents stop seeing its tools from their next run.": "エージェントは次の実行からそのツールを使わなくなります。",
  "has a token": "トークンあり",
  "Token (if the server needs one)": "トークン(サーバーが求める場合)",
  "paste the server's address": "サーバーのアドレスを貼り付けてください",
  "that address has spaces in it or is too long": "そのアドレスは空白を含むか、長すぎます",
  "that isn't a web address": "それはウェブアドレスではありません",
  "leave the sign-in out of the address; paste a token in its own box": "アドレスにサインイン情報を入れないでください。トークンは専用の欄に貼り付けます",
  "the address must start with https:// (plain http only for a server on this computer)": "アドレスは https:// で始めてください(暗号化なしの http はこのコンピューター上のサーバーだけ)",
  "no connector added by address has that name": "その名前のアドレス追加コネクタはありません",
  "give it a short name: letters, digits, - and _ (up to 40)": "短い名前を付けてください:英字、数字、- と _(40 文字まで)",
  "a connector with that name is already here": "その名前のコネクタはすでにあります",
  "that token can't be used": "そのトークンは使えません",
  __patterns: [
    [/^Remove (.+)\?$/, "$1 を削除しますか?"],
    [/^(\d+) is the most\. Remove one to add another\.$/, "最大 $1 個です。追加するには 1 つ削除してください。"],
    [/^(\d+) is the most; remove one first$/, "最大 $1 個です。先に 1 つ削除してください"],
    [/^a connector added by address already has this name: (.+)$/, "アドレスで追加したコネクタがすでにこの名前を使っています:$1"],
  ],
};
export default SELF;
