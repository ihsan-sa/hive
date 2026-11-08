"""Names and roles on a page, and whether a page really backs a claim about someone. Pattern matching: it finds candidates and checks words on a page, and doesn't pretend to understand them."""
import re
import unicodedata
from .extract import fold, registrable, domain_of

ROLE = r"(?:(?:Senior |Associate |Assistant |Deputy |Interim |Acting |Executive |Founding |Vice |Co-?)*(?:President|Vice President|Vice Chancellor|Chancellor|Provost|Dean|Director|Chief [A-Z][a-z]+(?: [A-Z][a-z]+)* Officer|C[EOTFI]O|Chair(?:person|woman|man)?|Professor|Head|Manager|Coordinator|Superintendent|Founder|Partner|Principal|Lead|Secretary|Commissioner|Faculty|Instructor|Lecturer|Fellow|Officer|Editor|Researcher|Scientist|Engineer|Advisor|Adviser))"
_ROLE_RE = re.compile(r"\b" + ROLE + r"\b", re.I)
IROLE = f"(?i:{ROLE})"
_W = r"[A-ZÀ-Þ][\w'’-]+"
_SP = r"[ \t\u00a0]+"  # a name stays on one line: across lines it's a menu
_PARTICLE = r"(?:de|del|della|di|du|la|le|van|von|der|den|ter|bin|ibn|al|el|da|dos|das)"  # Jean de la Verpilliere
NAME = rf"{_W}(?:{_SP}[A-Z]\.)*(?:{_SP}(?:{_PARTICLE}{_SP})*{_W}){{1,2}}"
SUFFIX = r"(?:,?\s+(?:Jr\.?|Sr\.?|II|III|IV|Ph\.?\s?D\.?|Ed\.?\s?D\.?|M\.?D\.?|J\.?D\.?|MBA|M\.?S\.?|DBA|Esq\.?))*"
HONOR = r"(?:(?:Dr|Prof|Mr|Ms|Mrs|Mx)\.?\s+)?"
NOT_NAME = set("""has officially stepped steps expect compensation analysis by blvd boulevard ave avenue road rd drive highway parkway suite log copy position profile vertically integrated projects effectiveness governor senator congressman representative act acts report reports offices hub directory services resources contact contacts apply news events calendar careers staff faculty students alumni tech university college community center centre office department school institute program programs campus board foundation technology artificial intelligence data science workforce president vice dean director chief associate assistant executive senior interim the news read more contact student students services academic affairs human resources learn click home about our staff faculty new state national american united north south east west county city district education learning development innovation career careers business engineering computer health online division professional continuing leadership team trustees chancellor provost professor manager coordinator chair head email phone view all meet his her their this that these those when where with from for and page press release media event events research strategic planning economic public relations information systems digital cyber security machine ai it us january february march april may june july august september october november december monday tuesday wednesday thursday friday saturday sunday he she we they you read share follow privacy policy site map search menu working title property management job jobs salary posting vacancy vacancies opening openings""".split())
_LINE_ROLE = re.compile(rf"^(?:the\s+)?(?:{ROLE})\b", re.I)


# Office and abstract nouns that head a label ("Property Management", "Grants Administration"). Shape rule: a label whose every word is one of these, or ends like an abstract noun, is a heading. Nothing here is a common given name or surname.
_COMMON = set("""property management grants grant administration accounting finance financial payroll procurement purchasing facilities operations planning compliance records admissions registrar marketing communications recruiting recruitment hiring employment benefits policy policies safety risk legal audit budget budgets assets inventory transportation parking housing dining library libraries athletics counseling advising tutoring outreach engagement partnerships partnership commercialization licensing patents patent transfer entrepreneurship ventures venture incubator accelerator sponsored programs project projects intellectual rights general main front desk senior junior full part time remote staff positions application applications requirements qualifications responsibilities description summary overview details location department division unit sector industry sciences studies arts humanities""".split())
_ABSTRACT = re.compile(r"(?:ment|tion|sion|ness|ship|ance|ence|ity|ics|ings?)s?$", re.I)


def _heading_like(toks):
    low = [t.lower() for t in toks]
    return all(t in _COMMON or (len(t) > 6 and _ABSTRACT.search(t)) for t in low)


def plausible_name(n):
    toks = [t.strip(".,'’") for t in n.split()]
    if not 2 <= len(toks) <= 4 or len(n) > 45: return False
    if _heading_like(toks): return False
    if any(t.lower() in NOT_NAME for t in toks): return False
    # BizTUCSON: capitals in the middle of a word make a brand. McDonald, DeShawn and O'Brien have just one.
    if any(re.search(r"[a-z].*[A-Z].*[A-Z]", part) for t in toks for part in re.split(r"[-'’]", t)): return False
    # CARES Act, NOVA Offices: one shouted word among ordinary ones is a label, not a name.
    shouted = [t for t in toks if len(t) > 2 and t.isupper()]
    if shouted and len(shouted) < sum(len(t) > 1 for t in toks): return False
    return sum(len(t) > 1 for t in toks) >= 2


def clean_name(n):
    n = re.sub(r"^(?:Dr|Prof|Mr|Ms|Mrs|Mx)\.?\s+", "", n.strip())
    n = re.sub(SUFFIX + "$", "", n).strip(" ,")
    return n.title() if n.isupper() else n  # BRIAN BARNETT, as some directories print it


def clean_title(t):
    t = t.split("\n")[0]
    # Where the post ends: "CEO of Nexeon, said:", "..., Dr Alex Groombridge, CTO" (the next person), "CEO, spearheads the ...".
    t = re.split(r"(?<![A-Z])[.;:|•]\s|\s[–—-]\s|\s(?:said|says|who|and has|has|is|was|will|at|in|on|to)\s|[\"“”(]|,?\s(?:said|says|added|explained)\b|,\s+(?:Dr|Prof|Mr|Ms|Mrs)\.?\s|,\s+[a-z]+s\s+(?:the|a|an|its)\b|,\s+the\s+[a-z]|,\s+(?:where|which|whose|when)\b|\suntil\s|\sand\s+(?:highly|widely|also|one)\b", t)[0].strip(" ,.")
    t = re.sub(r"\b((?:co-?)?founder)\s+of\s+[a-z][a-z -]*(?:,.*)?$", r"\1", t, flags=re.I)  # "co-founder of advanced materials supplier"
    if len(t) > 110: t = t[:110].rsplit(" ", 1)[0].strip(" ,")  # whole words only: a cut "Inno" read as another employer
    return t


