#!/bin/sh
# Install the Hive on Linux or macOS. One line:
#   curl -fsSL https://hive.ihsan.cc/install.sh | sh
# Running it again reinstalls. HIVE_VERSION=v0.1.2 installs that release instead
# of the latest. The binary is static; no runtime is needed.
set -eu

REPO="${HIVE_REPO:-ihsan-sa/hive}"
CMD="${HIVE_CMD:-hive}"
VERSION="${HIVE_VERSION:-latest}"
DIR="${HIVE_BIN_DIR:-$HOME/.local/bin}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "$CMD: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac
case "$os" in linux|darwin) ;; *) echo "$CMD: unsupported OS $os (on Windows use install.ps1)" >&2; exit 1 ;; esac

asset="hive-$os-$arch"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading the Hive ($asset, $VERSION) from $REPO ..."
base="https://github.com/$REPO/releases/download/$VERSION"
[ "$VERSION" = latest ] && base="https://github.com/$REPO/releases/latest/download"
curl -fsSL "$base/$asset" -o "$tmp/$asset"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"
want=$(grep " $asset\$" "$tmp/checksums.txt" | awk '{print $1}')
if command -v sha256sum >/dev/null 2>&1; then got=$(sha256sum "$tmp/$asset" | awk '{print $1}'); else got=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}'); fi
if [ -z "$want" ] || [ "$want" != "$got" ]; then echo "$CMD: checksum mismatch; not installing" >&2; exit 1; fi

mkdir -p "$DIR"
for c in "$CMD" hive-v0; do [ -x "$DIR/$c" ] && "$DIR/$c" shutdown >/dev/null 2>&1 || true; done
install -m 755 "$tmp/$asset" "$DIR/$CMD"

case ":$PATH:" in
  *":$DIR:"*) ;;
  *)
    # macOS opens zsh, which never reads .profile, so .zshrc is written there.
    for rc in "$HOME/.profile" "$HOME/.bashrc" "$HOME/.zshrc"; do
      [ -f "$rc" ] || [ "$rc" = "$HOME/.profile" ] || [ "$rc:$os" = "$HOME/.zshrc:darwin" ] || continue
      grep -qs "$DIR" "$rc" || printf '\nexport PATH="%s:$PATH"\n' "$DIR" >> "$rc"
    done
    echo "Added $DIR to your PATH (open a new terminal)."
    ;;
esac
"$DIR/$CMD" --version
echo "Installed. Run: $CMD"
