# CDP over the extension bridge (GLU-01 / RIG-07)

Three browser drivers existed in these two repos, and every capability cost
one implementation and reached one of them. `browser_console` could not see
the tab the user was logged into; the extension could not produce a web-vitals
report.

The seam is **CDP**. The extension already holds Chrome's `debugger`
permission and already speaks CDP to the user's real tabs; monobrowse's
`CdpClient` is the one choke point every instrument goes through. Putting a
transport under that client, rather than an interface over the instruments,
means console, network, HAR, vitals, trace, CPU profiler, snapshot,
screenshot and emulation reach the logged-in browser unchanged.

## The path

```
monobrowse instrument
  → CdpClient                       packages/@monoes/monobrowse/src/browser/cdp.ts
  → BridgeTransport                 …/src/browser/bridge.ts
  → ws  /monoagent/cdp              internal/extension/cdp.go
  → ws  /monoagent (extension)      chrome-extension/cdp_proxy.js
  → chrome.debugger.sendCommand     the user's tab
```

Events come back the other way unasked-for: `chrome.debugger.onEvent` →
`{"type":"cdp_event"}` frame → fanned out to every listening relay client →
delivered to `CdpClient` as an ordinary CDP event. That direction is why the
relay is a socket and not another `/monoagent/relay` call — console messages,
network responses and trace chunks are nobody's reply.

The relay knows nothing about CDP: framing, ids, sessions and timeouts all
live in `bridge.ts`. It enforces two things nothing downstream can — the same
token as `/monoagent/relay`, and that only `cdp`/`cdp_attach`/`cdp_detach`
travel this socket, so it cannot quietly become a second path to `get_cookies`
and `eval`.

## Sessions

Against a raw socket, monobrowse attaches to a target and carries the
resulting `sessionId` on every command and event. `chrome.debugger` has no
such id for a tab: the debuggee **is** the page session, and a `sessionId` on
the wire means a *child* session (an out-of-process iframe).

`BridgeTransport` mints `bridge-session-<tabId>`, strips it on the way out,
stamps it onto relayed events on the way in, and passes any other id through
untouched. `Target.attachToTarget` against the synthetic target id is answered
locally; against any other target it is forwarded, so real OOPIF attachment
still works.

## What MV3's `chrome.debugger` cannot do

`chrome.debugger` exposes a fixed set of domains: Accessibility, Audits,
CacheStorage, Console, CSS, Database, Debugger, DOM, DOMDebugger, DOMSnapshot,
Emulation, Fetch, IO, Input, Inspector, Log, Network, Overlay, Page,
Performance, Profiler, Runtime, Storage, Target, Tracing, WebAudio, WebAuthn.
Everything else is refused.

Measured against what monobrowse actually sends, two gaps fall out:

| Domain | Used by | Over the bridge |
|---|---|---|
| `HeapProfiler` | `profiler.ts` heap snapshots, `browser_profile` | **Unavailable** — not an exposed domain |
| `Browser` | `closeBrowser()`, launch/reap paths | **Unavailable**, and meaningless here: this is the user's browser, not ours to close |

Everything else each instrument sends — `Log`, `Runtime`, `Network`, `Fetch`,
`Page`, `DOM`, `Accessibility`, `Emulation`, `Profiler` (CPU), `Tracing` — is
on the supported list. A refused command comes back as an error from Chrome
and is relayed as an error, not papered over.

Other limits, all Chrome's:

- **Attaching shows the user a banner** — "MonoAgent Bridge started debugging
  this browser" — on the tab, for as long as the debugger is attached. The
  user can click Cancel, which detaches; that arrives as `Inspector.detached`
  with reason `canceled_by_user` and fails in-flight commands immediately
  rather than letting them time out. `--silent-debugger-extension-api` or a
  force-install policy suppresses the banner; nothing in this code can.
- **DevTools and the debugger are exclusive.** Opening DevTools on an attached
  tab detaches us (`onDetach`), and attaching to a tab that already has
  DevTools open fails.
- **Some pages refuse attachment** — `chrome://` pages, the Web Store, other
  extensions' pages. Enterprise `ExtensionSettings` blocked-hosts policy
  refuses everything with "Host access is restricted by policy."
- `Target.getBrowserContexts` and similar browser-scoped commands return
  "Not Allowed" even though `Target` is a supported domain.

## Size and latency

The bridge caps every frame at 32 MiB (`maxMessageSize`,
`internal/extension/server.go`) and drops the connection on a bigger one — so
an oversized screenshot would not merely fail, it would take the extension's
socket down. `cdp_proxy.js` refuses results over 30 MiB with an error, and
drops oversized *events* while emitting a `MonoAgent.eventDropped` marker, so
a truncated trace is visible rather than silent.

Measured on the Go relay hop alone (`go test ./internal/extension/ -run '^$'
-bench CdpRelay`, AMD Ryzen AI 9 HX 470):

| Result payload | Round trip | Allocations |
|---|---|---|
| empty | 0.05 ms | 6 KB |
| 64 KiB | 1.8 ms | 563 KB |
| 1 MiB | 5.7 ms | 9.4 MB |
| 8 MiB | 31 ms | 80 MB |

Two things that measurement does not include: the extension's own JSON
handling plus `chrome.debugger` IPC, and — the term that dominates when it
applies — waking an idle MV3 service worker. The existing 20 s keep-alive ping
and 30 s alarm keep it warm in practice.

Note the allocation column: a large payload costs roughly ten times its own
size in transient allocations, because it is decoded and re-encoded at the
relay. Small, frequent commands are cheap; multi-megabyte ones are not. Prefer
the instruments that stream (console, network, trace chunks) over ones that
return one large blob.

A subscribed tab is pinned against the background worker's idle sweep, which
otherwise detaches a debugger after 30 s without a *command* — exactly what a
capture that only listens looks like.

## Status

Working and tested: the transport seam, the bridge transport, the relay, the
extension proxy, and console / network / vitals driven over a fake bridge
end-to-end.

Not yet wired: choosing the bridge backend from the CLI or the MCP tools.
`getConnection()` in `packages/@monomind/cli/src/mcp-tools/browser-session.ts`
hardcodes `connectToTarget(port)`; swapping in `connectBridge()` there is the
whole of what `browser_console`, `browser_network` and `browser_vitals` need,
since each handler only ever takes `{ client, cdpSessionId }` from it.
`monobrowse report`'s `collect(client, sessionId, options)` is likewise
backend-agnostic — only its CLI entry point picks a backend.
