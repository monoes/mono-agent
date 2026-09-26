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

echo "regress-cli: $FAILS failure(s)"
[ "$FAILS" = 0 ]
