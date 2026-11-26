// Hindi for the 23 words the Link buy card, Settings' repo line and the document viewer's carried note added after lang-hi-10.js
// (product-test keeper round 2026-11-24: drive_app_lang_fr failed on them in an early part-3 run; `node test/lang_left.mjs count` showed 23 in every
// language but Spanish). Same keys as lang-es-11.js's. Merged in lang.js, first in its language.
export default {
  "Bought": "खरीदा गया",
  "Canceled": "रद्द किया गया",
  "Card": "कार्ड",
  "Card expired": "कार्ड की अवधि समाप्त",
  "Charged": "शुल्क लिया गया",
  "Code, issues and updates are on": "कोड, इश्यू और अपडेट यहाँ हैं:",
  "Declined": "अस्वीकृत",
  "Expired": "समाप्त",
  "It has no merchant or amount.": "इसमें न विक्रेता है न राशि।",
  "It isn't JSON.": "यह JSON नहीं है।",
  "It isn't a buy.": "यह खरीद नहीं है।",
  "Item": "आइटम",
  "Merchant": "विक्रेता",
  "One-time card from Link, made on approval": "Link का एक-बार वाला कार्ड, मंज़ूरी पर बना",
  "Receipt": "रसीद",
  "Recording": "रिकॉर्डिंग",
  "Shipping": "शिपिंग",
  "Should have lapsed": "इसे समाप्त हो जाना चाहिए था",
  "Still open from rev": "अब भी खुला, इस संशोधन से: rev",
  "Tax": "कर",
  "Waiting for Link": "Link की प्रतीक्षा",
  "Waiting in Link": "Link में प्रतीक्षा",
  "test mode": "टेस्ट मोड",
  "Stop and send": "रोकें और भेजें",
  "Stopped and sent": "रोककर भेजा गया",
  "Send waits for the current step to end. Stop and send tries to cut that step short.": "भेजें मौजूदा चरण के खत्म होने तक रुकता है। “रोकें और भेजें” उस चरण को बीच में रोकने की कोशिश करता है।",
  __patterns: [
    [/^Stop .+ now and send this, without waiting for its step to end$/, () => "इसका चरण पूरा होने का इंतज़ार किए बिना अभी रोकें और यह भेजें"],
    [/^Still open from rev (.+)$/, (_, r) => "अब भी खुला, इस संशोधन से: rev" + " " + r],   // ui/docviewer.js: a carried item, the text is joined before h()
  ],
};
