#!/bin/bash
# Build the venv (CPU torch, transformers, numpy) and link the unit. Does not start or enable it.
set -e
D=$(cd "$(dirname "$0")" && pwd)
[ -x "$D/.venv/bin/python" ] || uv venv "$D/.venv" -q
"$D/.venv/bin/python" -c "import torch, transformers, numpy" 2>/dev/null || {
  uv pip install -q --python "$D/.venv/bin/python" torch --index-url https://download.pytorch.org/whl/cpu
  uv pip install -q --python "$D/.venv/bin/python" transformers numpy
}
# serve.py runs offline, so fetch the model files here, once (about 130 MB)
HF_HUB_OFFLINE=0 "$D/.venv/bin/python" -c "from transformers import AutoModel, AutoTokenizer; m='BAAI/bge-small-en-v1.5'; AutoTokenizer.from_pretrained(m); AutoModel.from_pretrained(m)"
mkdir -p ~/.config/systemd/user && ln -sf "$D/kb-dense.service" ~/.config/systemd/user/kb-dense.service
systemctl --user daemon-reload
echo "ready: systemctl --user start kb-dense"