_PATS = [
    re.compile(rf"(?P<name>{HONOR}{NAME}){SUFFIX}\s*(?:,|\s[–—|-]\s)\s*(?:the\s+|an?\s+|[A-Z][\w&.-]*['’]s\s+)?(?P<title>(?:[A-Z][\w&'-]*\s+|of\s+|for\s+|and\s+){{0,3}}{IROLE}\b[^\n]{{0,90}})"),
    re.compile(rf"(?P<name>{HONOR}{NAME}){SUFFIX}\s+(?:is|was|serves as|served as|has been named|was named|was appointed|has been appointed|became|joined as)\s+(?:the\s+|an?\s+|as\s+)*(?:new\s+)?(?P<title>(?:[A-Z][\w&'-]*\s+|of\s+|for\s+|and\s+){{0,3}}{IROLE}\b[^\n]{{0,90}})"),
    re.compile(rf"(?<![A-Za-z] )(?P<title>{ROLE}(?:{_SP}(?:of|for){_SP}[A-Z][\w&'-]*(?:{_SP}(?:and{_SP})?[A-Z][\w&'-]*){{0,4}})?){_SP}(?P<name>(?:Dr\.\s+)?{NAME})\b(?!\s+(?:of|for)\b)"),
]


def people_in(text, ld=()):
    """[{name, title, how}] on a page, one per name, with the longest title seen. Candidates, not facts: check() decides."""
    text = _plain(text)
    out = {}
    def add(name, title, how):
        name, title = clean_name(name), clean_title(title)
        if not plausible_name(name) or not _ROLE_RE.search(title): return
        k = fold(name)
        if k not in out or len(title) > len(out[k]["title"]): out[k] = {"name": name, "title": title, "how": how}
    for p in ld: add(p["name"], p["title"], "structured")
    for pat in _PATS:
        for m in pat.finditer(text):
            title = m.group("title")
            # A title the page wrapped: "dean of Career and\nTechnical Programs".
            if re.search(r"\b(?:and|of|for|&)\s*$", title):
                more = re.match(r"\s*\n[ \t]*([A-Z][^\n]{0,60})", text[m.end():])
                if more: title = title.rstrip() + " " + more.group(1)
            add(m.group("name"), title, "prose")
    lines = [l.strip() for l in text.split("\n") if l.strip()]
    for a, b in zip(lines, lines[1:]):
        if re.fullmatch(HONOR + NAME + SUFFIX, a) and _LINE_ROLE.match(b) and len(b) < 120 and "directory" not in b.lower(): add(a, b, "directory")
    return list(out.values())


TITLE_STOP = set("a an and at for in of on the to with".split())
_PLACE_AFTER = re.compile(r"[ \t]*(?:'s[ \t]+)?(?:endowed|distinguished|professorship|fellowship|scholarship|center|centre|institute|building|hall|library|school of|college of|foundation|award|prize|lecture|fund|auditorium|stadium|boulevard|road|street|memorial)\\b".replace("\\\\b", r"\b"))
_POST_AFTER = re.compile(r"[ \t]+(?:executive director|chair|professor|fellow|scholar)\b")
_POST_BEFORE = re.compile(r"(?:\bthe|\band|/|,|\bas)[ \t]+$")


def name_regex(name):
    """Matches a name in folded text with a middle part or none: 'jane doe', 'jane q. doe', 'doe, jane'. Not 'jane and mary doe', and a middle name that differs from the claim's is another person."""
    toks = [t for t in fold(clean_name(name)).split() if t.strip(".")]
    if not toks: return re.compile(r"(?!)")
    if len(toks) < 2: return re.compile(rf"(?<![\w-]){re.escape(toks[0])}(?![\w-])")
    first, last, middles = re.escape(toks[0]), re.escape(toks[-1]), {t[0] for t in toks[1:-1]}
    only = rf"(?=[{''.join(re.escape(c) for c in sorted(middles))}])" if middles else ""
    return re.compile(rf"(?<![\w-]){first}(?:\s+{only}(?!and\b|or\b)[\w.'-]+){{{{0,2}}}}?\s+{last}(?![\w-])|(?<![\w-]){last},\s+{first}(?![\w-])".replace("{{", "{").replace("}}", "}"))


def person_key(name):
    """One key for one person: Justin H. Lonon and Justin Lonon are the same row."""
    toks = [t for t in fold(clean_name(name)).split() if len(t.strip(".")) > 1]
    return f"{toks[0]} {toks[-1]}" if len(toks) >= 2 else fold(name)


def term_regex(term):
    """A term as whole words in folded text, so AI doesn't match training or villain."""
    return re.compile(rf"(?<![\w]){re.escape(fold(term))}(?![\w])")


def named_not_person(text, name):
    """True when the name shows up here only as part of something named after someone: the Fannie Mitchell Executive Director, Jane Doe Hall. A name with a title on the next line is a person: that's how staff pages are laid out."""
    low = fold(text)
    ms = list(name_regex(name).finditer(low))
    def named(m):
        after = low[m.end():m.end() + 40]
        if _PLACE_AFTER.match(after): return True
        return bool(_POST_AFTER.match(after) and _POST_BEFORE.search(low[max(0, m.start() - 12):m.start()]))
    return bool(ms) and all(named(m) for m in ms)


RANK = {"president", "vice", "chancellor", "senior", "associate", "assistant", "deputy", "executive", "interim", "acting"}
ABBREV = [(r"\bavp\b", "associate vice president"), (r"\bsvp\b", "senior vice president"), (r"\bevp\b", "executive vice president"), (r"\bvp\b", "vice president"), (r"\bexec\b\.?", "executive"), (r"\bassoc\b\.?", "associate"), (r"\basst\b\.?", "assistant"), (r"\bdir\b\.?", "director"), (r"\bprof\b\.?", "professor")]


def long_form(low):
    for a, b in ABBREV: low = re.sub(a, b, low)
    return low


def _title_words(title):
    return [w for w in re.findall(r"[\w&'-]+", long_form(fold(re.sub(r"\([^)]*\)", " ", title)))) if w not in TITLE_STOP]


_ABBR = set("dr mr ms mrs mx prof st jr sr inc ltd corp co vs no dept univ sen rep gov gen lt col capt rev hon".split())
_BODY = set("academy advisory agency alliance association authority board bureau center centre college committee council department district division foundation group institute laboratory league office partnership program project school society state university".split())  # "AI Advisory Council" is capitalised and isn't a person
_PRONOUN = re.compile(r"(?:he|she|they|his|her|their)\b", re.I)
_CLAUSE = re.compile(r"[,;:()|–—]|\s-\s|\band\b|\bwith\b")
_RANK_BELOW = "assistant|associate|deputy|vice|sub|under"
_LINE_LOOSE = rf"{_W}(?:{_SP}[A-Z]\.)*(?:{_SP}(?:{_PARTICLE}{_SP})*{_W}){{{{1,5}}}}".replace("{{", "{").replace("}}", "}")


