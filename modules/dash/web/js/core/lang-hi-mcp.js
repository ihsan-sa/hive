// hi: Settings > Connectors > Add a connector by address (views/settings.js mcpUrlBlock, E120a) and the server's reasons for a refusal
// (dash.py mcp_url_check / mcp_url_do), shown as they come. Exact English keys; patterns for the lines with a name or a count.
const SELF = {
  "Add a connector by address": "पते से कनेक्टर जोड़ें",
  "A remote MCP server, like a custom connector in Claude. Your agents can use its tools from their next run, so only add a server you trust.": "एक रिमोट MCP सर्वर, Claude के कस्टम कनेक्टर जैसा। आपके एजेंट अपने अगले रन से इसके टूल इस्तेमाल कर सकते हैं, इसलिए सिर्फ़ वही सर्वर जोड़ें जिस पर आपको भरोसा हो।",
  "Added. Agents can use it from their next run.": "जोड़ दिया। एजेंट अपने अगले रन से इसे इस्तेमाल कर सकते हैं।",
  "Address": "पता",
  "Agents stop seeing its tools from their next run.": "एजेंट अपने अगले रन से इसके टूल नहीं देखेंगे।",
  "has a token": "टोकन है",
  "Token (if the server needs one)": "टोकन (अगर सर्वर माँगे)",
  "paste the server's address": "सर्वर का पता चिपकाएँ",
  "that address has spaces in it or is too long": "इस पते में खाली जगह है या यह बहुत लंबा है",
  "that isn't a web address": "यह वेब पता नहीं है",
  "leave the sign-in out of the address; paste a token in its own box": "साइन-इन को पते से बाहर रखें; टोकन उसके अपने बॉक्स में चिपकाएँ",
  "the address must start with https:// (plain http only for a server on this computer)": "पता https:// से शुरू होना चाहिए (सादा http सिर्फ़ इसी कंप्यूटर के सर्वर के लिए)",
  "no connector added by address has that name": "पते से जोड़े गए किसी कनेक्टर का यह नाम नहीं है",
  "give it a short name: letters, digits, - and _ (up to 40)": "इसे छोटा नाम दें: अक्षर, अंक, - और _ (40 तक)",
  "a connector with that name is already here": "इस नाम का कनेक्टर पहले से है",
  "that token can't be used": "यह टोकन इस्तेमाल नहीं हो सकता",
  __patterns: [
    [/^Remove (.+)\?$/, "$1 हटाएँ?"],
    [/^(\d+) is the most\. Remove one to add another\.$/, "ज़्यादा से ज़्यादा $1। दूसरा जोड़ने के लिए एक हटाएँ।"],
    [/^(\d+) is the most; remove one first$/, "ज़्यादा से ज़्यादा $1; पहले एक हटाएँ"],
    [/^a connector added by address already has this name: (.+)$/, "पते से जोड़े गए एक कनेक्टर का यही नाम है: $1"],
  ],
};
export default SELF;
