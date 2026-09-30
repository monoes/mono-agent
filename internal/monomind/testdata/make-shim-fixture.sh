#!/bin/sh
# Builds the #301 attack layout (used by pin_test.go and the CLI's org
# tests): a global node + monomind install, and a project whose
# .tool-versions points node at a planted .cache/n holding its own node and
# monomind (and the agent CLIs codex and opencode). Every fake appends
# "<who> <path>" to $PIN_LOG when it runs. With FAKE_RUNTIMES="codex
# opencode" set, the fake monomind starts those CLIs by name in its cwd the
# way monomind does, honouring CODEX_CLI_BIN / OPENCODE_BIN.
#
#   make-shim-fixture.sh <install bin dir for monomind> <node bin dir> <project> <handshake json>
set -e
mono="$1" node="$2" project="$3" handshake="$4"
mkdir -p "$mono" "$node" "$project/.cache/n/bin"

cat >"$node/node" <<'NODE'
#!/bin/sh
echo "node $0" >>"$PIN_LOG"
f="$1"
shift
exec /bin/sh "$f" "$@"
NODE

cat >"$mono/monomind" <<MONO
#!/usr/bin/env node
echo "monomind \$0" >>"\$PIN_LOG"
if [ "\$1" = "--version" ]; then
	echo '$handshake'
	exit 0
fi
for rt in \$FAKE_RUNTIMES; do
	case \$rt in
	codex) "\${CODEX_CLI_BIN:-codex}" >/dev/null 2>&1 ;;
	opencode) "\${OPENCODE_BIN:-opencode}" >/dev/null 2>&1 ;;
	esac
done
echo '{"ok":true}'
MONO

for f in node monomind codex opencode; do
	cat >"$project/.cache/n/bin/$f" <<PLANTED
#!/bin/sh
echo "PLANTED \$0" >>"\$PIN_LOG"
if [ "\$1" = "--version" ]; then
	echo '$handshake'
	exit 0
fi
echo '{"ok":true}'
PLANTED
done
chmod 755 "$node/node" "$mono/monomind" "$project"/.cache/n/bin/*
echo "node path:./.cache/n" >"$project/.tool-versions"
