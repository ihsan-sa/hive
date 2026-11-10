# kb-dense

Search by meaning for `kb find`. A small service holds vectors (BAAI/bge-small-en-v1.5, 384 numbers each) of the library's passages and answers the passages nearest to a question. `kb find` merges them with the word search (weights measured as engine `hybrid` in `projects/kb-search`: word 1, dense 0.5, K=20, bonus 0.02 on the word list, 30 deep each, key path:line). `kb recall` (message sweeps) is not touched.

It is a helper, not a dependency: `~/core/kb.py` is stdlib only and works with this module absent, stopped or broken. No socket, an error, a reply that is not understood, or no answer within `KB_DENSE_MS` (450 ms) and `kb find` prints exactly what the word search alone prints.

## Start, stop, turn off

```
~/modules/kb-dense/setup.sh               # once: venv (CPU torch, transformers, numpy) and the unit link; starts nothing
systemctl --user start kb-dense           # now (the first start seeds the vector store in ~10 s)
systemctl --user stop kb-dense            # kb find goes back to the word search at once
systemctl --user enable kb-dense          # also at login
KB_DENSE=0 kb find WORDS                  # one search without it
```

Status: `journalctl --user -u kb-dense -n 20`. The socket answers `{"stats":1}`:

```
printf '{"stats":1}\n' | python3 -c "import socket,sys;s=socket.socket(socket.AF_UNIX);s.connect('$HOME/.cache/kb-dense/sock');s.sendall(sys.stdin.buffer.read());print(s.recv(65536).decode())"
```

## What it does

- Listens on a unix socket only, `~/.cache/kb-dense/sock` (directory mode 0700), nothing on the network. One JSON line in, one out: `{"db": PATH, "q": TEXT, "n": 30}` gives `{"ok": true, "hits": [[path, line, end], ...], "ms": N}`, at most 2 per file. `{"db": PATH, "warm": SECONDS}` waits until that index is read (the bench uses it); `{"stats": 1}` gives sizes and memory.
- CPU only (`CUDA_VISIBLE_DEVICES=""`, 2 threads, `Nice=10`, `MemoryMax=3500M`). It needs about 2.5 GB with torch and the mapped vectors of 170k passages; on 2026-11-10 a cap of 1G left it answering nothing for 45 min with systemd still calling it active, since `MemoryHigh` throttles and kills nothing: the vectors are fp16 on disk, memory-mapped, and the product with the question runs in chunks of 8192 rows, never as one float32 matrix.
- The vector store, `~/.cache/kb-dense/store/bge-small-en-v1.5-256/` (`keys.bin`, `vec.f16`), is keyed by a hash of the exact text that was embedded (`"{title} > {head}\n{body}"[:1536]`, at most 256 tokens, as `engines/dense/build.py`), so one store serves any index: the live one and any snapshot. First start seeds it from `projects/kb-search/cache/dense/2026-11-06r2-bge-small-en-v1.5-256/`.
- It keeps the row map of each index it is asked about (up to 4; the live one stays), rebuilt when the file changes, reading text only for passages that changed.
- Every `KB_DENSE_REFRESH` (180 s) it looks at the live index (`~/.cache/hive-kb/index.db`) and embeds passages without a vector, 8 at a time, sleeping as long as each batch took, so a search is not held up for long. A passage without a vector is simply not in the dense list. Only the live index is ever embedded; any other is searched with what the store has.
- Not done: vectors of passages that no longer exist stay in the store (it grows by the churn, about 150 KB per 200 passages); compact by deleting the store directory and restarting (the seed returns the snapshot's, the refresh fills the rest).

## Settings (environment of the unit)

`KB_DENSE_DIR`, `KB_DENSE_SOCK`, `KB_DENSE_LIVE` (index to keep filled), `KB_DENSE_SEED`, `KB_DENSE_SEED_DB`, `KB_DENSE_REFRESH`, `KB_DENSE_BATCH`, `KB_DENSE_DUTY`. In `kb.py`: `KB_DENSE=0` (off), `KB_DENSE_SOCK`, `KB_DENSE_MS`.

## Log

The search log record of a `kb find` carries `"dense": {"used": true|false, "ms": N}` when a socket was there to ask.

## Tests

`python3 test_kb_dense.py` (needs the pinned snapshot's index; `KB_CORE` says which kb.py): no service, a service that never answers, garbage, an error reply, a wrong-shaped reply, `KB_DENSE=0`, a filtered find, a dense-only hit, the merge formula.
