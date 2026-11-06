#!/usr/bin/env python3
"""phone: talk to Hive by voice. Speech recognition (faster-whisper) and the voice (Kokoro) run on this machine: on the GPU
when it has one with CUDA, else on the CPU with a smaller recognition model.

  phone setup                      build the speech environment, download the models (~1.5 GB), start hive-phone.service

  phone serve                      the call service on ~/.hive/phone.sock (hive-phone.service); the dash proxies /api/phone/*
  phone answer CALL                an agent called by name picks up the call itself (the board post says so)
  phone talk CALL TEXT             say TEXT on the call, then wait for the person's reply and print it
  phone listen CALL                wait for the person's next words without speaking
  phone hangup CALL [TEXT]         say goodbye and end the call
  phone transfer AGENT [--why W]   from inside a call: hold music, then AGENT is summoned
  phone ring [--why W]             from any agent: ring the person; when they answer, you're summoned to pick up
  phone calls                      what's live
  phone enroll [FILE...]           learn the person's voice from saved call turns and memos, so a nearby talker gets muted
  phone say TEXT [-o FILE]         speak TEXT to a wav file, as a test of the voice

Calling Hive starts a new agent in a new thread for the call, like any other agent (it shows in the agent view and the
thread list). A transfer back to Hive gets the receptionist: a brain, `claude -p` in stream-json mode, speaking sentence by
sentence as its text streams. Calling an agent (or a transfer, or an answered ring) reaches that agent itself: a board post from `phone`
wakes or interrupts it, it runs `phone answer` and talks from its own session while its other work waits, and the
person hears hold music meanwhile. If it hasn't picked up in SUMMON_S the person hears "still here"; after SUMMON_LONG_S the phone leaves a board note and ends the call. No copy of the agent ever answers. Calling a
thread starts a new agent on it the same way. The transcript goes on the board when the call ends.
"""
import base64, io, json, os, re, shutil, subprocess, sys, threading, time, uuid, wave
import http.client, socket, socketserver
from http.server import BaseHTTPRequestHandler
from pathlib import Path
from urllib.parse import urlparse, parse_qs

HOME = Path.home()
MOD = Path(__file__).resolve().parent
LOG = HOME / "log.jsonl"
STATE = HOME / ".hive"
# A Unix socket in this account's own state dir, not a loopback port: any account on the machine can open loopback, and
# a call here can put words in the person's mouth to an agent. Only this account can open the socket.
SOCK = Path(os.environ.get("PHONE_SOCK") or STATE / "phone.sock")
VOICE = os.environ.get("PHONE_VOICE", "af_heart")
BRAIN_MODEL = os.environ.get("PHONE_MODEL", "sonnet")
# A sentence, not a word list: a bare list of terms as the prompt makes Whisper cut speech short ("Hi, there.").
RING_TTL = 600
SUMMON_S = int(os.environ.get("PHONE_SUMMON_S", "20"))   # then the person hears "still here"; no copy ever answers a named agent's call
SUMMON_LONG_S = int(os.environ.get("PHONE_SUMMON_LONG_S", "240"))   # then the phone says nobody came, leaves a board note and ends the call
FILLER_S = 6                                            # the agent on the line says "one sec" after this much silence
RECEPTION_NAMES = ("hive", "phone", "reception")

SPEAK_RULES = """You are on a live voice call with the person who runs the Hive. Everything you write is spoken aloud by a
text-to-speech voice, sentence by sentence, as you write it. So:
- Talk like a person on the phone: short, warm, natural sentences. One to three sentences a turn unless asked for more.
- No markdown, lists, code, file paths, ids, URLs or symbols. Say numbers and names the way you'd say them aloud.
- Never read out command output. Sum it up in a sentence.
- Don't announce tool use or add filler ("let me check", "one sec", "sure thing"): run the tool silently, then say what you found.
- What you hear comes from speech recognition and may have small errors. Read a word that sounds alike by what fits the
  conversation and the Hive's own terms (a queue, not a cue; Claude, not clawed). Someone nearby may get transcribed too: if part of a
  turn doesn't fit, don't act on it. Guess sensibly, ask only if it matters.
- You have your full tools (Bash, hive, kb, files). Small things you can do during the call; for real work, post a
  brief on the board (hive post, quoted heredoc) to the agent who owns it or to a new one, and tell the person you did.
- Keep turns quick: anything you do mid-call (a post, asking another agent on the board and waiting for its answer,
  a short script) should take under a minute. One voice per call: bring other agents in by asking them and relaying.
- `phone transfer NAME --why "..."` puts the person through to another agent (they hear hold music, then that agent
  picks up). Use `hive who`, `hive search` and `hive read` to find who is working on something. After you run it, say
  nothing more: your part of the call is over.
- When the person says goodbye, say a short goodbye back.
"""

RECEPTION = SPEAK_RULES + """
You are Hive itself answering the phone: the front desk of the Hive. You can answer questions about what's going on
(hive read --since 2h, hive who, hive asks list, kb find), take requests and put them on the board, or patch the person
through to whoever is working on something. Start by greeting them in a few words, e.g. "Hey, it's the Hive. What's up?"
"""

# --------------------------------------------------------------------------------------------------- speech models

_stt = _tts = _hint = None
_beam = 5   # finals get beam search on a GPU; on the CPU greedy decoding, three times faster
_mlock = threading.Lock()


def _preload_cuda():
    """ctranslate2 wants cuBLAS 12 and cuDNN 9; onnxruntime-gpu brings its own. Load the cu12 wheels first, globally."""
    import ctypes, glob
    root = next(iter((MOD / ".venv" / "lib").glob("python*/site-packages/nvidia")), None)
    if not root: return
    libs = sorted(glob.glob(str(root / "cublas/lib/libcublas*.so.12")), reverse=True) + glob.glob(str(root / "cudnn/lib/libcudnn*.so.9"))
    for p in libs:
        try: ctypes.CDLL(p, mode=ctypes.RTLD_GLOBAL)
        except OSError: pass


STT_GPU, STT_CPU = "faster-whisper-large-v3-turbo", "faster-distil-whisper-small.en"
STT_OLD = "faster-distil-whisper-medium.en"   # the GPU model before turbo; still used if it's what is here
STT_REPO = {STT_GPU: "mobiuslabsgmbh/faster-whisper-large-v3-turbo", STT_CPU: f"Systran/{STT_CPU}"}
# Words recognition gets wrong without a hint. Passed as hotwords, which apply to every 30 s window; initial_prompt with
# condition_on_previous_text=False reaches only the first. Distil models can't take either: they return nothing.
STT_HINT = os.environ.get("PHONE_STT_HINT", "Hive, Hive, the board, warden, rootwarden, keeper, Claude, Claude Code, Codex, Fable, Sonnet, Opus, Haiku, Whisper, Kokoro, GPU, PCB, FPGA, SPICE, Verilog, PLL, BOM, DRC, LVS, KiCad, Gerbers, JLCPCB, Bambu, Tiny Tapeout, Digi-Key, Telnyx, OpenRouter, GitHub, kb.")
SPK_URL = "https://github.com/k2-fsa/sherpa-onnx/releases/download/speaker-recongition-models/3dspeaker_speech_campplus_sv_en_voxceleb_16k.onnx"
KOKORO = "https://github.com/thewh1teagle/kokoro-onnx/releases/download/model-files-v1.0/"


