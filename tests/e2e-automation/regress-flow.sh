#!/bin/bash
# Record → analyze → verify → save → run, plus the security, trust, rerecord,
# side panel and bundle checks, against the fixture site through the private
# bridge. Needs the stack from setup.sh. Prints PASS/FAIL lines, exits 1 on
# any failure.
#
#   E2E_REC=<id>     reuse an existing recording instead of recording one
#   E2E_REAL_AI=1    also run one real `record analyze` (monomind + claude; costs money)
source "$(dirname "$0")/env.sh"
R="$E2E_WORK/flow"; F="$E2E_WORK/fixtures"; mkdir -p "$R"
python3 "$E2E_DIR/make_fixtures.py" "$F" "$E2E_SITE"
B() { node "$E2E_DIR/browser.mjs" "$@"; }
LOG="$E2E_WORK/requests.log"
mark() { wc -l < "$LOG"; }
posts_since() { tail -n +$(( $1 + 1 )) "$LOG" | grep -c '"m":"POST","p":"/api/contacts"'; }
hits_since() { tail -n +$(( $1 + 1 )) "$LOG" | grep -c "\"host\":\"$2"; }
STUB="$E2E_DIR/stub-monomind.mjs"
DR="$E2E_HOME/.monoagent/recording-drafts"

# --- 1. record through the real side panel
REC="${E2E_REC:-$(B record 2>$R/record.err | tail -1)}"
check record.id "$([ -n "$REC" ] && echo ok)" ok "$REC"
RD="$E2E_HOME/.monoagent/recordings/$REC"
check record.events "$(j $RD/meta.json 'd["eventCount"] >= 10')" True
check record.source "$(j $RD/meta.json 'd["source"]')" recording
check record.password-masked "$(sed -n 2p $RD/events.jsonl | python3 -c 'import json,sys;e=json.load(sys.stdin);print(e.get("masked"), "value" in e)')" "True False"
check record.dir-mode "$(stat -c %a $RD)" 700

# --- 2. analyze
if [ "${E2E_REAL_AI:-}" = 1 ]; then
  m record analyze $REC --json > $R/analyze-real.json 2>$R/analyze-real.err; check analyze.real.rc $? 0 "$(j $R/analyze-real.json 'd["draft"]["action"], d["draft"]["lint"]')"
  n=$(mark); m record verify $DR/$REC --full --input name="Real AI" --input email=realai@example.com --json > $R/real-full.json 2>/dev/null
  check verify.real-draft.full.rc $? 0; check verify.real-draft.full.posts "$(posts_since $n)" 1
fi
MONOMIND_BIN=$STUB m record analyze $REC --json > $R/analyze.json 2>/dev/null; check analyze.stub.rc $? 0
check analyze.lint-empty "$(j $R/analyze.json 'd["draft"]["lint"]')" "[]"

# --- 3. verify
n=$(mark)
m record verify $DR/$REC --input name=Safe --input email=safe@example.com --json > $R/safe.json 2>/dev/null; check verify.safe.rc $? 0
check verify.safe.stops-before-save "$(j $R/safe.json '[s["status"] for s in d["steps"]][3]')" stopped_before_side_effect
check verify.safe.highlighted "$(j $R/safe.json 'd["highlighted"]')" True
check verify.safe.no-post "$(posts_since $n)" 0
m record verify $DR/$REC --full --input name=Full --input email=full@example.com --json > $R/full.json 2>/dev/null; check verify.full.rc $? 0
check verify.full.one-post "$(posts_since $n)" 1

# --- 4. save and run
m record save $DR/$REC --as action --new e2e-crm --json > $R/save.json 2>/dev/null; check save.rc $? 0 "$(j $R/save.json 'd["nodeType"]')"
m node schema e2e-crm.create_contact > $R/schema.json 2>/dev/null
check schema.fields "$(j $R/schema.json '[f["key"] for f in d["fields"]]')" "['username', 'email', 'name']"
n=$(mark); mt 200 node run e2e-crm.create_contact --config '{"email":"node@example.com","name":"Node Run"}' --output json > $R/run.json 2>/dev/null
check noderun.rc $? 0; check noderun.post "$(posts_since $n)" 1