def _plain(text):
    """Accents off as fold does, capitals kept: they are how a person or an employer shows."""
    text = "".join(c for c in unicodedata.normalize("NFKD", text) if not unicodedata.combining(c)).replace("’", "'")
    return _LABEL.sub("\n", text)


# A contact card labels the post ("Title:\n Director, Technology Transfer"): drop the label so the post sits under the name.
_LABEL = re.compile(r"\n[ \t]*(?:(?:job|current)\s+)?(?:title|position|role|designation)[ \t]*:[ \t]*(?:\n[ \t\xa0]*)?", re.I)


def _page_lines(text):
    """Non-blank lines, with a paragraph that was wrapped by hand put back together: "dean of Career and / Technical Programs" is one line, a name over a title isn't."""
    out = []
    for block in re.split(r"\n\s*\n", _plain(text)):
        lines = [l.strip() for l in block.split("\n") if l.strip()]
        for line in lines:
            if len(lines) > 1 and out and out[-1] is not None and (re.match(r"[a-z(]", line) or re.search(r"(?:\b(?:and|or|of|the|for|in|at|to|with|said)|,)$", out[-1])): out[-1] += " " + line
            else: out.append(line)
        out.append(None)
    return [l for l in out if l is not None]


def _sentences(line):
    """A line cut into sentences, not at Dr. or Inc. or an initial."""
    out, at = [], 0
    for m in re.finditer(r"(?<=[a-z0-9)])[.!?]\s+(?=[A-Z\"“])", line):
        word = re.search(r"[A-Za-z]+$", line[at:m.start()])
        if word and word.group().lower() in _ABBR: continue
        out.append(line[at:m.start() + 1]); at = m.end()
    return out + [line[at:]]


def _others(seg, skip, own_words):
    """Spans of other people in this text. A run of capitalised words is cut at title and office words, so "Dean of Engineering Bob Roe" gives Bob Roe; a run that's all words of the claim's own title or org isn't a person."""
    found = []
    for m in re.finditer(_LINE_LOOSE, seg):
        run = []
        for t in list(re.finditer(r"\S+", m.group())) + [None]:
            word = t.group().strip(".,'").lower() if t else ""
            if t and word not in NOT_NAME and not _ROLE_RE.fullmatch(word):
                run.append(t)
                continue
            if len(run) >= 2:
                a, b = m.start() + run[0].start(), m.start() + run[-1].end()
                words = {w.strip(".,'").lower() for w in seg[a:b].split()}
                if plausible_name(seg[a:b]) and not (a < skip[1] and skip[0] < b) and not words <= own_words and not words & _BODY: found.append((a, b))
            run = []
    return found


def _own_part(seg, m, others):
    """The part of a sentence that can be about the person at m: not past another person, and not a title that sits against that other person."""
    start, end = 0, len(seg)
    after = [o for o in others if o[0] >= m.end()]
    if after:
        end = after[0][0]
        last = _CLAUSE.split(seg[m.end():end])[-1]
        if _ROLE_RE.search(last): end -= len(last)
    before = [o for o in others if o[1] <= m.start()]
    if before: start = m.start() - len(_CLAUSE.split(seg[before[-1][1]:m.start()])[-1])
    return seg[start:end]


def _alone(line, m):
    """Is the name all there is on this line, bar a courtesy title and letters after it?"""
    before = re.sub(r"^(?:Dr|Prof|Professor|Mr|Ms|Mrs|Mx|Sir|Dame)\.?$", "", line[:m.start()].strip(), flags=re.I)
    return not (before + re.sub(rf"^{SUFFIX}", "", line[m.end():])).strip(" ,.:-–—|")


def _names_person(line, skip_words):
    return bool(_others(line, (0, 0), skip_words))


def attached(text, name, own_words=frozenset()):
    """Every piece of the page that can be about this person: the sentence naming them up to the next person, the lines under (or else over) a name alone on its line, and a following sentence that goes on about them."""
    rx = re.compile(name_regex(name).pattern, re.I)
    toks = [t for t in fold(clean_name(name)).split() if len(t.strip(".")) > 1]
    last = re.compile(rf"\b{re.escape(toks[-1])}\b", re.I) if toks else None
    lines = _page_lines(text)
    for i, line in enumerate(lines):
        if not rx.search(line): continue
        sents = _sentences(line)
        for j, sent in enumerate(sents):
            m = rx.search(sent)
            if not m: continue
            others = _others(sent, m.span(), own_words)
            piece = [_own_part(sent, m, others)]
            for later in sents[j + 1:j + 4]:
                if _others(later, (0, 0), own_words): break
                if (later is sents[j + 1] and _PRONOUN.match(later)) or (last and last.search(later)): piece.append(later)
            if _alone(line, m):
                under = []
                for below in lines[i + 1:i + 3]:
                    if _names_person(below, own_words): break
                    under.append(below)
                if not any(_ROLE_RE.search(u) for u in under) and i and not _names_person(lines[i - 1], own_words) and not (i > 1 and _names_person(lines[i - 2], own_words)):
                    under.insert(0, lines[i - 1])
                piece += under
            yield " ".join(piece)


def _title_words_of(title, org=""):
    return {w.lower() for w in re.findall(r"[\w'-]+", _plain(f"{title} {org}"))}


def title_on(text, name, title, org=""):
    """(share of the title's words attached to the name, best quote, the higher-or-lower post the page gives instead). A role word itself must be there. A title with two posts (a; b or a / b) counts if either holds."""
    pieces = [(p, long_form(fold(p))) for p in attached(text, name, _title_words_of(title, org))]
    best, quote, below = (False, 0.0), "", ""
    for part in re.split(r"\s*[;/]\s*", title):
        words = _title_words(part)
        role = [w for w in words if _ROLE_RE.fullmatch(w) or w in RANK]
        if not words: continue
        for raw, low in pieces:
            if role and not all(re.search(rf"\b{re.escape(w)}\b", low) for w in role): continue
            share = sum(1 for w in words if re.search(rf"\b{re.escape(w)}\b", low)) / len(words)
            other = _other_rank(low, role, words)
            if (not other, share) > best or not quote:
                best, quote, below = (not other, share), " ".join(raw.split())[:300], other
    return best[1], quote, below


# A verb right after "Name, Title," says what this person did; a capitalised word there would be another employer (folded text loses case, so only known verbs and -ed words count).
_VERB_AFTER = re.compile(r",\s*(?:spoke|said|says|leads|runs|opened|told|wrote|added|noted|stated|explained|announced|welcomed|presented|discussed|introduced|praised|launched|led|oversees|joined)\b|,\s*(?!(?:college|university|school|institute)\b)[a-z]{3,}ed\s+(?!college\b|university\b)")


