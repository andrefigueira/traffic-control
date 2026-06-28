#!/usr/bin/env bash
# Traffic Control installer. Builds the single `tc` binary and drops it on your
# PATH. Optionally wires the current project's Claude Code config.
#
#   ./install.sh            build and install the binary
#   ./install.sh --here     also wire Claude Code in the current directory
#
# Override the install location with PREFIX, e.g. PREFIX=/usr/local ./install.sh
set -euo pipefail

PREFIX="${PREFIX:-$HOME/.local}"
BINDIR="$PREFIX/bin"
WIRE_HERE=0
[ "${1:-}" = "--here" ] && WIRE_HERE=1

say()  { printf '\033[1;36m==>\033[0m %s\n' "$1"; }
warn() { printf '\033[1;33m warning:\033[0m %s\n' "$1"; }

if ! command -v go >/dev/null 2>&1; then
  echo "Go is required to build Traffic Control. Install Go, then re-run." >&2
  exit 1
fi

cd "$(dirname "$0")"
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"

say "building tc $VERSION"
go build -ldflags "-X main.version=$VERSION" -o bin/tc ./cmd/tc

say "installing to $BINDIR/tc"
mkdir -p "$BINDIR"
install -m 0755 bin/tc "$BINDIR/tc"

case ":$PATH:" in
  *":$BINDIR:"*) : ;;
  *) warn "$BINDIR is not on your PATH. Add this to your shell profile:"
     printf '       export PATH="%s:$PATH"\n' "$BINDIR" ;;
esac

if [ "$WIRE_HERE" = "1" ]; then
  say "wiring Claude Code in $(pwd)"
  "$BINDIR/tc" install-claude --project .
fi

cat <<EOF

Done. Next:

  1. Start the tower (leave it running):
       tc serve

  2. Wire a project for Claude Code (once per project):
       cd /path/to/your/project && tc install-claude

  3. Run Claude Code in that project as usual. Agents now check in, file
     flight plans, and get warned off files other agents are editing.

Quick check:
       tc status
EOF