def cuda():
    """True when ctranslate2 sees a CUDA GPU (PHONE_DEVICE=cpu forces the CPU)."""
    if os.environ.get("PHONE_DEVICE") == "cpu": return False
    try:
        import ctranslate2
        return ctranslate2.get_cuda_device_count() > 0
    except Exception: return False


def stt_dir(gpu):
    """The recognition model to load: PHONE_STT_MODEL, else turbo on a GPU and small distil on the CPU, else whichever is here."""
    names = [os.environ.get("PHONE_STT_MODEL")] + ([STT_GPU, STT_OLD, STT_CPU] if gpu else [STT_CPU, STT_OLD, STT_GPU])
    for n in filter(None, names):
        if (MOD / "models" / n).is_dir(): return MOD / "models" / n
    raise SystemExit(f"phone: no speech model in {MOD / 'models'}; run: phone setup")


def setup():
    """Build .venv, download the models, then install and start the service. Safe to rerun."""
    gpu = bool(shutil.which("nvidia-smi")) and subprocess.run(["nvidia-smi", "-L"], capture_output=True).returncode == 0
    venv, py = MOD / ".venv", MOD / ".venv/bin/python"
    uv = shutil.which("uv")
    def run(argv): print("+", " ".join(argv), flush=True); subprocess.run(argv, check=True)
    if not py.exists(): run([uv, "venv", "-q", str(venv)] if uv else [sys.executable, "-m", "venv", str(venv)])
    pkgs = ["faster-whisper", "huggingface_hub", "numpy"] + (["kokoro-onnx[gpu]", "nvidia-cublas-cu12", "nvidia-cudnn-cu12==9.*"] if gpu else ["kokoro-onnx"])
    run([uv, "pip", "install", "-q", "--python", str(py)] + pkgs if uv else [str(py), "-m", "pip", "install", "-q"] + pkgs)
    m = MOD / "models"; m.mkdir(exist_ok=True)
    for f in ("kokoro-v1.0.onnx", "voices-v1.0.bin"):
        if not (m / f).exists(): run(["curl", "-fsSL", "--retry", "3", "-o", str(m / f) + ".part", KOKORO + f]); (m / (f + ".part")).rename(m / f)
    if not (m / "speaker-campplus.onnx").exists():
        run(["curl", "-fsSL", "--retry", "3", "-o", str(m / "speaker-campplus.onnx.part"), SPK_URL]); (m / "speaker-campplus.onnx.part").rename(m / "speaker-campplus.onnx")
    for n in ([STT_GPU] if gpu else [STT_CPU]):
        if not (m / n).is_dir(): run([str(py), "-c", f"from huggingface_hub import snapshot_download as d; d('{STT_REPO[n]}', local_dir={str(m / (n + '.part'))!r})"]); (m / (n + ".part")).rename(m / n)
    print(f"phone: speech ready for the {'GPU' if gpu else 'CPU'}", flush=True)
    unit = MOD / "hive-phone.service"
    if not shutil.which("systemctl"): print(f"No systemd here: run {py} {MOD / 'phone.py'} serve yourself, and keep it running."); return
    u = HOME / ".config/systemd/user"; u.mkdir(parents=True, exist_ok=True)
    (u / unit.name).unlink(missing_ok=True); (u / unit.name).symlink_to(unit)
    dash = u / "hive-dash.service.d/local.conf"   # the install's PATH (where claude is) and env file, as the other services get them
    if dash.exists(): (u / f"{unit.name}.d").mkdir(exist_ok=True); shutil.copy(dash, u / f"{unit.name}.d/local.conf")
    run(["systemctl", "--user", "daemon-reload"]); run(["systemctl", "--user", "enable", "--now", unit.name]); run(["systemctl", "--user", "restart", unit.name])
    print("The Call button shows in the dashboard once the models have loaded (under a minute; reload the page).")


def enroll(paths=None):
    """Learn the person's voice from their own audio, one saved call turn or voice memo at a time; a clip that doesn't match the
    rest is left out. Writes speaker.ENROL with one voice per channel (a phone line changes how a voice scores). Stops with no
    file written if there's too little to go on."""
    import numpy as np, speaker, telnyx
    _preload_cuda()
    if not paths:
        paths = sorted(Path(os.environ.get("TMPDIR", "/tmp"), "phone-turns").glob("*.pcm")) + sorted((HOME / "files").glob("*/voice-*"))
    raw = {"browser": [], "line": []}; names = []
    for p in map(Path, paths):
        if p.suffix == ".pcm": a = np.fromfile(p, dtype="<i2").astype("float32") / 32768
        else:
            r = subprocess.run(["ffmpeg", "-v", "error", "-i", str(p), "-f", "f32le", "-ac", "1", "-ar", "16000", "-"], capture_output=True)
            a = np.frombuffer(r.stdout, dtype="float32")
        if len(a) < 16000 * 3 or np.sqrt((a ** 2).mean()) < 0.01: continue
        a = a[:16000 * 60]
        raw["browser"].append(speaker.embed(a)); raw["line"].append(speaker.embed(telnyx.over_line(a))); names.append(p.name)
    if len(names) < 8: raise SystemExit(f"phone: only {len(names)} usable clips of 3 s or more; talk on a few more calls, or name the files")
    out = {}
    for ch, e in raw.items():
        out[ch], kept = speaker.centroid(e)
        print(f"{ch}: {len(kept)} of {len(e)} clips agree, median score {np.median(np.stack(e)[kept] @ out[ch]):.2f}; left out: {[names[i] for i in range(len(e)) if i not in kept]}")
    speaker.ENROL.parent.mkdir(parents=True, exist_ok=True)
    with open(speaker.ENROL, "wb") as f: np.savez(f, **out)
    print(f"wrote {speaker.ENROL}. To pick it up: systemctl --user restart hive-phone hive-voice, once `phone calls` shows nothing live.")


def models():
    global _stt, _tts, _beam, _hint
    with _mlock:
        if _tts is None:
            _preload_cuda()
            import onnxruntime as ort
            from kokoro_onnx import Kokoro
            from faster_whisper import WhisperModel
            try: ort.preload_dlls()
            except Exception: pass
            s = ort.InferenceSession(str(MOD / "models/kokoro-v1.0.onnx"), providers=["CUDAExecutionProvider", "CPUExecutionProvider"])
            _tts = Kokoro.from_session(s, str(MOD / "models/voices-v1.0.bin"))
            gpu = cuda(); _beam = 5 if gpu else 1
            d = stt_dir(gpu); _hint = None if "distil" in d.name else STT_HINT
            _stt = WhisperModel(str(d), device="cuda" if gpu else "cpu", compute_type="float16" if gpu else "int8")
            _tts.create("Ready.", voice=VOICE, speed=1.0, lang="en-us")  # the first run on the GPU is slow; do it now, not on the call
    return _stt, _tts


def tts_wav(text, voice=None):
    """Text to 24 kHz mono 16-bit wav bytes."""
    import numpy as np
    _, tts = models()
    with _mlock:
        samples, sr = tts.create(text, voice=voice or VOICE, speed=1.05, lang="en-us")
    pcm = (np.clip(samples, -1, 1) * 32767).astype("<i2").tobytes()
    b = io.BytesIO()
    with wave.open(b, "wb") as w:
        w.setnchannels(1); w.setsampwidth(2); w.setframerate(sr); w.writeframes(pcm)
    return b.getvalue()