def _alone_name(line):
    """A line that is just someone's name, as on a staff card."""
    m = re.fullmatch(rf"\s*{HONOR}({NAME}){SUFFIX}\s*", line)
    return bool(m) and plausible_name(m.group(1))


def title_slot(text, name, title, org=""):
    """A positive grammatical slot, not arbitrary words attached to a name.
    Unrecognised prose remains partial. This deliberately sacrifices recall:
    a title modifier or an unexplained continuation cannot establish a post.
    """
    rx = re.compile(name_regex(name).pattern, re.I)
    lines = _page_lines(text)
    def role(candidate, appt=False):
        candidate = long_form(fold(candidate)).strip(" .")
        candidate = re.sub(r"^(?:the|a|an)\s+", "", candidate)
        for part in re.split(r"\s*[;/]\s*", title):
            words = _title_words(part)
            if not words: continue
            # Stopwords and punctuation may vary, but the role's words stay in order.
            pat = r"[\s,]*(?:(?:of|for|the|and|at|in)\s+)*".join(re.escape(w) for w in words)
            m = re.match(pat + r"(?![\w'-])", candidate)
            if not m:
                # A second explicit post in a compound title belongs to the same name.
                prefix, sep, suffix = candidate.partition(" and ")
                if sep and _ROLE_RE.fullmatch(prefix) and role(suffix): return True
                continue
            tail = candidate[m.end():].strip()
            if not tail or tail == ".": return True
            if re.fullmatch(r"since (?:19|20)\d\d\.?", tail): return True
            if tail.startswith(("|", "—", "–")) and "@" in tail: return True
            if _VERB_AFTER.match(tail): return True
            # An appointment sentence names the unit the post sits in: "... appointed Dean within the College of X."
            if appt and re.fullmatch(r",\s*[a-z][\w ,&'-]{2,80}\.?", tail): return True  # the unit's name continues past a comma
            if appt and re.fullmatch(r"(?:within|in|at)\s+(?:the\s+)?[\w ,&'-]{3,120}\.?", tail): return True
            if org and re.fullmatch(r"(?:at|of|,)\s*" + re.escape(fold(org_core(org))) + r"(?:(?:" + _VERB_AFTER.pattern + r").*)?\.?", tail): return True
            # A short role may be followed by a department, or another role.
            extension = re.sub(r"^(?:of|for|and)\s+", "", tail)
            ws = set(re.findall(r"[\w-]+", extension))
            if tail.startswith(("of ", "for ", "and ")) and ws and ws <= POST_WORDS and not ws & {"emeritus", "emerita", "student", "member", "day"}: return True
        return False
    toks = [t for t in fold(clean_name(name)).split() if len(t.strip(".")) > 1]
    # "She is Dean of Students." or "Doe is Dean of Students." straight after the sentence naming them.
    then = re.compile(rf"^(?:she|he|they|{re.escape(toks[-1]) if toks else 'she'})\s+(?:is|serves as|has been)\s+(.*)$", re.I)
    contact = re.compile(r"\s*(?:[\w.+-]+@[\w-]+(?:\.[\w-]+)+|\+?[\d(][\d ().-]{6,}\d)\s*$")
    def contact_line(line):
        return bool(re.fullmatch(r"\s*(?:(?:e-?mail|phone|tel|fax|contact)\b.*|[\w.+-]+@[\w-]+(?:\.[\w-]+)+|\+?[\d(][\d ().-]{6,}\d)\s*", line, re.I))
    for i, line in enumerate(lines):
        # A directory row: "Jane Doe<TAB>Dean of Students<TAB>jdoe@example.com", nothing but contact cells after the post.
        if "\t" in line:
            cells = [c.strip() for c in line.split("\t") if c.strip()]
            if len(cells) > 1 and rx.search(cells[0]) and _alone(cells[0], rx.search(cells[0])) and role(cells[1]) and all(contact_line(x) for x in cells[2:]):
                return " ".join(line.split())[:300]
        sents = _sentences(line)
        for k, sent in enumerate(sents):
            m = rx.search(sent)
            if not m: continue
            head, tail = sent[:m.start()].strip(), sent[m.end():].strip()
            head = re.sub(r"^(?:said\s+)?(?:Dr|Prof|Mr|Ms|Mrs)\.?$", "", head, flags=re.I).strip()
            if head.lower() == "said": head = ""
            tail = re.sub(rf"^{SUFFIX}", "", " " + tail).strip()
            tail = re.sub(r"^,?\s*(?:(?:Ph\.?\s?D\.?|Ed\.?\s?D\.?|MBA|M\.?D\.?),?\s*)+", "", tail, flags=re.I)
            if not head:
                value = re.sub(r"^(?:[,|–—-]\s*|(?:currently\s+)?(?:(?:has been|was|is) (?:appointed|named|promoted|elevated) (?:as |to )?|(?:is|serves as|has been)\s+))", "", tail, count=1, flags=re.I)
                appt = bool(re.match(r"(?:(?:has been|was|is) (?:appointed|named|promoted|elevated)\b)", tail, re.I))
                if tail and role(value, appt): return " ".join(sent.split())[:300]
                # "Jane Doe (Dean of Students)": the whole rest of the sentence in brackets.
                inner = re.fullmatch(r"\(([^()]*)\)\.?", tail)
                if inner and role(inner.group(1)): return " ".join(sent.split())[:300]
            # "Our Dean of Students, Jane Doe, welcomes you.": the post, then the name set off by commas, then a verb.
            lead = re.fullmatch(r"(?:our|the|its)\s+(.*?),", head, flags=re.I)
            if lead and role(lead.group(1)) and (_VERB_AFTER.match(fold(tail)) or re.match(r",\s*(?:welcomes|invites|greets|hosts|encourages)\b", tail)): return " ".join(sent.split())[:300]
            if role(head.rstrip(":")) and (not tail or re.fullmatch(r"(?:opened the event|spoke)\.?", tail, re.I)):
                return " ".join(sent.split())[:300]
            # A sentence with no post of its own, then "She is Dean of Students." with nobody else named between.
            if not _ROLE_RE.search(sent):
                for j in range(k + 1, min(k + 3, len(sents))):
                    later = sents[j]
                    if rx.search(later) or _others(later, (0, 0), _title_words_of(title, org)): break
                    t = then.match(later.strip())
                    if t and role(t.group(1)): return " ".join(" ".join(sents[k:j + 1]).split())[:300]
            # A name alone on its line (bar an address or phone) takes the line under it, or else the line over it,
            # but over only when nothing under it reads as another post and the line above that isn't someone else.
            bare = contact.sub("", sent)
            if _alone(bare, rx.search(bare) or m):
                below = lines[i + 1] if i + 1 < len(lines) else ""
                # A card's post may run on into its email or phone on the same line.
                post = below
                while True:
                    end = contact.search(post)
                    # A phone has at least ten digits; "1998-2004" is a term of office, not a number to ring.
                    if not end or ("@" not in end.group() and (len(re.sub(r"\D", "", end.group())) < 10 or re.fullmatch(r"\s*(?:19|20)\d\d\s*[-–]\s*(?:19|20)?\d\d\s*", end.group()))): break
                    post = post[:end.start()].rstrip(" |,;")
                if below and role(post): return " ".join((sent + " " + below).split())[:300]
                if (not below or contact_line(below)) and i and role(lines[i - 1]) and not (i > 1 and _alone_name(lines[i - 2])):
                    return " ".join((lines[i - 1] + " " + sent).split())[:300]
    return ""


