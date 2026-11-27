// hi: Settings › Projects, export a project and bring one in (settings.js projectsBlock, projJob); product-test rung, 2026-11-27.
// Exact English keys; the patterns are the status lines with a project or file name in them.
const W = {
  // E207: the line in a conversation's reply place while it shows the copy kept on this device (views/comms.js readOnly)
  "Offline. This is the copy kept on this device, read only; replies need a connection.": "ऑफ़लाइन। यह इस डिवाइस पर रखी गई कॉपी है, सिर्फ़ पढ़ने के लिए; जवाब देने के लिए कनेक्शन चाहिए।",
  "Showing the chats this device last read. Trying again every few seconds.": "इस डिवाइस पर आख़िरी बार पढ़ी गई चैट दिखाई जा रही हैं। हर कुछ सेकंड में फिर कोशिश हो रही है।",
  "On, with the first line of each reply. It shows on your phone's lock screen and passes through ntfy.sh, a public service. Anyone who learns the topic below can read those lines, so keep it to yourself.": "चालू, हर जवाब की पहली पंक्ति के साथ। यह आपके फ़ोन की लॉक स्क्रीन पर दिखती है और ntfy.sh से होकर जाती है, जो एक सार्वजनिक सेवा है। नीचे दिया विषय जिसे भी पता हो, वह ये पंक्तियाँ पढ़ सकता है, इसलिए इसे अपने तक रखें।",
  "Show the reply's first line in the alert": "अलर्ट में जवाब की पहली पंक्ति दिखाएँ",
  "Hide the reply's first line": "जवाब की पहली पंक्ति छिपाएँ",
  "The alerts now carry the reply's first line.": "अब अलर्ट में जवाब की पहली पंक्ति आती है।",
  "The alerts no longer carry any words from a reply.": "अब अलर्ट में जवाब का कोई शब्द नहीं आता।",
  "Off: the alert only says something is waiting, so nothing from a reply shows on the lock screen.": "बंद: अलर्ट बस इतना कहता है कि कुछ इंतज़ार कर रहा है, इसलिए लॉक स्क्रीन पर जवाब का कुछ नहीं दिखता।",
  "Anyone who can see your phone's lock screen can read that line.": "जो भी आपके फ़ोन की लॉक स्क्रीन देख सकता है, वह यह पंक्ति पढ़ सकता है।",
  "it stopped without saying why": "यह बिना कारण बताए रुक गया",
  "your Hive restarted while this ran; start it again": "इसके चलते समय आपका Hive फिर से शुरू हुआ; इसे फिर से शुरू करें",
  "this sign-in can't follow it any more (full access ended after a while idle); it goes on running on your Hive, so sign in again and look in Projects": "यह साइन-इन अब इसे नहीं देख सकता (कुछ देर कोई गतिविधि न होने पर पूरी पहुँच खत्म हो गई); यह आपके Hive पर चलता रहता है, इसलिए फिर से साइन इन करें और प्रोजेक्ट में देखें",
  "your Hive stopped answering; it may still be running, so look in Projects in a while": "आपके Hive ने जवाब देना बंद कर दिया; यह शायद अब भी चल रहा है, इसलिए थोड़ी देर बाद प्रोजेक्ट में देखें",
  "Move a project to another Hive, with its history. Export saves one as a zip on this device; Bring in adds one from such a zip.": "किसी प्रोजेक्ट को उसके इतिहास के साथ दूसरे Hive पर ले जाएँ। एक्सपोर्ट उसे इस डिवाइस पर zip के रूप में सहेजता है; लाएँ ऐसे zip से एक जोड़ता है।",
  "Project to export": "एक्सपोर्ट करने का प्रोजेक्ट",
  "No projects yet": "अभी कोई प्रोजेक्ट नहीं",
  "Couldn't read the projects": "प्रोजेक्ट पढ़े नहीं जा सके",
  "Export": "एक्सपोर्ट",
  "Export the project as a zip": "प्रोजेक्ट को zip के रूप में एक्सपोर्ट करें",
  "its name here (optional)": "उसका नाम यहाँ (वैकल्पिक)",
  "Name for the project brought in": "लाए गए प्रोजेक्ट का नाम",
  "Bring in a project…": "प्रोजेक्ट लाएँ…",
  "Bring in a project from a zip": "zip से प्रोजेक्ट लाएँ",
  "What it did": "इसने क्या किया",
  "Bring in a project": "प्रोजेक्ट लाएँ",
  "Exporting": "एक्सपोर्ट हो रहा है",
  "Bringing in": "लाया जा रहा है",
};
const er = e => W[e] || e;   // the job's own error words, when they are these
export default {
  ...W,
  __patterns: [
    [/^Exporting (.+?)(…?)$/, (_, n, d) => `${n} एक्सपोर्ट हो रहा है` + d],
    [/^Bringing in (.+?)(…?)$/, (_, n, d) => `${n} लाया जा रहा है` + d],
    [/^(.+) is downloading\. Bring it in on the other Hive with "Bring in a project", here in its Settings\.$/, (_, f) => `${f} डाउनलोड हो रहा है। दूसरे Hive की सेटिंग्स में "प्रोजेक्ट लाएँ" से इसे लाएँ।`],
    [/^(.+) wasn't exported: (.+)$/, (_, n, x) => { const e = er(x); return `${n} एक्सपोर्ट नहीं हुआ: ${e}`; }],
    [/^Brought in as (.+): it's in Projects now\.$/, (_, n) => `${n} के नाम से लाया गया: अब यह प्रोजेक्ट में है।`],
    [/^(.+) wasn't brought in: (.+)$/, (_, n, x) => { const e = er(x); return `${n} नहीं लाया जा सका: ${e}`; }],
  ],
};