PRE = os.environ.get("PHONE_PRE", "gate+norm")   # which clean-up stt() runs first; eval/eval.py scores the choices


def _stft(a, n=512, hop=128):
    import numpy as np
    w = np.hanning(n).astype("float32"); a = np.pad(a, (n, n))
    idx = np.arange(0, len(a) - n, hop)[:, None] + np.arange(n)[None]
    return np.fft.rfft(a[idx] * w, axis=1), w, n, hop


def isolate(a, mode):
    """float32 16 kHz speech with room noise, hum and rumble in it -> the same with those turned down. mode is a '+' list of:
    hp (high-pass 90 Hz), gate (spectral subtraction with a Wiener-style floor, noise learned from the quietest frames of this clip),
    norm (loudness to a steady level, never boosting silence), base (nothing)."""
    import numpy as np
    for m in mode.split("+"):
        if m == "hp":
            f = np.fft.rfft(a); fr = np.fft.rfftfreq(len(a), 1 / 16000)
            f *= np.clip((fr - 60) / 40, 0, 1) ** 2; a = np.fft.irfft(f, len(a)).astype("float32")
        elif m == "gate" and len(a) > 4096:
            S, w, n, hop = _stft(a); mag = np.abs(S); pw = mag ** 2
            e = pw.sum(1); q = e <= np.quantile(e, 0.15)               # the quietest frames are the room
            noise = np.sqrt(pw[q].mean(0)) + 1e-9
            snr = np.maximum(mag ** 2 - (1.5 * noise) ** 2, 0) / (mag ** 2 + 1e-12)   # per-bin Wiener-like gain
            g = np.maximum(snr, 0.12) ** 0.7
            g = np.apply_along_axis(lambda r: np.convolve(r, np.ones(3) / 3, "same"), 1, g)   # smooth across frequency: less musical noise
            Y = S * g
            out = np.zeros(len(a) + 2 * n, "float32"); wsum = np.zeros_like(out)
            fr_ = np.fft.irfft(Y, axis=1).astype("float32") * w
            for i in range(len(Y)): out[i * hop:i * hop + n] += fr_[i]; wsum[i * hop:i * hop + n] += w ** 2
            a = (out / np.maximum(wsum, 1e-6))[n:n + len(a)]
        elif m == "norm":
            fl = 512; k = len(a) // fl
            if k:
                r = np.sqrt((a[: k * fl].reshape(k, fl) ** 2).mean(1)); loud = r[r > max(np.quantile(r, 0.5), 1e-4)]
                if len(loud):
                    gain = float(np.clip(0.08 / (np.median(loud) + 1e-9), 0.5, 12)); a = np.clip(a * gain, -1, 1)
    return a.astype("float32")


def keep_turn(cid, pcm, tag=""):
    """Empty, long and partly muted turns stay on this machine to look at and to enrol from, the newest 300."""
    d = Path(os.environ.get("TMPDIR", "/tmp"), "phone-turns"); d.mkdir(parents=True, exist_ok=True)
    (d / f"{tag}{cid}-{int(time.time())}.pcm").write_bytes(pcm)
    for f in sorted(d.glob("*.pcm"), key=lambda f: f.stat().st_mtime)[:-300]: f.unlink(missing_ok=True)


_rejects = {}   # channel: turns in a row that sounded like nobody enrolled
_matched = {}   # channel: when a turn last had some of the enrolled voice in it
STALE = 180     # seconds without one match before rejects start failing open


def voice_gain(a, channel, info, now=None):
    """The mute mask for a final turn (see speaker.py), or None to keep all of it. This fails open: any error, no enrolment, or
    a voice that has stopped matching (say a cold or a new mic) keeps the audio. Stopped matching means two whole turns in a row
    rejected and no turn with the person in it for STALE seconds: while their voice still matches now and then, rejected turns
    are someone else, and a room with other people talking gives runs of those (the 2026-11-05 calls let them through)."""
    now = time.time() if now is None else now
    try:
        import speaker
        ref = speaker.refs().get(channel)
        if ref is None: return None
        g, kept = speaker.mask(a, ref)
        info["kept"] = round(kept, 2)
        if g is None or kept > 0:
            _rejects[channel] = 0
            if g is not None: _matched[channel] = now
            return g
        _rejects[channel] = _rejects.get(channel, 0) + 1
        if _rejects[channel] <= 2 or now - _matched.get(channel, 0) < STALE: info["not_person"] = True
        return None
    except Exception as e:
        print(f"phone: speaker check failed, keeping the audio: {e}", flush=True); return None


_yielded = 0.0
def _yield_gpu(secs=120):
    """Ask the kb reranker (if running) to leave the GPU before we use it. Every error ignored; at most one ask per 10 s."""
    global _yielded
    try:
        if time.time() - _yielded < 10: return
        import socket, json
        p = os.path.expanduser("~/.cache/kb-rerank/sock")
        if not os.path.exists(p): return
        _yielded = time.time()
        s = socket.socket(socket.AF_UNIX); s.settimeout(0.2)
        s.connect(p); s.sendall((json.dumps({"yield": secs}) + "\n").encode()); s.recv(100); s.close()
    except Exception: pass


def stt(pcm16k, partial=False, channel="browser", info=None):
    """16 kHz mono int16 little-endian bytes to text. Drops what's most likely noise, not speech.
    partial: a look at speech still going on, for showing it live; gives None rather than wait behind another transcription.
    channel: browser or line, which enrolled voice to compare a final with. info, a dict, gets kept (fraction of the turn that
    was the enrolled voice) and not_person (none of it was: the text is "" and what was heard goes to the log)."""
    import numpy as np
    info = {} if info is None else info
    a = np.frombuffer(pcm16k, dtype="<i2").astype("float32") / 32768
    if len(a) < 16000 * 0.25: return ""
    _yield_gpu()
    st, _ = models()
    if partial:   # the live look is masked too, so a nearby talker doesn't flash up as the person's words
        try:
            import speaker
            ref = speaker.refs().get(channel)
            g = speaker.mask(a, ref)[0] if ref is not None else None
        except Exception: g = None
    else: g = voice_gain(a, channel, info)
    if PRE != "base": a = isolate(a, PRE)   # the mask comes after: isolate learns the room from the quietest frames, and muted ones would fool it
    if g is not None: a = a * g
    if not _mlock.acquire(blocking=not partial): return None
    try:
        # _hint is None on distil-whisper: with a prompt it gave no segments at all for 22 of 51 chunks of real speech
        # (the "long turns come back empty" bug); without it, 0 of 136, clean or choppy. Finals get beam search.
        segs, _ = st.transcribe(a, language="en", beam_size=1 if partial else _beam, vad_filter=True, condition_on_previous_text=False, hotwords=_hint)
        segs = list(segs)
    finally: _mlock.release()
    # Whisper's own rule: a segment is silence only when it is both likely no-speech and decoded with low confidence.
    # Stricter than that on the usual hallucination signs: a segment decoded with very low confidence, one that is repetitive
    # (compression ratio), or one the model half-thinks is silence and is not confident about.
    def _ok(s):
        if s.no_speech_prob > 0.6 and s.avg_logprob < -1.0: return False
        if s.avg_logprob < -1.4 or getattr(s, "compression_ratio", 0) > 2.4: return False
        if s.no_speech_prob > 0.4 and s.avg_logprob < -0.8: return False
        return True
    text = " ".join(s.text.strip() for s in segs if _ok(s)).strip()
    if re.search(r"(?i)\b(\w+(?:\W+\w+){0,3}?)(?:\W+\1\b){3,}", text): return ""   # the same few words over and over: a loop, not speech
    if re.fullmatch(r"(?i)\W*(thank you|thanks for watching|thanks for listening|please subscribe|subtitles by.*|you|bye|bye-bye|okay|ok|yeah|oh|uh|um|hmm|mm|\.+)\W*", text) and len(a) < 16000 * 1.5: return ""  # whisper's usual noise words
    if info.get("not_person"):
        print(f"phone: {channel} turn {len(a) / 16000:.1f}s is not the enrolled voice, dropped {text!r}", flush=True); return ""
    if info.get("kept", 1) < 1: print(f"phone: {channel} turn {len(a) / 16000:.1f}s kept {info['kept']:.0%} as the enrolled voice", flush=True)
    return text