def _other_rank(low, role, words):
    """The post the page really gives, when the claim's first role word only shows up as part of a different one: Assistant Director for Director, Assistant to the Dean for Dean."""
    if not role: return ""
    w = re.escape(role[0])
    prefix = rf"\b(?:(?:{_RANK_BELOW})[ -]+|(?:(?:special )?assistant|secretary|advis[eo]r) to (?:the )?)(?={w}\b)"
    uses = len(re.findall(rf"\b{w}\b", low))
    found = [m for m in re.finditer(prefix, low) if not set(m.group().replace("-", " ").split()) & set(words)]
    return (found[0].group() + role[0]).strip() if found and len(found) == uses else ""


def title_near(text, name, title, org=""):
    """(share, quote) as title_on gives them."""
    return title_on(text, name, title, org)[:2]


ORG_NOISE = set("the of and a at for in inc llc ltd corp corporation company college colleges university community institute school state district system center technical suny cuny".split())


def org_core(org):
    """The organisation itself from a label like 'Triton College (IL)' or 'Duke University Career Center'."""
    o = re.sub(r"\([^)]*\)", " ", org)
    o = re.split(r"\s[–—-]\s|,", o)[0]
    m = re.match(r"(.*?\b(?:College|University|Institute of Technology)(?:\s+of\s+[A-Z][\w-]+)?)\b", o)
    return " ".join((m.group(1) if m else o).split())


def names_org(org, text):
    low = fold(text)
    return fold(org) in low or fold(org_core(org)) in low


def org_near(text, name, org, window=300):
    """Is the organisation named close to the person, by the distinctive words of its name? A long page can name the college and also a panel of people from elsewhere."""
    low = fold(text)
    words = [t for t in re.findall(r"[a-z0-9]+", fold(org_core(org))) if t not in ORG_NOISE] or re.findall(r"[a-z0-9]+", fold(org_core(org)))
    for m in name_regex(name).finditer(low):
        near = low[max(0, m.start() - window):m.end() + window]
        if all(re.search(rf"\b{re.escape(w)}\b", near) for w in words): return True
    return False


# Words a college post is made of. Any other capitalised word in a title is probably another employer: Intel Vice President, Chancellor of the Ohio Department.
POST_WORDS = set("""advisory board ceo cto coo cfo cso cio cmo founder co-founder cofounder chairman chairwoman non-executive adviser secretary controller head academic academics academy accreditation adult advancement advising advisor affairs ai analytics applied art arts assessment assistant associate business campus career careers center centre chair chancellor chief college colleges commerce community computer computing continuing corporate council counseling credit curriculum data dean deputy design development digital director distance district division early economic education educational elearning emerging employer employers engagement engineering enrollment enterprise entrepreneurship executive experiential extended external faculty finance founding future general global grants graduate health higher human humanities hub information initiatives innovation institute institutional instruction instructional interim internships it job jobs language leadership learning liberal library life manager management manufacturing marketing math mathematics medical officer office online operations opportunity outreach partner partners partnership partnerships planning policy president principal professional program programs provost readiness regional research resources science sciences school senior services skills social stem strategic strategy student students studies success superintendent support sustainability systems talent teaching technical technologies technology trades training transfer transformation trade trustees university vice workforce acting and at for in of on the to with""".split())


DEGREES = {"phd", "mba", "mph", "ma", "ms", "md", "edd", "jd", "dba", "cpa"}
_POST_HEAD = r"(?:(?:Senior|Executive|Associate|Assistant|Deputy|Global|Regional|National|Group)\s+)*(?:Vice President|President|Vice Chancellor|Chancellor|Director|Chief|Dean|Provost|CEO|CIO|CTO|Manager|Head)\b"
_ORG_END = r"(?:College|University|Institute|Inc|LLC|Ltd|Corp|Corporation|Company|Networks|Systems|Technologies|Foundation|Federation|Association|Society|Agency|Commission|Department|Board|Council)"


_CHIEF = r"(?:Chief\s+(?:[A-Z][\w&-]*\s+){1,3}?Officer|CEO|CTO|COO|CFO|CIO|CSO|CMO|Managing Director|(?:Co-?)?[Ff]ounder)"


def elsewhere(title, org):
    """The other employer a title names, or '': in front of the post (Intel Vice President), as its last part (director, Oracle) or after it (Chancellor of the Ohio Department of Higher Education). Words of the post itself don't count, known or not."""
    own = set(re.findall(r"[a-z0-9]+", fold(org)))
    initials = "".join(t[0] for t in re.findall(r"[a-z0-9]+", fold(org)) if t not in ("the", "of", "and", "at", "for", "in"))
    title = re.sub(r"['’]s\b", "", title)
    def caps(part): return re.findall(r"[A-Z][\w&-]*", part)
    def foreign(part):
        for w in caps(part):
            low = fold(w)
            if low not in own and low != initials and low not in POST_WORDS and low not in DEGREES and low not in ORG_NOISE: return w
        return ""
    m = re.match(rf"((?:[A-Z][\w&.-]*\s+){{1,3}}?){_POST_HEAD}", title)
    if m and not re.search(r"\bCampus\b", m.group(1)) and foreign(m.group(1)): return foreign(m.group(1))
    m = re.search(rf"\b(?:of|at|with|for)\s+(?:the\s+)?((?:[A-Z][\w&.-]*\s+){{1,4}}?{_ORG_END})\b", title)
    if m and foreign(m.group(1)): return foreign(m.group(1))
    # A chief runs a whole company, so what follows names it, whatever its name ends in: "Chief Technology Officer of UKBIC".
    m = re.search(rf"\b{_CHIEF}\s+(?:of|at)\s+(?:the\s+)?((?:[A-Z][\w&.-]*\s*){{1,4}})", title)
    if m and foreign(m.group(1)): return foreign(m.group(1))
    parts = [p.strip() for p in title.split(",")[1:] if p.strip() and fold(p.strip()) not in DEGREES]
    if parts:
        last = parts[-1]
        names_employer = re.search(rf"\b{_ORG_END}\.?$", last) or not any(fold(w) in POST_WORDS for w in re.findall(r"[A-Za-z][\w&-]*", last))
        if names_employer and foreign(last): return foreign(last)
    return ""


