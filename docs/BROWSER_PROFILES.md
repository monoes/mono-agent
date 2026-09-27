# One browser per profile

Every browser profile with the MonoAgent Bridge extension installed (a Chrome
profile, an Edge profile, a separate browser) connects to the bridge on its
own. Bind each one to a monoagent profile and that profile's browser actions
(workflow browser nodes, `capture page`, `login`, `node run`, `application
apply`) run there, signed in as that browser's accounts.

Two profiles bound to two browsers run at the same time, including two
accounts on the same site.

## Set it up

1. Install the extension in each browser profile (`chrome://extensions` →
   Load unpacked → `chrome-extension/`) and pair it: paste the output of
   `monoagentcli extension pair` into the side panel's Connection settings.
2. In each browser's side panel, open **Connection settings → Automations in
   this browser** and choose the profile. Give the browser a name you'll
   recognise.
3. Check: `monoagentcli extension browsers`.

Or from a terminal: `monoagentcli extension bind "<browser name or id>" <profile>`
(`extension unbind <browser>` makes it a default browser again). Or in the
desktop app: **Settings → Browsers**.

## Which browser runs what

For a profile P, the bridge picks:

1. the most recently connected browser bound to P;
2. otherwise a browser bound to no profile (a *default browser*);
3. never a browser bound to a different profile. If that's all there is, the
   run fails with "no browser is set up for profile P" and the fix.

A run stays in the browser it started in, even if you rebind that browser
mid-run. Changing a binding never reconnects the browser, so it can't drop a
running workflow's commands.

Binding a browser also makes its profile the default for captures saved from
that browser.

## Seeing every profile at once

The desktop dashboard has a **This profile / All profiles** toggle. All
profiles adds up every profile's counts, labels each workflow, run and
account with its profile, and adds a Profiles card with a **Switch** button
for each. Rows from other profiles are read-only (run, stop and open act on
the active profile); **Switch** takes you there.

The CLI side is `--all-profiles` on `summary`, `workflow list`,
`workflow executions --all` and `org summary`.

## Upgrading

Old extensions (before 1.5.0) keep working as a single default browser. Old
bridges ignore bindings until restarted. After updating, restart the daemon
or `monoagentcli extension serve`, and reload the extension in every browser
profile.