# --------------------------------------------------------------------------------------------------- agents


def last_start(name):
    """The newest start of agent NAME in log.jsonl: {session, login, thread...}, or None."""
    found = None
    try:
        with open(LOG, "rb") as f:
            for line in f:
                if name.encode() not in line: continue
                try: e = json.loads(line)
                except ValueError: continue
                if e.get("name") == name and e.get("session") and e.get("event") == "start":
                    found = e
    except OSError: pass
    return found


def best_login():
    """The login with the most weekly room (as the starter reads it)."""
    try: u = json.loads((STATE / "usage.json").read_text())
    except (OSError, ValueError): u = {}
    ok = [(v if isinstance(v, (int, float)) else 0, k) for k, v in u.items() if k != "_checked" and (v is None or v < 95)]
    return str(Path(min(ok)[1]).expanduser()) if ok else os.environ.get("CLAUDE_CONFIG_DIR", str(HOME / ".claude"))


def login_usage(login):
    try: u = json.loads((STATE / "usage.json").read_text())
    except (OSError, ValueError): return 0
    v = u.get(login) or u.get(login.replace(str(HOME), "~"))
    return v if isinstance(v, (int, float)) else 0


def copy_session(sid, src, dst):
    for f in Path(src).glob(f"projects/*/{sid}.jsonl"):
        to = Path(dst) / f.relative_to(src); to.parent.mkdir(parents=True, exist_ok=True); shutil.copy2(f, to)
        return True
    return False


def thread_of(name):
    """The thread agent NAME last worked in."""
    st = last_start(name)
    return (st or {}).get("thread")


# --------------------------------------------------------------------------------------------------- calls

CALLS, RINGS = {}, {}
_glock = threading.Lock()
SENT_END = re.compile(r"(?<=[.!?])[\"')\]]?\s+|\n+")


def speakable(t):
    t = re.sub(r"```.*?```", " ", t, flags=re.S)
    t = re.sub(r"`([^`]*)`", r"\1", t)
    t = re.sub(r"https?://\S+", "a link", t)
    t = re.sub(r"[*_#>|~]+", "", t)
    t = re.sub(r"\[([^\]]*)\]\([^)]*\)", r"\1", t)
    return re.sub(r"\s+", " ", t).strip()


class Brain:
    """One claude -p process in stream-json mode. Speaks via call.say(); calls call.turn_done() at each result."""

    def __init__(self, call, name, prompt, resume=None, login=None, first=None):
        self.call, self.name, self.dead = call, name, False
        login = login or best_login()
        argv = ["claude", "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
                "--include-partial-messages", "--model", BRAIN_MODEL, "--dangerously-skip-permissions", "--append-system-prompt", prompt]
        if resume: argv += ["--resume", resume, "--fork-session"]
        env = {**os.environ, "CLAUDE_CONFIG_DIR": login, "PHONE_CALL": call.id, "HIVE_NAME": name,
               "TMPDIR": os.environ.get("TMPDIR", str(HOME / ".cache/hive-tmp"))}
        self.p = subprocess.Popen(argv, cwd=str(HOME), env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                  stderr=subprocess.PIPE, text=True, bufsize=1)
        threading.Thread(target=self._read, daemon=True).start()
        threading.Thread(target=self._err, daemon=True).start()
        if first: self.send(first)

    def send(self, text):
        if self.dead: return
        try:
            self.p.stdin.write(json.dumps({"type": "user", "message": {"role": "user", "content": text}}) + "\n"); self.p.stdin.flush()
        except (OSError, ValueError): self.dead = True

    def _err(self):
        for line in self.p.stderr: self.call.note(f"brain stderr: {line.strip()[:300]}")

    def _read(self):
        buf, spoke = "", False
        for line in self.p.stdout:
            try: m = json.loads(line)
            except ValueError: continue
            t = m.get("type")
            if t == "stream_event":
                ev = m.get("event") or {}
                if ev.get("type") == "content_block_delta" and (ev.get("delta") or {}).get("type") == "text_delta":
                    buf += ev["delta"].get("text", "")
                    parts = SENT_END.split(buf)
                    for s in parts[:-1]:
                        if speakable(s): self.call.say(speakable(s), self); spoke = True
                    buf = parts[-1]
                elif ev.get("type") == "content_block_start" and (ev.get("content_block") or {}).get("type") == "tool_use":
                    if speakable(buf): self.call.say(speakable(buf), self); spoke = True
                    buf = ""
                    if not spoke: self.call.filler(self)
                    self.call.thinking(True)
                elif ev.get("type") == "message_start":
                    self.call.thinking(False)
            elif t == "result":
                if speakable(buf): self.call.say(speakable(buf), self)
                buf, spoke = "", False
                self.call.turn_done(self)
        self.dead = True
        self.call.brain_exit(self)

    def stop(self):
        self.dead = True
        try: self.p.stdin.close()
        except OSError: pass
        try: self.p.terminate(); self.p.wait(5)
        except Exception:
            try: self.p.kill()
            except Exception: pass


class AgentLine:
    """The agent itself on the line, talking through `phone talk` from its own session. Stands where a Brain would:
    what the person says queues here until the agent's next `phone talk` or `phone listen` collects it."""

    def __init__(self, call, name):
        self.call, self.name, self.dead, self.heard, self.spoke = call, name, False, [], time.time()

    def send(self, text):
        with self.call.cv:
            self.heard.append(text); self.call.cv.notify_all()
        t0 = time.time()
        def nudge():  # an agent with a big context can take a while to reply: one filler, so the line isn't dead air
            if not self.dead and self.call.brain is self and self.spoke < t0 and self.heard == []: self.call.filler(self)
        threading.Timer(FILLER_S, nudge).start()

    def take(self, timeout):
        """What the person said since the last take: waits up to TIMEOUT for something, the end of the call, or a transfer away."""
        end = time.time() + timeout
        with self.call.cv:
            while not self.heard and not self.dead and not self.call.ended and not self.call.muted and self.call.brain is self and time.time() < end:
                self.call.cv.wait(max(0.1, end - time.time()))
            out, self.heard = self.heard, []
            return out

    def stop(self):
        self.dead = True
        with self.call.cv: self.call.cv.notify_all()


