#!/bin/bash
# Package and CLI checks that need no browser. Prints one PASS/FAIL line per
# check and exits 1 if any failed. Uses $E2E_BIN against the scratch $E2E_HOME.
source "$(dirname "$0")/env.sh"
R="$E2E_WORK/cli"; F="$E2E_WORK/fixtures"; mkdir -p "$R"
python3 "$E2E_DIR/make_fixtures.py" "$F" "$E2E_SITE"

# --- package lifecycle (contracts §5)
m automation list --json > $R/list.json 2>/dev/null; check list.rc $? 0 "n=$(j $R/list.json 'len(d["automations"])')"
m automation show hackernews --json > $R/show.json 2>/dev/null; check show.rc $? 0
check show.visibility-arrays "$(j $R/show.json 'all(isinstance(a["visibility"],list) for a in d["actions"])')" True
m automation export hackernews -o $R/hn.mpkg --json >/dev/null 2>&1; check export.rc $? 0
m automation uninstall hackernews --json >/dev/null 2>&1; check uninstall.rc $? 0
m automation install $R/hn.mpkg --json >/dev/null 2>&1; check install.without-yes.rc $? 1
m automation install $R/hn.mpkg --dry-run --json >/dev/null 2>&1; check install.dry-run.rc $? 0
m automation install $R/hn.mpkg --yes --json >/dev/null 2>&1; check install.rc $? 0
m automation export hackernews -o $R/hn2.mpkg --json >/dev/null 2>&1; cmp -s $R/hn.mpkg $R/hn2.mpkg; check export.round-trip-identical $? 0
m automation uninstall hackernews --json >/dev/null 2>&1
m automation restore hackernews --json > $R/restore.json 2>/dev/null; check restore.rc $? 0
check restore.trust "$(j $R/restore.json 'd["info"]["trust"]')" builtin
m automation test hackernews --full --json > $R/hn-test.json 2>/dev/null; check test.hackernews.full.rc $? 0 "$(j $R/hn-test.json '[r["status"] for r in d["results"]]')"

# --- author a package from a template; fixture tests compare expect.json
T=$(date +%s); L=$R/e2e-list-$T; LB=$R/e2e-list-bad-$T
m automation new e2e-list --template list-scrape --dir $L --json >/dev/null 2>&1; check new.rc $? 0
m automation validate $L --json >/dev/null 2>&1; check validate.ok.rc $? 0
m automation validate $F/vis-bad --json > $R/vis-bad.json 2>/dev/null; check validate.invalid.rc $? 1 "$(j $R/vis-bad.json '[i["code"] for i in d["issues"]]')"
m automation pack $L -o $L.mpkg --json >/dev/null 2>&1; check pack.rc $? 0
m automation install $L.mpkg --yes --json >/dev/null 2>&1; check install.mpkg.rc $? 0
m automation test e2e-list --json >/dev/null 2>&1; check test.pass.rc $? 0
cp -r $L $LB
sed -i 's/First item/WRONG/' $LB/tests/list_items.expect.json
sed -i 's/"version": "0.1.0"/"version": "0.1.1"/' $LB/automation.json
m automation install $LB --yes --json >/dev/null 2>&1
m automation test e2e-list --json >/dev/null 2>&1; check test.mismatch.rc $? 1
m automation rollback e2e-list --json > $R/rb.json 2>/dev/null; check rollback.version "$(j $R/rb.json 'd["info"]["version"]')" 0.1.0

# --- your own local package: new --install, upgrade, replace, rollback
N=$R/e2e-own-$T
m automation new e2e-own --template basic --start-url "$E2E_SITE/contacts" --dir $N --install --json >/dev/null 2>&1; check local.new-install.rc $? 0
m automation new e2e-own --template basic --dir $N-dup --install --json >/dev/null 2>&1; check local.new-install-existing-id.rc $? 1
check local.new-install-existing-id.no-dir "$([ -d $N-dup ] && echo created || echo none)" none
cp -r $N $N-020; sed -i 's/"version": "0.1.0"/"version": "0.2.0"/' $N-020/automation.json
m automation install $N-020 --local --yes --json >/dev/null 2>&1; check local.upgrade-without-replace.rc $? 0
m automation install $N --local --yes --json >/dev/null 2>&1; check local.downgrade-needs-replace.rc $? 1
m automation new e2e-own --template basic --dir $N-same --json >/dev/null 2>&1; sed -i 's/"version": "0.1.0"/"version": "0.2.0"/' $N-same/automation.json
m automation install $N-same --local --yes --json >/dev/null 2>&1; check local.same-version-other-content-needs-replace.rc $? 1
m automation install $N-same --local --yes --replace --json >/dev/null 2>&1; check local.replace.rc $? 0
m automation rollback e2e-own --json > $R/own-rb.json 2>/dev/null; check local.rollback-after-replace.domains "$(j $R/own-rb.json 'd["info"]["domains"][0]')" crm.e2e.test

