#!/bin/sh
# Uninstall the Hive (your folder, ~/hive by default, is left alone):
#   curl -fsSL https://raw.githubusercontent.com/ihsan-sa/hive/main/install/uninstall.sh | sh
# It removes hive and hive-v0, its name up to v0.1.2.
set -eu
CMD="${HIVE_CMD:-hive}"
DIR="${HIVE_BIN_DIR:-$HOME/.local/bin}"
for c in "$CMD" hive-v0; do
  if [ -x "$DIR/$c" ]; then "$DIR/$c" uninstall || true; fi
  rm -f "$DIR/$c"
done
echo "The Hive is uninstalled. Your folder and its projects are untouched."