# --- 5. a secret input from a 0600 inputs file
L="$DR/e2e-login-draft"; mkdir -p "$L"; cp -r $F/login-draft/. "$L/"
python3 - "$DR/$REC/draft.json" "$L/draft.json" <<'EOF'
import json,sys
d=json.load(open(sys.argv[1])); d.update(action='sign_in',targetAutomation='e2e-crm',names={'automation':'e2e-crm','action':'sign_in','fragment':'sign_in'},recordedInputs={'username':'alice'},lint=[])
json.dump(d,open(sys.argv[2],'w'))
EOF
m record verify $L --inputs-file $F/pw-right.json --json > $R/pw-right.json 2>/dev/null; check verify.secret.right.rc $? 0
m record verify $L --inputs-file $F/pw-wrong.json --json > $R/pw-wrong.json 2>/dev/null; check verify.secret.wrong.rc $? 1
check verify.failed-report-has-steps "$(j $R/pw-wrong.json '[s["status"] for s in d["steps"]][3]')" fail
chmod 644 $F/pw-right.json; m record verify $L --inputs-file $F/pw-right.json --json >/dev/null 2>&1; check inputs-file.0644-refused.rc $? 1; chmod 600 $F/pw-right.json

# --- 6. domains and page scripts are enforced at run time
m automation install $F/e2e-offdom --yes --json >/dev/null 2>&1
run() { mt 120 node run "$1" --config "$2" --output json > $R/run-out.json 2>&1; }
n=$(mark)
run e2e-offdom.go "{\"url\":\"$E2E_SITE/contacts\"}"; check offdom.same-site.rc $? 0
run e2e-offdom.go "{\"url\":\"http://evil.e2e.test:$E2E_FIXTURE_PORT/contacts\"}"; check offdom.navigate-off-domain.rc $? 1
run e2e-offdom.fetch "{\"url\":\"$E2E_SITE/__state\"}"; check scripts.fetch-without-trust.rc $? 1
m automation trust e2e-offdom --scripts --json >/dev/null 2>&1
run e2e-offdom.fetch "{\"url\":\"$E2E_SITE/__state\"}"; check scripts.fetch-with-trust.rc $? 0
run e2e-offdom.fetch "{\"url\":\"http://evil.e2e.test:$E2E_FIXTURE_PORT/__state\"}"; check offdom.fetch-off-domain.rc $? 1
check offdom.no-request-reached-evil "$(hits_since $n evil.e2e.test)" 0

# --- 7. uploads are confined to ~/.monoagent/uploads/<package>/
m automation install $F/e2e-upload --yes --json >/dev/null 2>&1
UP="$E2E_HOME/.monoagent/uploads/e2e-upload"; mkdir -p "$UP"; echo inside > "$UP/good.txt"; echo outside > "$E2E_WORK/outside.txt"
ln -sfn "$E2E_HOME/.monoagent/extension.token" "$UP/token-link.txt"; echo other > "$E2E_HOME/.monoagent/uploads/other.txt"
run e2e-upload.put "{\"file\":\"$UP/good.txt\"}"; check upload.inside.rc $? 0
for f in "$E2E_WORK/outside.txt" "$UP/token-link.txt" "$UP/../other.txt" "good.txt"; do
  run e2e-upload.put "{\"file\":\"$f\"}"; rc=$?; check "upload.refused[$(basename "$f")].rc" $rc 1
done
UD="$DR/e2e-upload-draft"; mkdir -p "$UD"; cp -r $F/e2e-upload/. "$UD/"
python3 - "$L/draft.json" "$UD/draft.json" <<'EOF'
import json,sys
d=json.load(open(sys.argv[1])); d.update(action='put',targetAutomation='e2e-upload',names={'automation':'e2e-upload','action':'put','fragment':'put'},recordedInputs={})
json.dump(d,open(sys.argv[2],'w'))
EOF
m record verify $UD --input file="$E2E_WORK/outside.txt" --json >/dev/null 2>&1; check upload.safe-verify-refuses-outside.rc $? 1
m record verify $UD --full --input file="$UP/good.txt" --json >/dev/null 2>&1; check upload.full-verify-inside.rc $? 0

# --- 8. extension → Go requests
B request record.list '{}' > $R/req-list.json; check bridge.record.list "$(j $R/req-list.json 'd["ok"]')" True
B request record.analyze '{"recordingId":"../../etc"}' > $R/req-bad.json; check bridge.analyze.bad-id "$(j $R/req-bad.json 'd["code"]')" bad_params
ln -sfn "$F/e2e-offdom" "$DR/evil-link"
for d in "/etc" "$DR/../automations" "$DR/evil-link" "$DR/no-such-draft" "--full"; do
  B request record.verify "{\"draftDir\":\"$d\"}" > $R/req-v.json
  check "bridge.verify.refused[$d]" "$(j $R/req-v.json 'd["error"]')" "draft not found or outside the drafts folder"
done