class Call:
    def __init__(self, who="person", ring=None):
        self.id = uuid.uuid4().hex[:10]
        self.events, self.cv = [], threading.Condition()
        self.lines = []          # transcript: (speaker, text)
        self.brain = None
        self.agent = "Hive"        # who is on the line
        self.thread = None       # the thread the call belongs to, for showing it live there
        self.partial = ""        # what the person is saying right now, before their turn ends
        self.state = "connecting"
        self.muted = False       # the person muted their mic: agents keep working instead of waiting for words
        self.started, self.last = time.time(), time.time()
        self.busy_since = None   # a turn is out to the brain since then
        self.pending = None      # a transfer waiting for the current turn to finish
        self.saying = threading.Lock()  # a sentence in TTS goes out before a transfer's hold, so the hold can't cut it off
        self.ring = ring
        self.notes = []
        self.ended = False
        self.quiet = False       # a test call: no transcript on the board
        self.leaving = None      # the brain that ran `phone transfer`: muted from then on
        self.summoned = None     # (name, at): the agent asked to pick up itself

    # events --------------------------------------------------------------
    def emit(self, kind, **kw):
        with self.cv:
            self.events.append({"n": len(self.events), "kind": kind, "at": time.time(), **kw})
            self.cv.notify_all()

    def note(self, s):
        self.notes.append(s); self.notes[:] = self.notes[-50:]

    def wait(self, after, timeout=25):
        end = time.time() + timeout
        with self.cv:
            while len(self.events) <= after and not self.ended and time.time() < end:
                self.cv.wait(max(0.1, end - time.time()))
            return self.events[after:]

    # speech --------------------------------------------------------------
    def say(self, text, brain):
        with self.saying:
            if brain is not self.brain or brain is self.leaving or self.ended: return
            self.lines.append((self.agent, text))
            t0 = time.time()
            try: audio = base64.b64encode(tts_wav(text)).decode()
            except Exception as e: self.note(f"tts: {e}"); audio = None
            if time.time() - t0 > 3: print(f"phone: call {self.id} slow tts {time.time() - t0:.1f}s for {len(text)} chars", flush=True)
            if isinstance(brain, AgentLine): brain.spoke = time.time()
            if self.state == "hold":
                self.state = "talking"; self.emit("pickup", agent=self.agent)
            self.emit("say", text=text, agent=self.agent, audio=audio)

    def filler(self, brain):
        return  # no spoken filler ("let me check"): silence beats generic sayings

    def thinking(self, on):
        self.emit("thinking", on=on)

    def heard(self, text):
        self.lines.append(("person", text)); self.last = time.time(); self.partial = ""
        self.emit("heard", text=text)
        if self.brain and not self.brain.dead and self.state != "hold":
            if not isinstance(self.brain, AgentLine): self.busy_since = time.time()  # the agent's own turn isn't ours to wait on
            self.brain.send(text)

    def turn_done(self, brain):
        if brain is not self.brain: return
        self.busy_since = None
        self.emit("turn_done")
        if self.pending: p, self.pending = self.pending, None; self._do_transfer(*p)

    def brain_exit(self, brain):
        if brain is self.brain and not self.ended and not self.pending and self.state != "hold":
            self.note("brain exited"); self.emit("error", text="The line dropped on our side.")

    # transfer ------------------------------------------------------------
    def transfer(self, to, why):
        """Asked from inside the brain's own Bash call: go on hold now, swap brains when its turn ends."""
        with self.saying:   # waits for a sentence still in TTS, so it reaches the person before the hold
            self.state, self.leaving = "hold", self.brain
            self.emit("hold", agent=to, why=why)
        self.lines.append(("system", f"transferred to {to}: {why}"))
        if self.busy_since: self.pending = (to, why)
        else: self._do_transfer(to, why)

    def _do_transfer(self, to, why):
        old = self.brain
        self.brain = None
        if old: threading.Thread(target=old.stop, daemon=True).start()
        self.agent = to
        self.thread = (self.ring or {}).get("thread") or thread_of(to) or self.thread
        if to.lower() in RECEPTION_NAMES: self.brain = brain_for(self, "Hive", "[phone] The person was put back through to you, the front desk. Pick up in a few words.")
        else: self.summon(to, f"put through by {old.name if old and old.name != 'phone' else 'the front desk'}: {why or 'no reason given'}")

    # the agent itself --------------------------------------------------------
    def summon(self, name, why, thread=None):
        """Ask agent NAME (or a new agent on THREAD, name "new") to come to the phone itself. The post reaches a running agent at
        its next step (the inbox hook) and wakes a sleeping one (the starter). The person hears hold music meanwhile."""
        self.state, self.agent = "hold", name
        if thread: self.thread = thread
        self.summoned = (name, time.time())
        self.emit("hold", agent=name if name != "new" else "a new agent", why=why)
        self.lines.append(("system", f"calling {name if name != 'new' else 'a new agent on this thread'}: {why}"))
        so_far = "\n".join(f"  {s}: {t}" for s, t in self.lines[-12:] if s != "system")
        text = (f"The person is calling you on the phone right now ({why}). Pause your work and pick up now:\n\n"
                f"  phone answer {self.id}\n  phone talk {self.id} \"what you say\"\n\n"
                "phone talk speaks your words, then waits and prints what the person says back. Keep running it until it says the call "
                "ended, then carry on with your work where you left off. Talk like on a phone: one to three short sentences, no markdown, "
                "ids, paths or links. Before a slow tool call, say one short line first. `phone transfer NAME --call ID` puts them through "
                "to another agent; `phone hangup ID \"bye\"` ends the call. You can still work while on the line, between turns: post in a thread, ask another agent on the board and wait for its answer, run a short script or a quick look-up. Keep each of those under a minute so your turns stay quick; for anything bigger, start a new agent on it (hive post --model sonnet with a mention of new, in its own thread) and tell the person. One voice per call: to bring in other agents, ask them on the board and relay what they say, or transfer the call."
                + (f"\n\nSo far on the call:\n{so_far}" if so_far else "")
                + f"\n\nThe person keeps waiting for you, no copy of you will answer: pick up as soon as this reaches you.")
        argv = ["hive", "post", "--as", "phone", "--to", name] + (["--model", BRAIN_MODEL] if name == "new" else []) + (["--thread", "phone-test" if self.quiet else self.thread] if self.thread or self.quiet else [])
        try:
            r = subprocess.run(argv, input=text.replace("@", "＠"), text=True, timeout=30, capture_output=True)
            if r.returncode: self.note(f"summon post: {r.stderr.strip()[:300]}")
            elif not self.thread and not self.quiet and re.fullmatch(r"[0-9a-f]{12}", r.stdout.strip().split("\n")[0]):
                self.thread = r.stdout.strip().split("\n")[0]  # a post with no thread starts one under its own id: the call's thread
        except (OSError, subprocess.SubprocessError) as e: self.note(f"summon post: {e}")
        threading.Timer(SUMMON_S, self._no_answer, args=(self.summoned,)).start()

    def _no_answer(self, summoned):
        """Nobody answers a call to a named agent with a copy: the call waits for that agent. A new agent on a thread (not a copy of
        anyone) gets the front-desk brain after SUMMON_S."""
        if self.ended or self.summoned is not summoned or self.brain: return
        name = summoned[0]
        if name == "new":
            self.summoned = None
            self.lines.append(("system", f"no new agent picked up in {SUMMON_S}s; the front desk answered"))
            self.agent = "Hive"
            self.brain = Brain(self, "phone", SPEAK_RULES + f"\nYou are a new agent on thread {self.thread}, answering the person's call about it. Read the thread first (hive read --thread {self.thread}, one quick look).",
                               first="[phone] The person called about this thread and is on hold. Pick up: say hi in a few words and ask what they need.")
            return
        self.hold_say("Still here. They'll be right with you.")
        threading.Timer(max(1, SUMMON_LONG_S - SUMMON_S), self._give_up, args=(summoned,)).start()

    def hold_say(self, text):
        """The phone's own short voice on hold (not an agent): spoken over the music, state stays on hold."""
        with self.saying:
            if self.ended or self.brain: return
            self.lines.append(("phone", text))
            try: audio = base64.b64encode(tts_wav(text)).decode()
            except Exception as e: self.note(f"tts: {e}"); audio = None
            self.emit("say", text=text, agent="phone", audio=audio)

    def _give_up(self, summoned):
        if self.ended or self.summoned is not summoned or self.brain: return
        name = summoned[0]; self.summoned = None
        self.hold_say(f"{name} hasn't picked up. I've left them a note on the board that you called, and they'll get back to you. You can hang up now.")
        argv = ["hive", "post", "--as", "phone", "--to", name] + (["--thread", self.thread] if self.thread and not self.quiet else [])
        try:
            subprocess.run(argv, input=f"The person called you and waited {SUMMON_LONG_S // 60} min; you didn't pick up. Reach them again with `hive post` or `phone ring` when you can.", text=True, timeout=30, capture_output=True)
        except (OSError, subprocess.SubprocessError) as e: self.note(f"left-message post: {e}")
        self.lines.append(("system", f"{name} never picked up; message left on the board"))
        self.end(f"{name} never picked up")

    def answer(self, name):
        """The agent itself picks up (from `phone answer`)."""
        with self.cv:
            if self.ended: return "ended"
            if isinstance(self.brain, AgentLine) and not self.brain.dead: return "taken" if self.brain.name != name else "ok"
            if self.brain or not self.summoned: return "taken"
            self.summoned, self.agent = None, name
            self.brain = AgentLine(self, name)
        self.lines.append(("system", f"{name} picked up"))
        return "ok"

    # end -----------------------------------------------------------------
    def end(self, why="hung up"):
        if self.ended: return
        self.ended, self.state = True, "ended"
        self.emit("end", why=why)
        if self.brain: threading.Thread(target=self.brain.stop, daemon=True).start()
        threading.Thread(target=self.post_transcript, daemon=True).start()

    def log_history(self):
        """One line per finished call in ~/.hive/phone-calls.jsonl, for the call page's recent list. Not for test calls."""
        if self.quiet: return
        ls = [[s, t] for s, t in self.lines if s != "system"][-200:]
        rec = {"id": self.id, "agent": self.agent, "thread": self.thread, "started": self.started, "ended": time.time(), "lines": ls}
        try:
            f = Path.home() / ".hive" / "phone-calls.jsonl"
            with open(f, "a") as fh: fh.write(json.dumps(rec) + "\n")
        except OSError as e: self.note(f"history: {e}")

    def post_transcript(self):
        self.log_history()
        if self.quiet: return
        spoken = [(s, t) for s, t in self.lines if s != "system" or True]
        if not any(s == "person" for s, _ in spoken): return
        mins = max(1, round((time.time() - self.started) / 60))
        body = f"Phone call, about {mins} min" + (f", answered from {self.ring['from']}'s ring" if self.ring else "") + ". Transcript:\n\n"
        body += "\n".join(f"{'person' if s == 'person' else s}: {t}" if s != "system" else f"({t})" for s, t in spoken)
        agents = [a for a in dict.fromkeys(s for s, _ in spoken if s not in ("person", "system", "Hive"))]
        thread = self.thread or (self.ring or {}).get("thread") or (thread_of(agents[-1]) if agents else None)
        argv = ["hive", "post", "--as", "phone"] + (["--thread", thread] if thread else [])
        try: subprocess.run(argv, input=body.replace("@", "＠"), text=True, timeout=60, capture_output=True)
        except (OSError, subprocess.SubprocessError) as e: self.note(f"post: {e}")

    def view(self):
        return {"id": self.id, "agent": self.agent, "thread": self.thread, "state": self.state, "started": self.started,
                "partial": self.partial, "lines": self.lines[-50:]}


