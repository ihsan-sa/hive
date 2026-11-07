"""Phone alerts: one line to the person's phone through ntfy when an agent has posted to them, even with the page closed.
Off until the person turns it on in Settings. This is an outbound call to a third party (ntfy.sh, no account), so what leaves is
deliberately small: a line saying something is waiting, the Hive's own address if it has one, and a long random topic that is the only
secret. Never a post's words, its author or its thread: anyone who learns the topic can read what is sent to it.
No imports from dash.py: the page reads and writes the state through the functions here, and tests run it with a fake send."""
from datetime import datetime, timezone
import json, os, secrets, tempfile, time, urllib.request
from pathlib import Path

SERVER = "https://ntfy.sh"
LINE = "Your Hive has something waiting for you."
RUN_LINE = "A run you started has finished."
GRACE = 20       # seconds a post must go unread before it is worth a ping: someone looking at the page has already seen it
GAP = 60         # seconds between pings; a burst of posts is one ping
CLICK_OK = ("http://", "https://")


def _now(): return time.time()


def _epoch(at):
    """A board post's time (ISO, UTC unless it says otherwise) as seconds; unreadable counts as old enough to ping."""
    try:
        d = datetime.fromisoformat(str(at))
        return (d if d.tzinfo else d.replace(tzinfo=timezone.utc)).timestamp()
    except ValueError: return 0.0


def http_send(url, headers, body, timeout=15):
    req = urllib.request.Request(url, data=body, headers=headers, method="POST")
    with urllib.request.urlopen(req, timeout=timeout) as r: r.read()


class PhoneAlert:
    def __init__(self, path, send=http_send, now=_now):
        self.path, self.send, self.now = Path(path), send, now
        self.last_sent = 0.0

    # --- the state file ---
    def _read(self):
        try: v = json.loads(self.path.read_text())
        except (OSError, ValueError): return {}
        return v if isinstance(v, dict) else {}

    def _write(self, v):
        self.path.parent.mkdir(parents=True, exist_ok=True)
        t = self.path.with_name(f".{self.path.name}.{os.getpid()}")
        fd = os.open(t, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "w") as f: f.write(json.dumps(v, indent=1) + "\n")
        os.replace(t, self.path)

    def view(self):
        """What Settings shows. The topic is shown only while on: it is the only secret."""
        v = self._read(); on = v.get("on") is True and bool(v.get("topic"))
        return {"on": on, "server": SERVER, "line": LINE, "topic": v.get("topic") if on else None,
                "subscribe": f"{SERVER}/{v['topic']}" if on else None, "runs": v.get("runs") is True, "link": v.get("link") or None, "error": v.get("error") or None}

    # --- switches ---
    def set_on(self, on, link=None, latest=""):
        """latest: the `at` of the newest post already waiting, so turning it on doesn't ping for what was there."""
        v = self._read()
        if on:
            v.setdefault("topic", "hive-" + secrets.token_urlsafe(18))
            v["seen"] = max(str(v.get("seen") or ""), str(latest or ""))
            if link is not None: v["link"] = link if link.startswith(CLICK_OK) else ""
        v["on"] = bool(on); v.pop("error", None)
        self._write(v); return self.view()

    def new_topic(self, latest=""):
        v = self._read()
        if v.get("on") is not True: raise ValueError("turn phone alerts on first")
        v["topic"] = "hive-" + secrets.token_urlsafe(18); v["seen"] = max(str(v.get("seen") or ""), str(latest or "")); v.pop("error", None)
        self._write(v); return self.view()

    def set_runs(self, on, latest=""):
        """Also ping when a run the person started ends. Off until asked for; latest = the newest end already in the log."""
        v = self._read()
        if on is True: v["runs_seen"] = max(str(v.get("runs_seen") or ""), str(latest or ""))
        v["runs"] = on is True
        self._write(v); return self.view()

    def tick_runs(self, ends):
        """ends: [{at}] for runs that the person started and that have ended. One ping per burst, nothing for ends before it was turned on."""
        v = self._read()
        if v.get("on") is not True or not v.get("topic") or v.get("runs") is not True: return False
        seen = str(v.get("runs_seen") or "")
        new = [e for e in ends if str(e.get("at") or "") > seen]
        if not new or self.now() - self.last_sent < GAP: return False
        v["runs_seen"] = max(str(e["at"]) for e in new)
        self.last_sent = self.now()
        try: self._push(v, "Hive", RUN_LINE); v.pop("error", None)
        except Exception as e: v["error"] = "Couldn't reach ntfy: " + str(e)[:120]
        self._write(v); return "error" not in v

    # --- sending ---
    def _push(self, v, title, line=LINE):
        h = {"Title": title, "Priority": "3", "Tags": "bee"}
        if v.get("link"): h["Click"] = v["link"]
        self.send(f"{SERVER}/{v['topic']}", h, line.encode())

    def test(self):
        v = self._read()
        if v.get("on") is not True or not v.get("topic"): raise ValueError("turn phone alerts on first")
        try: self._push(v, "Hive test")
        except Exception as e: raise ValueError("Couldn't reach ntfy: " + str(e)[:120])
        return {"sent": LINE}

    def tick(self, mine, read):
        """Called every few seconds with the posts for the person ({id, at}) and the ids already read. Returns whether it pinged."""
        v = self._read()
        if v.get("on") is not True or not v.get("topic"): return False
        seen = str(v.get("seen") or "")
        new = [p for p in mine if str(p.get("at") or "") > seen and p.get("id") not in read]
        if not new: return False
        hot = [p for p in new if is_budget_stop(p)]   # the person must act on it: not spaced out, not held for the grace
        if hot: due = hot
        elif self.now() - self.last_sent < GAP: return False
        else:
            due = [p for p in new if _epoch(p.get("at")) <= self.now() - GRACE]
            if not due: return False
        v["seen"] = max(str(p["at"]) for p in due)   # what came after stays for the next ping
        self.last_sent = self.now()   # a failure waits out the gap too
        try: self._push(v, "Hive"); v.pop("error", None)
        except Exception as e: v["error"] = "Couldn't reach ntfy: " + str(e)[:120]
        self._write(v); return "error" not in v


def is_budget_stop(p):
    """The starter's post that a run reached its budget or the daily cap held new starts (core/starter.py gave_up_text, daily_cap_holds)."""
    t = str(p.get("text") or "")
    return p.get("from") == "starter" and ("reached its budget" in t or "reached your daily cap" in t)


def person_run_ends(events):
    """From log events (oldest first): [{at, name}] for each run whose start came from a post by the person and that has since ended."""
    mine, out = set(), []
    for e in events:
        if e.get("event") == "start" and e.get("from") == "person": mine.add((e.get("name"), e.get("session")))
        elif e.get("event") == "end" and (e.get("name"), e.get("session")) in mine: out.append({"at": e.get("at"), "name": e.get("name")})
    return out