def employer_after(text, name, org, window=120):
    """The outside body the page gives right after this person's post ("Jamie Atchison, senior director at ASPPH"), or ''."""
    own = set(re.findall(r"[a-z0-9]+", fold(org)))
    initials = "".join(t[0] for t in re.findall(r"[a-z0-9]+", fold(org)) if t not in ("the", "of", "and", "at", "for", "in"))
    rx = re.compile(name_regex(name).pattern, re.I)
    # Accents and spacing off as fold does, but capitals kept: they are how an employer shows.
    plain = " ".join("".join(c for c in unicodedata.normalize("NFKD", text) if not unicodedata.combining(c)).replace("’", "'").split(" "))
    for m in rx.finditer(plain):
        tail = plain[m.end():m.end() + window]
        tail = re.split(r"[.;:!?\n“”\"]", tail)[0]
        at = re.search(r"\bat\s+(?:the\s+)?([A-Z][\w&-]*(?:\s+[A-Z][\w&-]*)*)", tail)
        if not at: continue
        words = [fold(w) for w in at.group(1).split()]
        if at.group(1).lower() == initials or set(words) & (own - ORG_NOISE): continue
        # "at the College", "at Downtown Campus": a place within, not another employer.
        if set(words) & {"campus", "center", "centre", "school", "office", "department"} or all(w in ORG_NOISE or w in POST_WORDS for w in words): continue
        return at.group(1)
    return ""


def employer_before(text, name, org, window=90):
    """The other employer named in front of this person's post ("Intel Corporation (Intel) Vice President of Government Relations Al Thompson"), or ''."""
    rx = re.compile(name_regex(name).pattern, re.I)
    plain = "".join(c for c in unicodedata.normalize("NFKD", text) if not unicodedata.combining(c)).replace("’", "'")
    for m in rx.finditer(plain):
        seg = re.sub(r"(?:Dr|Prof|Mr|Ms|Mrs|Mx)\.?\s*$", "", plain[max(0, m.start() - window):m.start()])
        seg = re.split(r"[.;:!?,\n“”\"]", seg)[-1]
        seg = re.sub(r"\([^)]*\)\s*", "", seg)
        seg = re.sub(r"^(?:[^A-Z]*\s)?(?=[A-Z])", "", seg).strip()
        # "Request Executive Vice Chancellor Monique Reeves": a sentence's first word, not an employer.
        seg = re.sub(r"^(?:Request|Invite|Contact|Meet|Ask|Email|Call|Join|Welcome|Congratulations|Thanks|Thank|Former|New|Our|The|By|With|From|When|While|Then|According|Said|Says)\s+", "", seg)
        if not seg: continue
        head = re.match(rf"((?:[A-Z][\w&.-]*\s+){{1,3}}?){_POST_HEAD}", seg)
        if not head or re.search(r"\bCampus\b", head.group(1)): continue
        other = elsewhere(seg, org)
        if other: return other
    return ""


_NOT_EMPLOYER = re.compile(r"\b(?:e-?mail|phone|tel|fax|contact|bio|biography|read more|view|profile|website|office|hours|campus|department|dept|division|college|school|center|centre|institute|program|programs|services|affairs|admissions|registrar|finance|marketing|communications|advancement|athletics|library|operations|administration|enrollment|development|education|research|studies|sciences|arts|engineering|business|law|medicine|nursing|students?|faculty|staff|board|team|committee|council|emeritus|emerita)\b", re.I)


def employer_listed(text, name, org):
    """The other body printed straight after a person's name and post: "Mike Pettit / Senior Vice President / Wabash" on three lines, or "Jane Doe, Vice President, Intel". Offices and departments of the org don't count."""
    own = set(re.findall(r"[a-z0-9]+", fold(org))) - ORG_NOISE
    initials = "".join(t[0] for t in re.findall(r"[a-z0-9]+", fold(org)) if t not in ("the", "of", "and", "at", "for", "in"))
    rx = re.compile(name_regex(name).pattern, re.I)
    def foreign(label):
        label = label.strip(" ,.;:|-–—")
        words = label.split()
        if not 1 <= len(words) <= 5 or not words[0][0].isupper() or label.lower() == initials: return ""
        low = set(re.findall(r"[a-z0-9]+", fold(label)))
        if low and low <= POST_WORDS | ORG_NOISE | {"counsel"}: return ""  # the person's own unit or second post: "Technology Transfer", "Associate General Counsel"
        if _SCHOOL.search(fold(label)) and not names_org(org, label) and not low <= POST_WORDS: return label
        if not low or low & own or _ROLE_RE.search(label) or _NOT_EMPLOYER.search(label) or re.search(r"\d|@", label) or low <= NOT_NAME | ORG_NOISE: return ""
        return "" if plausible_name(label) and len(words) in (2, 3) and not re.search(r"\b(?:Inc|LLC|Ltd|Corp|Corporation|Company|Group|Bank|Partners)\b", label) else label
    lines = _page_lines(text)
    for i, line in enumerate(lines):
        m = rx.search(line)
        if not m: continue
        if _alone(line, m) and i + 2 < len(lines) and _ROLE_RE.search(lines[i + 1]) and foreign(lines[i + 2]): return lines[i + 2]
        parts = re.split(r"\s*(?:,|\s[|–—-]\s)\s*", re.sub(rf"^{SUFFIX}", "", line[m.end():]))
        parts = [p for p in parts if p.strip()]
        if len(parts) == 2 and _ROLE_RE.search(parts[0]) and foreign(parts[1]): return parts[1]
    return ""


def same_person(a, b):
    """Antonio Delgado and Antonio Delgado Fornaguera: same first name, and all of the shorter name is in the longer one."""
    x, y = ([t for t in fold(clean_name(n)).split() if len(t.strip(".")) > 1] for n in (a, b))
    if len(x) < 2 or len(y) < 2 or x[0] != y[0]: return False
    return set(x) <= set(y) or set(y) <= set(x)


BOILERPLATE = re.compile(r"live chat|powered by|was used for|ai[- ]generated|generated (?:by|with)|chatbot|cookies?\b|this site|this website", re.I)