def brain_for(call, name, first):
    """A brain speaking as NAME: a fork of that agent's own session if it has one, else a stand-in that reads its thread."""
    if name.lower() in RECEPTION_NAMES:
        return Brain(call, "phone", RECEPTION, first=first)
    st = last_start(name)
    prompt = SPEAK_RULES + f"\nYou are agent {name}, on the phone with the person. This is a copy of your session; the original you keeps running. Things you agree to do, do now during the call or post to your thread before it ends."
    if st and st.get("harness", "claude-code") == "claude-code":
        login = str(Path(st.get("login") or best_login()).expanduser())
        if login_usage(login) >= 95:
            to = best_login()
            if copy_session(st["session"], login, to): login = to
        return Brain(call, name, prompt, resume=st["session"], login=login, first=first)
    return Brain(call, name, prompt + f"\nAgent {name} has no session to copy, so you stand in for it: `hive read` its thread first (one quick look) to know what it was doing.", first=first)


def start_call(ring_id=None, to=None, quiet=False, thread=None):
    """A call to Hive (the receptionist), to agent TO itself, to a new agent on THREAD, or answering a ring (that agent itself)."""
    ring = RINGS.pop(ring_id, None) if ring_id else None
    c = Call(ring=ring); c.quiet = bool(quiet)
    with _glock: CALLS[c.id] = c
    if to and to.lower() in RECEPTION_NAMES: to = None
    c.state = "talking"
    c.emit("connected", agent=(ring or {}).get("from") or to or ("a new agent" if thread else "Hive"))
    if ring: c.summon(ring["from"], f"you rang them ({ring['why'] or 'no reason given'}) and they picked up", ring.get("thread") or thread_of(ring["from"]))
    elif to: c.summon(to, "they asked for you directly", thread_of(to))
    elif thread: c.summon("new", f"they called thread {thread}, which starts a new agent on it", thread)
    else: c.summon("new", "they called the Hive; this call gets its own new agent and thread, like a new session. Nothing to read first: "
                          "greet them and do what they ask, as on any task")
    return c


def reaper():
    while True:
        time.sleep(15)
        now = time.time()
        for c in list(CALLS.values()):
            if not c.ended and now - c.last > 900: c.end("quiet for 15 minutes")
            if c.ended and now - c.last > 3600: CALLS.pop(c.id, None)
        for k, r in list(RINGS.items()):
            if now - r["at"] > RING_TTL: RINGS.pop(k, None)

# --------------------------------------------------------------------------------------------------- http