# --- install review: visibility, trust
m automation install $F/e2e-vis --dry-run --json > $R/vis-dry.json 2>/dev/null
check review.visibility "$(j $R/vis-dry.json 'sorted(d["review"].get("visibility",{}))')" "['profile_view_visible_to_owner', 'search_may_be_saved']"
m automation install $F/e2e-vis --yes --json >/dev/null 2>&1
m automation show e2e-vis --json > $R/vis-show.json 2>/dev/null
check show.visibility "$(j $R/vis-show.json '[a["visibility"] for a in d["actions"]][0]')" "['profile_view_visible_to_owner', 'search_may_be_saved']"

# --- unsafe archives are refused
for z in zip-dotdot zip-abs zip-nested zip-symlink; do
  m automation install $F/$z.mpkg --yes --json >/dev/null 2>&1; check archive.$z.refused.rc $? 1
done
test ! -e /tmp/zipslip-abs-pwned.txt; check archive.nothing-written $? 0

# --- workflow import never overwrites local work (D9)
m automation install $F/e2e-offdom --yes --json >/dev/null 2>&1
imp() { m workflow import --file "$1" ${2:+$2} --json > $R/imp.json 2>/dev/null; }
count_wf() { m workflow list --json 2>/dev/null | python3 -c 'import json,sys;print(len([w for w in json.load(sys.stdin) if w["name"]=="E2E import"]))'; }
WID=e2e-import-fixed-id
imp $F/wf-import.json; check import.first "$(j $R/imp.json 'd["status"]')" created
imp $F/wf-import.json; check import.identical "$(j $R/imp.json 'd["status"]')" unchanged
NODE=$(m workflow node list $WID --json 2>/dev/null | python3 -c 'import json,sys;print([n["id"] for n in json.load(sys.stdin) if n["node_type"]!="trigger.manual"][0])')
m workflow node set $WID $NODE --config "{\"url\":\"$E2E_SITE/contacts?edited=1\"}" >/dev/null 2>&1
m workflow import --file $F/wf-import.json --overwrite --json >/dev/null 2>&1; rc=$?
check import.overwrite-edited-refused "$([ $rc -ne 0 ] && echo refused || echo rc=$rc)" refused
imp $F/wf-import.json
check import.after-edit.copy "$(j $R/imp.json 'd["status"], d.get("copyOf"), d.get("copyReason")')" "created $WID id"
check import.after-edit.warning-says-id "$(j $R/imp.json '"id already exists and was edited locally" in d["warnings"][0]')" True
COPY=$(j $R/imp.json 'd["id"]')
imp $F/wf-import.json; check import.after-edit.again-unchanged "$(j $R/imp.json 'd["status"], d["id"]')" "unchanged $COPY"
imp $F/wf-import.json; check import.after-edit.one-copy-only "$(count_wf)" 2
check import.local-edit-kept "$(m workflow get $WID --json 2>/dev/null | python3 -c 'import json,sys;print([n["config"]["url"] for n in json.load(sys.stdin)["nodes"] if n["node_type"]!="trigger.manual"][0].endswith("edited=1"))')" True
imp $F/wf-import.json "--replace $WID"; check import.replace "$(j $R/imp.json 'd["status"], d["id"]')" "updated $WID"
python3 -c "import json,sys;d=json.load(open(sys.argv[1]));d['id']='e2e-import-same-name';d['nodes'][1]['config']['url']+='?other';json.dump(d,open(sys.argv[2],'w'))" $F/wf-import.json $R/wf-same-name.json
imp $R/wf-same-name.json; check import.same-name-other-content "$(j $R/imp.json 'd["status"], d.get("copyReason")')" "created name"

# --- legacy packages: narrow domain suggestions, startUrl keeps http, local-only
HL="$E2E_WORK/home-legacy"; mkdir -p "$HL/.monoagent/actions"; cp -r $F/legacy-actions/. "$HL/.monoagent/actions/"
ml() { HOME="$HL" PATH="$E2E_WORK/bin:$PATH" "$E2E_BIN" "$@"; }
ml automation show local-crmlegacy --json > $R/legacy-show.json 2>/dev/null; check legacy.show.rc $? 0
check legacy.suggested-domains-narrow "$(j $R/legacy-show.json 'd["manifest"]["legacy"]["suggestedDomains"]')" "['crm.e2e.test:$E2E_FIXTURE_PORT']"
check legacy.start-url-keeps-http "$(j $R/legacy-show.json 'd["manifest"]["site"]["startUrl"]')" "$E2E_SITE/"
ml automation export local-crmlegacy -o $R/legacy.mpkg --json >/dev/null 2>&1; check legacy.export-without-domains-refused.rc $? 1
ml automation export local-crmlegacy -o $R/legacy.mpkg --use-suggested-domains --json >/dev/null 2>&1; check legacy.export-suggested.rc $? 0
check legacy.exported-domains "$(python3 -c 'import zipfile,json,sys;print(json.loads(zipfile.ZipFile(sys.argv[1]).read("automation.json"))["site"]["domains"])' $R/legacy.mpkg)" "['crm.e2e.test:$E2E_FIXTURE_PORT']"
ml automation export local-locallegacy -o $R/local.mpkg --use-suggested-domains --json >/dev/null 2>&1; check legacy.local-only-not-exportable.rc $? 1

