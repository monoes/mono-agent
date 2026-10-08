#!/usr/bin/env bash
# Proves the hardened-runtime check of release.yml (B5a Task 5b) on a Mac: it passes binaries and an
# app bundle signed with --options runtime, and fails today's signatures. A stand-in for the real
# assets: two tiny Go programs in a bundle laid out like MonoAgent.app.
set -euo pipefail
dir="$(mktemp -d)"
trap 'rm -rf "$dir"' EXIT

printf 'module example.com/hr\n\ngo 1.26.0\n' > "$dir/go.mod"
printf 'package main\n\nfunc main() {}\n' > "$dir/main.go"
(cd "$dir" && CGO_ENABLED=0 go build -o linker .)

# The check of the workflow, as one function: the runtime flag, then dyld ignoring DYLD_ variables.
check() {
  local f="$1" run="${2:-}"
  codesign --verify --strict "$f" || return 1
  if [ "$(codesign -dv "$f" 2>&1 | grep -c 'flags=0x[0-9a-f]*([^)]*runtime')" -eq 0 ]; then
    echo "not hardened: ${f#"$dir"/}"; return 1
  fi
  if [ -n "$run" ]; then
    local output
    output="$(DYLD_PRINT_LIBRARIES=1 "$run" --help 2>&1)" || return 1
    if [ "$(printf '%s\n' "$output" | grep -c '^dyld\[')" -ne 0 ]; then
      echo "dyld honours DYLD_ variables: ${run#"$dir"/}"; return 1
    fi
  fi
  echo "hardened: ${f#"$dir"/}"
}

# Loose binaries: Go's linker signature, today's ad hoc signature, and the hardened runtime.
cp "$dir/linker" "$dir/adhoc"
cp "$dir/linker" "$dir/hardened"
codesign --force --sign - "$dir/adhoc"
codesign --force --options runtime --sign - "$dir/hardened"
for f in linker adhoc hardened; do
  printf '%s: %s, dyld lines %s\n' "$f" "$(codesign -dv "$dir/$f" 2>&1 | grep -o 'flags=0x[0-9a-f]*([^)]*)')" \
    "$(DYLD_PRINT_LIBRARIES=1 "$dir/$f" 2>&1 | grep -c '^dyld\[' || true)"
done
check "$dir/linker" "$dir/linker" && exit 1
check "$dir/adhoc" "$dir/adhoc" && exit 1
check "$dir/hardened" "$dir/hardened"

# A bundle laid out like MonoAgent.app, signed inside out as the workflow does, then zipped and
# unzipped as the release and `update --app` do.
APP="$dir/build/MonoAgent.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
printf 'Third-party notices fixture\n' > "$APP/Contents/Resources/NOTICE"
cat > "$APP/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>monoagent-ui</string>
<key>CFBundleIdentifier</key><string>com.example.monoagent-standin</string>
<key>CFBundlePackageType</key><string>APPL</string>
</dict></plist>
PLIST
cp "$dir/linker" "$APP/Contents/MacOS/monoagent-ui"
cp "$dir/linker" "$APP/Contents/MacOS/monoagentcli"
codesign --force --options runtime --sign - "$APP/Contents/MacOS/monoagentcli"
codesign --force --options runtime --sign - "$APP"
codesign --verify --strict --deep "$APP"
check "$APP"
check "$APP/Contents/MacOS/monoagentcli" "$APP/Contents/MacOS/monoagentcli"
(cd "$dir/build" && zip -qr "$dir/app.zip" MonoAgent.app)
mkdir "$dir/unzipped"
unzip -q "$dir/app.zip" -d "$dir/unzipped"
cmp "$APP/Contents/Resources/NOTICE" "$dir/unzipped/MonoAgent.app/Contents/Resources/NOTICE"
codesign --verify --strict --deep "$dir/unzipped/MonoAgent.app"
check "$dir/unzipped/MonoAgent.app"
check "$dir/unzipped/MonoAgent.app/Contents/MacOS/monoagentcli" "$dir/unzipped/MonoAgent.app/Contents/MacOS/monoagentcli"

# The CLI copied in after the app was signed, as release.yml does today: the seal no longer
# matches, and a CLI with only the linker signature is not hardened.
cp "$dir/linker" "$APP/Contents/MacOS/monoagentcli"
codesign --verify --strict --deep "$APP" 2>/dev/null && { echo "a bundle whose CLI changed after signing verified"; exit 1; }
check "$APP/Contents/MacOS/monoagentcli" "$APP/Contents/MacOS/monoagentcli" && exit 1
echo "hardened-runtime check: ok"