class H(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a): pass

    def out(self, obj, code=200):
        b = json.dumps(obj).encode()
        self.send_response(code); self.send_header("Content-Type", "application/json"); self.send_header("Content-Length", str(len(b)))
        self.send_header("Cache-Control", "no-store"); self.end_headers(); self.wfile.write(b)

    def body(self):
        n = int(self.headers.get("Content-Length") or 0)
        return self.rfile.read(n) if 0 < n <= 20_000_000 else b""

    def jbody(self):
        try: return json.loads(self.body() or b"{}")
        except ValueError: return {}

    def call(self, cid):
        c = CALLS.get(cid)
        if not c: self.out({"error": "no such call"}, 404)
        return c

    def do_GET(self):
        u = urlparse(self.path); q = parse_qs(u.query); p = u.path.rstrip("/")
        if p == "/health": return self.out({"ok": True, "models": _tts is not None})
        if p == "/rings": return self.out({"rings": sorted(RINGS.values(), key=lambda r: r["at"])})
        if p == "/history":
            try: rows = [json.loads(l) for l in (Path.home() / ".hive" / "phone-calls.jsonl").read_text().splitlines()[-40:]]
            except (OSError, ValueError): rows = []
            return self.out({"calls": [{**r, "lines": r["lines"][-60:]} for r in reversed(rows)][:20]})
        if p == "/calls": return self.out({"calls": [c.view() for c in CALLS.values() if not c.ended and (not c.quiet or q.get("all"))]})
        m = re.fullmatch(r"/call/(\w+)/listen", p)
        if m and (c := self.call(m[1])):
            line = c.brain if isinstance(c.brain, AgentLine) and c.brain.name == q.get("name", [""])[0] else None
            if not line: return self.out({"heard": [], "ended": c.ended, "gone": True, "agent": c.agent})
            heard = line.take(min(110, float(q.get("timeout", ["90"])[0] or 90)))
            return self.out({"heard": heard, "ended": c.ended, "gone": line.dead or c.brain is not line, "agent": c.agent, "muted": c.muted})
        m = re.fullmatch(r"/call/(\w+)/events", p)
        if m and (c := self.call(m[1])):
            return self.out({"events": c.wait(int(q.get("after", ["0"])[0] or 0)), "state": c.state, "agent": c.agent})
        m = re.fullmatch(r"/call/(\w+)", p)
        if m and (c := self.call(m[1])): return self.out({**c.view(), "notes": c.notes[-10:]})
        if not m: self.out({"error": "not found"}, 404)

    def do_POST(self):
        u = urlparse(self.path); p = u.path.rstrip("/")
        if p == "/call":
            b = self.jbody()
            th = str(b.get("thread") or "").strip() or None
            c = start_call(b.get("ring"), (b.get("to") or "").strip() or None, b.get("quiet"), th)
            return self.out({"id": c.id, "agent": c.agent})
        if p == "/ring":
            b = self.jbody(); name = str(b.get("from") or "").strip()
            if not re.fullmatch(r"[a-z0-9]{3,12}", name): return self.out({"error": "from: an agent name"}, 400)
            r = {"id": uuid.uuid4().hex[:8], "from": name, "why": str(b.get("why") or "")[:300], "thread": b.get("thread"), "at": time.time()}
            RINGS[r["id"]] = r
            if b.get("phone"):   # also ring the person's own phone (the real line)
                try: import telnyx; telnyx.dial(r["id"]); r = {**r, "phone": True}
                except Exception as e: r = {**r, "phone": False, "phone_error": str(e)[:200]}
            return self.out(r)
        m = re.fullmatch(r"/ring/(\w+)/decline", p)
        if m: RINGS.pop(m[1], None); return self.out({"ok": True})
        m = re.fullmatch(r"/call/(\w+)/(turn|partial|text|transfer|end|interrupt|answer|say|hangup|mute)", p)
        if not m: return self.out({"error": "not found"}, 404)
        c = self.call(m[1])
        if not c: return
        if c.ended: return self.out({"error": "call ended"}, 410)
        if m[2] == "turn":
            pcm = self.body()
            info = {}; text = stt(pcm, info=info)
            if not text and not info.get("not_person") and len(pcm) > 32000 * 1.5 and len((c.partial or "").split()) >= 2: text = c.partial  # shown live, so don't lose it
            print(f"phone: call {c.id} turn {len(pcm) / 32000:.1f}s heard {text!r}", flush=True)
            if os.environ.get("PHONE_DEBUG") or (not text and len(pcm) > 32000 * 1.5) or len(pcm) > 32000 * 20 or info.get("kept", 1) < 1: keep_turn(c.id, pcm)
            if text: threading.Thread(target=c.heard, args=(text,), daemon=True).start()
            return self.out({"text": text})
        if m[2] == "partial":
            text = stt(self.body(), partial=True)
            if text is not None and c.state != "hold": c.partial = text; c.emit("partial", text=text)
            return self.out({"text": text})
        if m[2] == "text":
            text = str(self.jbody().get("text") or "").strip()[:2000]
            if text: threading.Thread(target=c.heard, args=(text,), daemon=True).start()
            return self.out({"text": text})
        if m[2] == "mute":
            c.muted = bool(self.jbody().get("muted"))
            with c.cv: c.cv.notify_all()   # an agent waiting on the person's words gets back to its work
            return self.out({"ok": True, "muted": c.muted})
        if m[2] == "interrupt":
            c.lines.append(("system", "the person cut in")); return self.out({"ok": True})
        if m[2] == "transfer":
            b = self.jbody(); to = str(b.get("to") or "").strip()
            if not to: return self.out({"error": "to: an agent name"}, 400)
            c.transfer(to, str(b.get("why") or "")[:300]); return self.out({"ok": True, "to": to})
        if m[2] == "end":
            c.end(); return self.out({"ok": True})
        if m[2] in ("answer", "say", "hangup"):   # the agent itself on the line (phone answer / talk / hangup)
            b = self.jbody(); name = str(b.get("name") or "").strip()
            if m[2] == "answer":
                r = c.answer(name)
                return self.out({"result": r, "agent": c.agent, "lines": c.lines[-30:]})
            line = c.brain if isinstance(c.brain, AgentLine) and c.brain.name == name else None
            if not line: return self.out({"error": "you aren't on this call (run phone answer first, or it moved on)"}, 409)
            for s in SENT_END.split(str(b.get("text") or "")):
                if speakable(s): c.say(speakable(s), line)
            c.busy_since = None
            if m[2] == "hangup": c.end(f"{name} hung up")
            return self.out({"ok": True})


def serve():
    t0 = time.time(); models(); print(f"phone: models ready in {time.time() - t0:.1f}s, listening on {SOCK}", flush=True)
    threading.Thread(target=reaper, daemon=True).start()
    try: import telnyx   # the optional real phone line: only where telnyx.py is installed beside this file
    except ImportError: telnyx = None
    try: telnyx and telnyx.serve_public(sys.modules[__name__])
    except Exception as e: print(f"phone: real line off: {e}", flush=True)
    SOCK.parent.mkdir(mode=0o700, parents=True, exist_ok=True); SOCK.unlink(missing_ok=True)
    old = os.umask(0o177)
    try: srv = Server(str(SOCK), H)
    finally: os.umask(old)
    os.chmod(SOCK, 0o600); srv.serve_forever()


class Server(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
    daemon_threads = True
    def get_request(self):
        conn, _ = self.socket.accept()
        return conn, ("local", 0)   # BaseHTTPRequestHandler expects a (host, port) address


class UnixHTTP(http.client.HTTPConnection):
    """HTTP to the call service over its socket. Used by the phone CLI; the dash has its own copy."""
    def __init__(self, timeout=30): super().__init__("localhost", timeout=timeout)
    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM); self.sock.settimeout(self.timeout); self.sock.connect(str(SOCK))