# --- partial bundles
LW=$(ml workflow create "Legacy wf" --json 2>/dev/null | python3 -c 'import json,sys;print(json.load(sys.stdin)["id"])')
LT=$(ml workflow node add $LW --type trigger.manual --name Start --json 2>/dev/null | python3 -c 'import json,sys;print(json.load(sys.stdin)["id"])')
LA=$(ml workflow node add $LW --type local-crmlegacy.add_contact --name A --config '{"email":"legacy@example.com","name":"Legacy"}' --json 2>/dev/null | python3 -c 'import json,sys;print(json.load(sys.stdin)["id"])')
LB=$(ml workflow node add $LW --type local-locallegacy.add_contact --name B --config '{"email":"local@example.com"}' --json 2>/dev/null | python3 -c 'import json,sys;print(json.load(sys.stdin)["id"])')
ml workflow connect $LW --from $LT:main --to $LA:main >/dev/null 2>&1; ml workflow connect $LW --from $LA:main --to $LB:main >/dev/null 2>&1
ml workflow export $LW --bundle-automations --automation-domains nosuch=crm.e2e.test -o $R/b-nosuch.json >/dev/null 2>&1; rc=$?
check bundle.unknown-automation-domains-id-refused "$([ $rc -ne 0 ] && [ ! -e $R/b-nosuch.json ] && echo refused || echo rc=$rc)" refused
ml workflow export $LW --bundle-automations --use-suggested-domains -o $R/b-legacy.json >/dev/null 2>&1; check bundle.export-suggested.rc $? 0
check bundle.local-only-left-out "$(j $R/b-legacy.json 'list(d["automations"]), d["unbundledAutomations"]["local-locallegacy"]["localOnly"]')" "['local-crmlegacy'] True"
HR="$E2E_WORK/home-recipient"; mkdir -p "$HR/.monoagent"
mr() { HOME="$HR" PATH="$E2E_WORK/bin:$PATH" "$E2E_BIN" "$@"; }
mr workflow import --file $R/b-legacy.json --json > $R/imp-legacy.json 2>/dev/null
check bundle.import.missing-excludes-local "$(j $R/imp-legacy.json 'd["missingAutomations"]')" "['local-crmlegacy']"
check bundle.import.local-only-item "$(j $R/imp-legacy.json '[(a["localOnly"], "Recreate" in a["hint"]) for a in d["automations"] if a["id"]=="local-locallegacy"][0]')" "(True, True)"
mr workflow import --file $R/b-legacy.json --yes --json > $R/imp-legacy2.json 2>/dev/null
check bundle.import.installed "$(j $R/imp-legacy2.json '[a["status"] for a in d["automations"] if a["id"]=="local-crmlegacy"][0]')" installed
check bundle.imported-start-url-http "$(mr automation show local-crmlegacy --json 2>/dev/null | python3 -c 'import json,sys;print(json.load(sys.stdin)["info"]["startUrl"])')" "$E2E_SITE/"

# --- a bundled package that differs from the installed one (same id and version)
m workflow export $WID --bundle-automations -o $R/b-offdom.json >/dev/null 2>&1; check bundle.export-offdom.rc $? 0
mr automation install $F/e2e-offdom-differs --yes --json >/dev/null 2>&1
mr workflow import --file $R/b-offdom.json --yes --json > $R/imp-differs.json 2>/dev/null
check bundle.differs.kept "$(j $R/imp-differs.json '[(a["status"], a.get("replaceable")) for a in d["automations"] if a["id"]=="e2e-offdom"][0]')" "('differs', True)"
check bundle.differs.not-installed "$(mr automation show e2e-offdom --json 2>/dev/null | python3 -c 'import json,sys;print(json.load(sys.stdin)["manifest"]["description"])')" "e2e fixture package, edited locally"
mr workflow import --file $R/b-offdom.json --replace-automations --yes --json > $R/imp-replace.json 2>/dev/null
check bundle.replace-automations "$(j $R/imp-replace.json '[a["status"] for a in d["automations"] if a["id"]=="e2e-offdom"][0]')" replaced
check bundle.replaced-content "$(mr automation show e2e-offdom --json 2>/dev/null | python3 -c 'import json,sys;print(json.load(sys.stdin)["manifest"]["description"])')" "e2e fixture package"
mr automation rollback e2e-offdom --json >/dev/null 2>&1; check bundle.replaced-can-roll-back $? 0

echo "regress-cli: $FAILS failure(s)"
[ "$FAILS" = 0 ]