_EVENT = re.compile(r"(?:\b[A-Z][\w&'-]*\s+){0,4}\b(?:Symposium|Conference|Summit|Forum|Workshop|Expo|Showcase|Festival|Webinar|Colloquium|Hackathon|Pitch Competition)\b(?:\s*[:\u2013-]\s*(?:[A-Z&][\w&'-]*\s*){1,8})?")
def without_events(text):
    """Event and programme titles ("RISE Symposium: Research Innovation & Scholarship Exchange") name no one's job: blank them before looking for a signal word."""
    return _EVENT.sub(lambda m: " " * len(m.group(0)), text)

def without_boilerplate(text):
    """The text with the site's own small print taken out: "This site's live chat uses AI" says nothing about anyone there."""
    return "".join(s for s in re.split(r"(?<=[.!?\n])", text) if not BOILERPLATE.search(s))


EVENT_URL = re.compile(r"summit|conference|speakers?\b|keynote|agenda|symposium|webinar", re.I)


def _spells(label, org):
    """Is the site label built from the name's words in order, a piece of each: ufl for University of Florida, but not usf?"""
    toks = re.findall(r"[a-z0-9]+", fold(org))
    optional = {"the", "of", "and", "at", "for", "in"}
    def walk(rest, i):
        if not rest: return all(t in optional for t in toks[i:])
        if i == len(toks): return False
        if toks[i] in optional and walk(rest, i + 1): return True
        return any(toks[i].startswith(rest[:n]) and walk(rest[n:], i + 1) for n in range(len(rest), 0, -1))
    return len(label) >= 3 and walk(label, 0)


_SCHOOL = re.compile(r"\b(?:colleges?|university|institute of technology)\b")


def _rival_school(toks, org, text):
    """miami.edu is spelled like Miami University but belongs to the University of Miami; only the page can tell. True if it names a differently-shaped school of that word more than this one."""
    core = " ".join(toks)
    low = fold(text)
    own = fold(org_core(org))
    if not _SCHOOL.search(own) or len(toks) != 1: return False
    forms = re.findall(rf"\b(?:{re.escape(core)} (?:college|university|state)|(?:university|college) of {re.escape(core)})\b", low)
    rival = sum(f != own for f in forms)
    return rival > sum(f == own for f in forms)


def _rival_initials(org, initials, text):
    """msu.edu fits Mississippi State University by its letters, but the site says Michigan State University. True if the page spells those letters as another name more often than as this one."""
    own = fold(org_core(org))
    joins = {"of", "the", "and", "at", "for", "in"}
    rival = mine = 0
    for run in re.findall(r"[A-Z][\w.&'-]*(?:\s+(?:(?:of|the|and|at|for|in)\s+)*[A-Z][\w.&'-]*)+", text):
        words = run.split()
        for i in range(len(words)):
            if words[i].lower() in joins: continue
            got, j = "", i
            while j < len(words) and len(got) < len(initials):
                if words[j].lower() not in joins: got += words[j][0].lower()
                j += 1
            if got != initials: continue
            if fold(" ".join(words[i:j])).rstrip(".,") == own: mine += 1
            else: rival += 1
    return rival > mine


def official(org, url, domains=None, text=None):
    """Is this page the organisation's own site? Known domains win; otherwise the site's name must spell the org and, where a word in it is common, sit on an institutional domain."""
    host = domain_of(url)
    if domains:
        d = (domains.get(org) or domains.get(org_core(org))) if isinstance(domains, dict) else domains
        for x in ([d] if isinstance(d, str) else d or []):
            if host == x or host.endswith("." + x): return True
        # The spec named this organisation's domains, so no guess may add another: tamucc.edu is not tamu.edu.
        if d: return False
    reg = registrable(host)
    label, tld = reg.split(".")[0], reg.split(".")[-1]
    all_toks = re.findall(r"[a-z0-9]+", fold(org_core(org)))
    toks = [t for t in all_toks if t not in ORG_NOISE or t == "state"]  # ohio.edu is not Ohio State
    if not toks or toks == ["state"]: return False
    initials = "".join(t[0] for t in all_toks if t not in ("the", "of", "and", "at", "for", "in"))
    institutional = tld in ("edu", "gov", "mil", "ca") or bool(re.search(r"\.(?:edu|ac|gov)\.[a-z]{2}$", reg))
    # duke.com is not Duke University, and acme.co.uk is not Acme CC: a school takes an institutional domain, or one named in the spec.
    # Nor does a school sit on an open gTLD anyone can buy (duke.university, harvard.college): those need the domain given.
    if _SCHOOL.search(fold(org_core(org))) and ((len(tld) > 2 and not institutional) or tld in ("co", "io") or ".co." in reg): return False
    if text and _rival_school(toks, org, text): return False
    joined = "".join(toks)
    if label == joined or label == "".join(all_toks) or label + tld == joined: return True  # lina.energy
    # echiontech.com for Echion Technologies: the name and the start of its last word, on a site that says the name.
    if text and len(toks) == 2 and len(label) >= len(toks[0]) + 3 and label.startswith(toks[0]) and toks[1].startswith(label[len(toks[0]):]) and names_org(org, text): return True
    if institutional:
        if label == initials or (len(initials) >= 3 and label.startswith(initials)):
            return bool(text) and names_org(org, text) and not _rival_initials(org, initials, text)
        rest = label
        for t in toks: rest = rest.replace(t, "", 1) if t in rest else "?" * 9
        # The name's words and at most a stub: valenciacollege, wakecc; floridatech is someone else.
        if label.startswith(joined) and rest in ORG_NOISE: return True
        return bool(text) and names_org(org, text) and _spells(label, org_core(org))
    return False


STALE_DAYS = 730


_LEFT = r"(?:will\s+|to\s+|has\s+|have\s+|had\s+|officially\s+|formally\s+)*(?:step(?:s|ped|ping)?\s+down|resign(?:s|ed|ing)?\b|retire[sd]?\b|retiring\b|departed\b|has\s+left\b|left\s+the\b|passed\s+the\s+torch|announc\w*\s+(?:her|his|their)\s+(?:retirement|resignation|departure)|until\s+(?:\w+\s+){0,2}(?:19|20)\d\d)"
_LEFT_AFTER = re.compile(r"(?P<between>[^.\n]{0,90}?)\b" + _LEFT, re.I)
_LEFT_TITLE = r"(?:(?:the|its|our)\s+)?(?:former|outgoing|departing|retiring|retired|previous|past|ex-)\s*"
_ROLE_WORDS = r"(?:[\w&'’-]+\s+){0,5}?"
_POST_IN = re.compile(r"\b(?:ceo|cto|coo|cfo|chief|president|director|chair\w*|founder|dean|head|manager|provost|chancellor|officer)\b")
_SUCCEEDS = r"(?:succeed(?:s|ed|ing)?|replac(?:es|ed|ing)|takes?\s+over\s+from|took\s+over\s+from|taking\s+over\s+from)\s+(?:\w+\s+){0,3}?"


