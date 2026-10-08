#!/bin/bash
# Start the e2e stack: fixture site, private bridge, headless browser with the
# extension under test, paired and signed in. PIDs go to $E2E_WORK/pids/ so
# teardown.sh stops exactly what this started (never a pkill -f pattern).
#
#   E2E_BUILD=1   build $E2E_BIN from this checkout first
#   V2=1          start the fixture with the renamed name field (rerecord checks)
set -euo pipefail
source "$(dirname "$0")/env.sh"
mkdir -p "$E2E_WORK/pids" "$E2E_WORK/logs"

if [ "${E2E_BUILD:-}" = 1 ] || [ ! -x "$E2E_BIN" ]; then
  (cd "$REPO_ROOT" && go build -tags devaccount -o "$E2E_BIN" ./cmd/monoagentcli)
fi

busy() { ss -ltn 2>/dev/null | grep -q "127.0.0.1:$1 \|\[::1\]:$1 \|\*:$1 "; }
for p in "$E2E_BRIDGE_PORT" "$E2E_CDP_PORT" "$E2E_FIXTURE_PORT"; do
  if busy "$p"; then echo "e2e: port $p is already in use; run teardown.sh or pick another" >&2; exit 1; fi
done

# fixture site
: > "$E2E_WORK/requests.log"
PORT="$E2E_FIXTURE_PORT" LOG="$E2E_WORK/requests.log" V2="${V2:-}" \
  setsid node "$E2E_DIR/fixture/server.mjs" > "$E2E_WORK/logs/fixture.log" 2>&1 &
echo $! > "$E2E_WORK/pids/fixture"

# private bridge; analyze requests from the side panel use the stub runner
HOME="$E2E_HOME" MONOAGENT_EXTENSION_PORT="$E2E_BRIDGE_PORT" PATH="$E2E_WORK/bin:$PATH" \
  MONOMIND_BIN="$E2E_DIR/stub-monomind.mjs" E2E_SITE="$E2E_SITE" E2E_WORK="$E2E_WORK" \
  setsid "$E2E_BIN" extension serve > "$E2E_WORK/logs/bridge.log" 2>&1 &
echo $! > "$E2E_WORK/pids/bridge"
for _ in $(seq 1 40); do [ -s "$E2E_HOME/.monoagent/extension.token" ] && busy "$E2E_BRIDGE_PORT" && break; sleep 0.5; done
busy "$E2E_BRIDGE_PORT" || { echo "e2e: bridge did not start (see $E2E_WORK/logs/bridge.log)" >&2; exit 1; }

# browser
CHROME="$E2E_CHROME"
if [ -z "$CHROME" ]; then
  for c in chromium chromium-browser google-chrome google-chrome-stable; do
    if command -v "$c" >/dev/null; then CHROME=$(command -v "$c"); break; fi
  done
fi
[ -n "$CHROME" ] || { echo "e2e: no Chromium/Chrome found (set E2E_CHROME)" >&2; exit 1; }
PROFILE="$E2E_WORK/chrome-profile-$(date +%s)"
setsid "$CHROME" --headless=new --remote-debugging-port="$E2E_CDP_PORT" --user-data-dir="$PROFILE" \
  --load-extension="$E2E_EXTENSION_DIR" --disable-features=DisableLoadExtensionCommandLineSwitch \
  --no-first-run --no-default-browser-check \
  --host-resolver-rules="MAP crm.e2e.test 127.0.0.1, MAP evil.e2e.test 127.0.0.1" \
  --window-size=1400,900 about:blank > "$E2E_WORK/logs/chrome.log" 2>&1 &
echo $! > "$E2E_WORK/pids/chrome"
for _ in $(seq 1 40); do busy "$E2E_CDP_PORT" && break; sleep 0.5; done
sleep 2

node "$E2E_DIR/browser.mjs" pair
node "$E2E_DIR/browser.mjs" login >/dev/null
echo "e2e stack up: bridge :$E2E_BRIDGE_PORT, browser CDP :$E2E_CDP_PORT, fixture $E2E_SITE, HOME $E2E_HOME"