# --- 9. side panel: analyze → verify → save, then a failed verify shows its steps
B panel-review > $R/panel.json 2>$R/panel.err
check panel.review.saved "$(j $R/panel.json 'd["save"].startswith("Saved as")')" True
python3 - "$DR/$REC/selectors.json" <<'EOF'
import json,sys
p=sys.argv[1]; s=json.load(open(p)); s['contact.name_input']['candidates']=[{'css':'#no-such-field','score':0.9}]; json.dump(s,open(p,'w'))
EOF
B panel-verify > $R/panel-fail.json 2>/dev/null
check panel.failed-verify.steps-shown "$(j $R/panel-fail.json 'd["stepsHidden"] is False and any("fail" in s for s in d["steps"])')" True "$(j $R/panel-fail.json 'd["msg"][:80]')"

# --- 10. rerecord one selector by clicking it (recorded package → package)
# rr <key> <url> click|esc|value|none <timeout> [selector]: run rerecord and act on its picker tab
rr() {
  local before; before=$(B tabids)
  (m automation rerecord e2e-crm "$1" --url "$2" --timeout "$4" --json > $R/rr.json 2>/dev/null; echo $? > $R/rr.rc) &
  if [ "$3" != none ]; then E2E_BEFORE="$before" B pick "${2#*://*/}" "$3" "$5" >/dev/null; fi
  wait
}
rr contact.name_input "$E2E_SITE/contacts/new" click 60s "#contact-name"
check rerecord.click.rc "$(cat $R/rr.rc)" 0; check rerecord.click.where "$(j $R/rr.json 'd["where"]')" package
rr contact.name_input "$E2E_SITE/contacts/new" esc 60s; check rerecord.esc "$(j $R/rr.json 'd["error"]')" cancelled
rr contact.name_input "$E2E_SITE/contacts/new" none 5s; check rerecord.timeout "$(j $R/rr.json 'd["error"]')" timeout
# a password field: the picker must return candidates only, never the typed value
m record save $L --as action --automation e2e-crm --name sign_in --keep-package-selectors --json >/dev/null 2>&1; check save.sign_in.rc $? 0
rr login.password "$E2E_SITE/login" value 60s "#password"
check rerecord.password.rc "$(cat $R/rr.rc)" 0
check rerecord.password.no-value "$(grep -c "$E2E_PASSWORD" $R/rr.json)" 0
m automation doctor e2e-crm --json > $R/doctor.json 2>/dev/null
check doctor.after-rerecord "$(j $R/doctor.json '[s["status"] for s in d["automations"][0]["selectors"] if s["key"]=="contact.name_input"][0]')" ok

# --- 11. bundle a recorded workflow, import it elsewhere, first live run needs trust
m record save $DR/$REC --as workflow --automation e2e-crm --name create_contact_wf --keep-package-selectors --json > $R/wf-save.json 2>/dev/null
W=$(j $R/wf-save.json 'd["workflowId"]'); check workflow.saved "$([ -n "$W" ] && echo ok)" ok
m workflow export $W --bundle-automations -o $R/bundle.json >/dev/null 2>&1; check bundle.export.rc $? 0
H2="$E2E_WORK/home2"; mkdir -p "$H2/.monoagent"; cp "$E2E_HOME/.monoagent/extension.token" "$H2/.monoagent/"
m2() { HOME="$H2" MONOAGENT_EXTENSION_PORT="$E2E_BRIDGE_PORT" PATH="$E2E_WORK/bin:$PATH" "$E2E_BIN" "$@"; }
m2 workflow import --file $R/bundle.json --json > $R/imp0.json 2>/dev/null
check bundle.import.review "$(j $R/imp0.json 'd["automations"][0]["status"]')" missing
m2 workflow import --file $R/bundle.json --yes --json > $R/imp1.json 2>/dev/null
m2 workflow import --file $R/bundle.json --yes --json > $R/imp2.json 2>/dev/null
check bundle.import.idempotent "$(j $R/imp2.json 'd["status"]')" unchanged
m2 workflow activate $W >/dev/null 2>&1
n=$(mark); m2 workflow run $W --timeout 2m --json >/dev/null 2>&1; check trust.first-live-run-refused.rc $? 1
check trust.refused-run-no-post "$(posts_since $n)" 0
m2 automation trust e2e-crm --live --json >/dev/null 2>&1
m2 workflow run $W --timeout 2m --json >/dev/null 2>&1; check trust.after-live.rc $? 0
check trust.after-live.post "$(posts_since $n)" 1

# --- 12. the fixture password never lands anywhere
leaks=$(grep -rl --exclude=pw-right.json "$E2E_PASSWORD" "$E2E_HOME" "$H2" "$R" "$E2E_WORK/logs" 2>/dev/null | head -3)
check password.never-stored "$leaks" ""

echo "regress-flow: $FAILS failure(s)"
[ "$FAILS" = 0 ]