def departed(text, name):
    """The words on this page saying this person has left or is leaving the post, or ''. 'Jane Doe, the CEO, stepped down', 'former CEO Jane Doe', 'succeeding Jane Doe'.
    A later page may say otherwise, so this holds a row back for a person to look at, nothing more."""
    low = fold(text)
    for m in name_regex(name).finditer(low):
        head, tail = low[max(0, m.start() - 70):m.start()], low[m.end():m.end() + 140]
        said = re.split(r"[.!?\n]", tail)[0]
        if re.search(r"\b(?:late|in memoriam|obituary|remembering)\b", head.rsplit("\n", 1)[-1]) or (re.search(r"\b(?:died|passed away)\b", said) and not re.search(r"\b(?:said|says|her|his|their|whose|after|when)\b", said)):
            return " ".join((head.rsplit("\n", 1)[-1] + low[m.start():m.end()] + said).split())[:200]
        after = _LEFT_AFTER.match(tail)
        # "Jane Doe succeeded Bob Roe, who stepped down" is about Bob.
        if after and not re.search(r"\b(?:after|succeed\w*|replac\w*|from|following|when|said|says|became|joined|appointed|named)\b", after.group("between")):
            return " ".join(text[m.start():m.end() + after.end()].split())[:200]
        if re.match(r"\s*,\s*" + _LEFT_TITLE, tail):
            return " ".join(text[m.start():m.end() + 60].split())[:200]
        line = head.rsplit("\n", 1)[-1]  # the line above is another matter: "Past Experience" over a name in a profile
        before = re.search(_LEFT_TITLE + "(" + _ROLE_WORDS + ")$", line)
        if (before and _POST_IN.search(before.group(1))) or re.search(_SUCCEEDS + "$", line):
            return " ".join(line.split()[-8:] + [name])[:200]
    return ""


def check(page, name, title, org="", domains=None, claim="", today=None):
    """Does this saved page back 'name holds title at org'? Returns {status, quote, note, ...}.
    verified: on a site that looks like the org's own, the role's words are attached to the name (same sentence or the lines under it, with no other person between). A pattern match, not a reading of the page, and a page saying they held a post doesn't show they hold it today. secondary: the same on another site. partial: the name is there and the role isn't, or the page says they've left.
    not-found: the page was read and has no such name. not-a-person: the name is only part of a named post or building. blocked: unread."""
    import datetime as dt
    res = {"status": "", "quote": "", "url": page.get("url", ""), "via": page.get("via", ""), "page_date": page.get("date", "") or "", "as_of": page.get("snapshot") or (page.get("fetched_at") or "")[:10], "official": False, "title_share": 0.0, "note": "", "claim_share": "", "stale": False}
    if page.get("status") != "ok":
        return dict(res, status="blocked" if page.get("status") != "refused" else "refused", note=page.get("note", ""))
    text = page["text"]
    res["official"] = bool(org) and official(org, page.get("final_url") or page["url"], domains, text)
    if len([t for t in fold(clean_name(name)).split() if len(t.strip(".")) > 1]) < 2: return dict(res, status="not-found", note="that isn't a full name, so there's nothing to check")
    if not name_regex(name).search(fold(text)): return dict(res, status="not-found", note="the page has no such name")
    if named_not_person(text, name): return dict(res, status="not-a-person", note="the name appears only as part of a named post or place")
    share, quote, other_rank = title_on(text, name, title, org)
    res.update(title_share=round(share, 2), quote=quote)
    notes = []
    if org and not names_org(org, text) and not res["official"]: notes.append("the page doesn't name the organisation")
    if res["via"] == "archive": notes.append(f"read from an archive copy dated {page.get('snapshot')}: confirm it still holds")
    today = today or dt.date.today()
    if res["page_date"]:
        try:
            if (today - dt.date.fromisoformat(res["page_date"])).days > STALE_DAYS: notes.append(f"the page is dated {res['page_date']}: may be out of date"); res["stale"] = True
        except ValueError: pass
    if claim:
        words = [w for w in _title_words(claim) if len(w) > 3]
        if words:
            low = fold(text)
            res["claim_share"] = round(sum(1 for w in words if w in low) / len(words), 2)
            if res["claim_share"] < 0.5: notes.append("the page doesn't mention most of the claim's words")
    if claim: notes.append("a free-form claim needs a reading of the source; word overlap does not verify it")
    left = departed(text, name)
    if left:
        res["left"] = left
        return dict(res, status="partial", note="; ".join([f"the page says they left or are leaving: {left}"] + notes))
    res["note"] = "; ".join(notes)
    if org:
        other = employer_after(text, name, org) or employer_before(text, name, org) or employer_listed(text, name, org) or elsewhere(title, org)
        if other:
            res["other_employer"] = other
            return dict(res, status="partial", note="; ".join([f"the page puts them at {other}, not {org}"] + notes))
    if other_rank: return dict(res, status="partial", note="; ".join([f"the page gives a different post: {other_rank}"] + notes))
    if share < 0.6 and not title_slot(text, name, title, org): return dict(res, status="partial", note="; ".join(["the name is on the page, the title isn't given as theirs"] + notes))
    slot = title_slot(text, name, title, org)
    if not slot: return dict(res, status="partial", note="; ".join(["no unambiguous name/title slot; read the quoted text"] + notes))
    # A page that is old, an archive copy, or silent on a claim can't stand as a held post today.
    if res["stale"] or res["via"] == "archive" or (claim and (res["claim_share"] == "" or res["claim_share"] < 0.5)): return dict(res, status="partial", note="; ".join(notes))
    res["quote"] = slot
    if org and not names_org(org, text) and not res["official"]: return dict(res, status="partial")
    if org and not res["official"] and not org_near(text, name, org): return dict(res, status="partial", note="; ".join(["the page names the organisation, but not near this person"] + notes))
    return dict(res, status="verified" if res["official"] else "secondary")


def sentences_with(text, terms, near=None, limit=300):
    """The first sentence with one of the terms, preferring one close to `near` (a compiled pattern on folded text)."""
    best = ""
    for s in re.split(r"(?<=[.!?])\s+|\n+", text):
        low = fold(s)
        if any(term_regex(t).search(low) for t in terms) and len(s) > 25:
            if near is None or near.search(low): return " ".join(s.split())[:limit]
            best = best or " ".join(s.split())[:limit]
    return best
