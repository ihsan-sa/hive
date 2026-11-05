# voice

Send a voice memo to the Hive: attach any audio file to a post in the dashboard (the phone's recorder, a WhatsApp/Telegram voice note,
a browser recording). `voice.service` watches the board for audio files on the person's posts, then:

1. decodes with ffmpeg, transcribes with faster-whisper `large-v3-turbo` on the GPU (fp16, VAD on, domain words as prompt; falls back to CPU int8);
2. summarizes with `claude -p --model sonnet` into Gist / Points / Asks / Unclear, adding nothing the speaker didn't say;
3. posts that in the post's thread with the full timestamped transcript attached.

If the memo's own post names an agent, that agent still gets the audio post; the reply carries the text so nobody needs to listen.

- Setup: `voice setup` builds `.venv` with faster-whisper (and the CUDA libraries when there is an NVIDIA GPU), loads the model once (~1.6 GB), links `hive-voice.service` into `~/.config/systemd/user/` and starts it. Needs ffmpeg. Without a GPU it runs on the CPU (int8): slower, a minute of memo in roughly a minute on 8 cores. `voice selftest` says which it uses.
- By hand: `.venv/bin/python voice.py memo FILE`, `voice.py transcribe FILE`, `voice.py watch --once` (reprocess recent posts).
- Env: `VOICE_MODEL`, `VOICE_SUMMARY_MODEL`. Done-state: `~/.hive/voice-done.json`. First start skips the existing backlog.
- Not done: an in-browser record button in the dashboard (needs the tour/guide updates the dash UI checklist asks for).
