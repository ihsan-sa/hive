# phone

Voice calls with the Hive. The person presses **Call** in the dashboard composer (a new agent on that thread picks up, or Hive on a new conversation), or **Call** on an agent's hover card or page (that agent itself picks up), and talks; it answers out loud.

## How a call works

- **Speech on the box.** The browser cuts the person's speech into turns (a noise-floor gate with barge-in) and sends 16 kHz PCM. `faster-whisper-large-v3-turbo` on the GPU (or `small.en` on the CPU) writes it down; Kokoro (`af_heart`) speaks the answer. Models load in about 4 s at service start, and a turn's first words come back 1 to 2 s after the person stops.
- **Calling an agent reaches the agent itself.** The service posts one short line to the board as `phone --to NAME` with the reason and the `phone answer` command; `phone answer` prints how to talk and the call so far, so only the agent that picks up reads them. A running agent gets it mid-run from the inbox hook; a sleeping one is woken by the starter within about 10 s (`wake_phone_s`). It runs `phone answer CALL`, then `phone talk CALL "..."` (speaks, waits for the reply, prints it) until `phone hangup CALL "bye"`, then goes back to its work. The person hears hold music until it answers, and "one sec" if it's slow to reply. No copy ever answers: after 20 s (`PHONE_SUMMON_S`) the person hears "Still here. They'll be right with you."; after 4 min (`PHONE_SUMMON_LONG_S`) the phone says nobody came, leaves a board note for the agent and ends the call. A late `phone answer` before then still picks up. (A call to a new agent on a thread has no one to wait for, so after the first wait the front-desk brain answers.)
- **Calling a thread starts a new agent** (`--to new --model sonnet` in that thread), which reads the thread and picks up the same way.
- **Calling Hive starts a new agent in a new thread**, like opening a new session: it shows in the agent view and the thread list, and its thread is the call's (under For you only, it shows only when it posts something for you). A transfer back to Hive gets the receptionist, a `claude -p` stream-json brain that looks things up on the board (`hive who`, `hive read`) and answers. If a login is near its weekly limit, a forked session is copied to the login with the most room.
- **Sentences as they come.** The brain's text streams out sentence by sentence and each is spoken as soon as it's done. It does not announce tool calls or speak filler.
- **Transfer.** "Put me through to whoever's on X" makes the brain run `phone transfer NAME --why ...`. The last sentence finishes, the call goes on hold (hold music in the browser), and NAME is summoned as above, with the call so far in its post.
- **Rings.** An agent that needs the person runs `phone ring --why "..." --thread T`. The dashboard shows a ringing banner with Answer and Decline, and a board note with a `For you:` line goes to the thread. Answering summons that agent to pick up. A ring expires after 10 min.
- **After the call** the transcript goes to the agent's (or ring's) thread as `phone`, with no mentions. A call quiet for 15 min is hung up.

## Commands

    phone serve                         # the service (systemd: hive-phone)
    phone answer CALL                   # an agent called by name picks up
    phone talk CALL TEXT                # say TEXT, wait for the reply, print it
    phone listen CALL                   # wait for the person's next words
    phone hangup CALL [TEXT]            # say goodbye and end the call once the person is quiet; if they are talking it stays on and prints their words
    phone transfer NAME --why TEXT      # from inside a call: hand the call over
    phone ring --why TEXT [--thread T]       # an agent asks the person to pick up
    phone calls                         # calls and rings now
    phone say TEXT -o out.wav           # Kokoro to a file

HTTP on the Unix socket `~/.hive/phone.sock` (mode 600, so no other account on the machine can reach it, as it could loopback; the dash proxies `/api/phone/*` for anyone with the post cap): `GET /health /rings /calls /call/ID/events?after=N`, `GET /call/ID/listen`, `POST /call {to, ring, thread, quiet}`, `/call/ID/answer|say|hangup {name, text}`, `/ring`, `/ring/ID/decline`, `/call/ID/turn` (raw PCM), `/text`, `/interrupt`, `/transfer`, `/end`. `quiet: true` makes a test call that posts no transcript.

## Setup

`phone setup` builds `.venv` (uv, else `python -m venv`) with faster-whisper and kokoro-onnx, downloads the Kokoro files and the recognition model into `models/` (~2 GB), and the CAM++ voice model, links `hive-phone.service` into `~/.config/systemd/user/` (MemoryMax 6G) and starts it. With an NVIDIA GPU it installs the CUDA libraries and uses `faster-whisper-large-v3-turbo` (float16, beam 5); without one, `faster-distil-whisper-small.en` on the CPU (int8, beam 1): a 3 s turn takes about 1 to 2 s on an idle 8-core machine. `PHONE_DEVICE=cpu` forces the CPU, `PHONE_STT_MODEL=DIR` picks another model. `PHONE_DEBUG=1` saves the last turn's audio to `$TMPDIR/phone-last-turn.pcm`.

Test: `CDP_ARGS="--use-fake-device-for-media-stream --use-fake-ui-for-media-stream --use-file-for-fake-audio-capture=Q.wav" node test/cdp.mjs drive.mjs http://127.0.0.1:8077/new/` with a 48 kHz wav from `phone say` and a script that calls `startCall({to: "Hive", quiet: true})`.

Whisper's prompt is a sentence on purpose: a bare word list as `initial_prompt` cut "Hi there, who is working on file previews?" down to "Hi, there."

## Transcription and end of turn

- **Vocabulary:** `STT_HINT` (Hive's names and terms, `PHONE_STT_HINT` to replace it) goes in as `hotwords`, which reach every 30 s window; `initial_prompt` with `condition_on_previous_text=False` only reaches the first. Distil models can't take either: with the old hint distil-whisper returned no segments for 22 of 51 chunks of real speech. So the hint is off for any model with `distil` in its name (the CPU fallback). Finals use beam 5, partials beam 1.
- **Voice check:** after `phone enroll [FILE...]` (default: the saved turns; written to `~/.hive/voice-enrol.npz`), `speaker.py` scores each 1.5 s window against the person's voice with CAM++ and mutes windows under `PHONE_SPK_THR` (0.35). A turn with nothing left is dropped and logged. It fails open (the audio passes as is) only when the voice looks to have stopped matching: two such turns in a row on that channel and no turn with the person in it for `STALE` (180 s). Until 2026-11-05 it failed open after any two in a row, and in a room with people talking that let their runs through (`eval/replay_turns.py` replays the saved real turns: of 27 turns with nobody enrolled in them, 7 got through before and 2 after, and both of those had a matching turn of the person's in the same second that wasn't saved, so live they'd be dropped too). No enrolment, or any error, means no mask. Partials are masked the same way. It stops a nearby talker before or after the person; it doesn't separate two people talking at once.
- A segment is dropped as silence only when Whisper's own rule says so (no_speech_prob > 0.6 and avg_logprob < -1.0).
- If a long final still comes back empty, the last live partial is used, and the audio is kept in `$TMPDIR/phone-turns/` to look at.
- The browser ends a turn after 1.1 s of quiet when the live transcript ends a sentence (not "..." or a trailing "uh", "so", "and"), or the reply is short; 2.2 s mid-thought; 120 s at most. Turns over 20 s are kept in `$TMPDIR/phone-turns/` too.