# --------------------------------------------------------------------------------------------------- cli


def api(method, path, obj=None, timeout=30):
    c = UnixHTTP(timeout)
    try:
        c.request(method, path, body=json.dumps(obj).encode() if obj is not None else None, headers={"Content-Type": "application/json"})
        r = c.getresponse(); b = r.read()
    except OSError as e: return {"error": f"the phone service isn't answering ({e.__class__.__name__}): systemctl --user start hive-phone"}
    finally: c.close()
    try: return json.loads(b)
    except ValueError: return {"error": f"HTTP {r.status}"}


def listen(cid, name):
    """Wait for the person's next words on call CID and print them for the agent, or say the call is over for it."""
    r = api("GET", f"/call/{cid}/listen?name={name}&timeout=90", timeout=120)
    if r.get("error") == "no such call" or r.get("ended"): print("The call ended. Carry on with your work where you left off."); return
    if r.get("gone"): print(f"You're off the call now ({r.get('agent')} has the line). Carry on with your work where you left off."); return
    if r.get("muted") and not r.get("heard"): print(f"(The person muted their mic; the call is still on. Carry on with your work and post progress; run phone listen {cid} later, or phone talk {cid} \"...\" to say something.)"); return
    if r.get("heard"): print("The person: " + " ".join(r["heard"]) + f'\n\n(Answer with phone talk {cid} "...")'); return
    print(f'(The person said nothing for 90 seconds. Run phone listen {cid} to keep waiting, or phone talk {cid} "..." to say something.)')


SMS_DAILY_CAP = 10


def send_sms(text):
    """Text the person (the first allowed caller in telnyx.json) from the Hive's Telnyx number. Outbound only; replies are not read."""
    import urllib.request, urllib.error
    cfgd = Path.home() / ".config/hive"
    cfg = json.loads((cfgd / "telnyx.json").read_text())
    key = (cfgd / "secrets/telnyx-api-key").read_text().strip()
    text = text.strip()
    if not text: sys.exit("empty text")
    if len(text) > 600: sys.exit("too long for a text (600 characters max); post the rest on the board and send a link")
    count = cfgd / "sms-count.json"
    today = time.strftime("%Y-%m-%d", time.gmtime())
    try: c = json.loads(count.read_text())
    except (OSError, ValueError): c = {}
    if c.get("day") != today: c = {"day": today, "n": 0}
    if c["n"] >= SMS_DAILY_CAP: sys.exit(f"daily text cap ({SMS_DAILY_CAP}) reached; use the board or a ring instead")
    body = json.dumps({"from": cfg["number"], "to": cfg["allowed_callers"][0], "text": text, "type": "SMS"}).encode()
    rq = urllib.request.Request("https://api.telnyx.com/v2/messages", data=body, headers={"Authorization": "Bearer " + key, "Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(rq, timeout=30) as r: d = json.load(r)
    except urllib.error.HTTPError as e: sys.exit(f"Telnyx refused the text (HTTP {e.code}): {e.read().decode()[:600]}")
    except OSError as e: sys.exit(f"could not reach Telnyx: {e}")
    c["n"] += 1; count.write_text(json.dumps(c))
    return f"sent (id {d.get('data', {}).get('id')}); {c['n']}/{SMS_DAILY_CAP} today"


def main(argv):
    import argparse
    ap = argparse.ArgumentParser(prog="phone", description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sp = ap.add_subparsers(dest="cmd", required=True)
    sp.add_parser("serve")
    sp.add_parser("setup")
    t = sp.add_parser("transfer"); t.add_argument("agent"); t.add_argument("--why", default=""); t.add_argument("--call")
    for k in ("answer", "listen"): sp.add_parser(k).add_argument("call")
    for k in ("talk", "hangup"): x = sp.add_parser(k); x.add_argument("call"); x.add_argument("text", nargs="?" if k == "hangup" else None, default="")
    r = sp.add_parser("ring"); r.add_argument("--why", default=""); r.add_argument("--thread"); r.add_argument("--phone", action="store_true", help="also ring the person's own phone")
    sp.add_parser("calls")
    x = sp.add_parser("enroll"); x.add_argument("files", nargs="*")
    sm = sp.add_parser("sms", help="text the person's own number (a link or a short note); capped per day"); sm.add_argument("text")
    s = sp.add_parser("say"); s.add_argument("text"); s.add_argument("-o", default="say.wav")
    a = ap.parse_args(argv)
    if a.cmd == "serve": return serve()
    if a.cmd == "setup": return setup()
    if a.cmd == "enroll": return enroll(a.files)
    if a.cmd == "say":
        Path(a.o).write_bytes(tts_wav(a.text)); print(a.o); return
    if a.cmd == "calls": print(json.dumps(api("GET", "/calls"), indent=1)); return
    me = os.environ.get("HIVE_NAME", "")
    if a.cmd == "answer":
        r = api("POST", f"/call/{a.call}/answer", {"name": me})
        if r.get("result") == "ok":
            so_far = "\n".join(f"  {s}: {t}" for s, t in r.get("lines", []) if s != "system")
            print("You're on the line; the person hears hold music until you speak." + (f" The call so far:\n{so_far}" if so_far else "")
                  + f'\n\nNow say hi: phone talk {a.call} "..."')
        elif r.get("result") == "taken" or r.get("result") == "ended" or r.get("error"):
            print(f"Too late: {'the call ended' if r.get('result') == 'ended' or r.get('error') else 'someone else has the line (a copy of you, if you took too long)'}. "
                  "Carry on with your work; the transcript gets posted in the thread.")
        return
    if a.cmd in ("talk", "hangup"):
        r = api("POST", f"/call/{a.call}/{'say' if a.cmd == 'talk' else 'hangup'}", {"name": me, "text": a.text}, timeout=120)   # it returns once every sentence is synthesised
        if r.get("error"): print(f"{r['error']}. Carry on with your work."); return
        if a.cmd == "hangup": print("Hung up. Carry on with your work where you left off."); return
        return listen(a.call, me)
    if a.cmd == "listen": return listen(a.call, me)
    if a.cmd == "transfer":
        cid = a.call or os.environ.get("PHONE_CALL")
        if not cid: sys.exit("phone transfer needs --call ID (or PHONE_CALL set, inside a call)")
        print(json.dumps(api("POST", f"/call/{cid}/transfer", {"to": a.agent, "why": a.why})))
        print("Done: the person is on hold and the agent is picking up. Don't say anything more.")
        return
    if a.cmd == "sms": print(send_sms(a.text)); return
    if a.cmd == "ring":
        name = os.environ.get("HIVE_NAME", "")
        thread = a.thread or os.environ.get("HIVE_THREAD") or thread_of(name)
        res = api("POST", "/ring", {"from": name, "why": a.why, "thread": thread, "phone": a.phone})
        if res.get("error"): sys.exit(res["error"])
        text = (f"Ringing the person: {a.why or 'to talk'}. The ring shows in the dashboard for 10 minutes" + (", and their phone is ringing too (PIN first)" if res.get("phone") else "") + "; answering puts a copy of me on the line.\n"
                f"For you: answer the call in the dashboard if you have a minute.")
        subprocess.run(["hive", "post"] + (["--thread", thread] if thread else []), input=text, text=True)
        print(json.dumps(res))


if __name__ == "__main__":
    main(sys.argv[1:])
