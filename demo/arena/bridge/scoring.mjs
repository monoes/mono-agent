// Org Arena scoring — a pure reducer over bus events.
//
// GROUNDING NOTE (2026-09-16 live smoke test): the org runtime's bus does
// NOT carry tool stdout/exit codes. A Bash "go test" call only ever shows
// up as a `tool` event with `decision: "allow"` at invocation time — there
// is no follow-up event saying it passed or failed. So "tests pass" is
// inferred from the agent's own narration (a chat/status message matching
// /pass|green|ok\b/i shortly after a go-test Bash call), never verified
// independently. Say so on screen — don't present it as ground truth.
//
// Also grounded live: a pending tool-approval and a genuine fence block
// both emit `type: "audit", reason: "decision-trace"` with the same shape
// (`data.decisionType === "tool"`, `data.outcome === "denied"`). The only
// way to tell them apart is `data.context`/`data.reasoning` text — a fence
// block mentions "fence" or "scanMessages"; a routine approval mentions
// "pending human approval". The original design doc treated every `audit`
// event as a fence block; that was wrong and is fixed here.

export const ORG_TITLES = { forge: 'Forge', anvil: 'Anvil', herald: 'Herald' };
export const HOUSE_ORGS = new Set(['herald']); // scored for display, not the leaderboard

const MSG_CAP_PER_MIN = 6;
const XORG_CAP_PER_MIN = 3;
const MSG_POINTS = 10;
const XORG_POINTS = 25;
const TOOL_DENY_POINTS = -5;
const ACHIEVEMENT_POINTS = { firewall: 50, phoenix: 75, frugal: 50, diplomat: 0, ambassador: 0, 'first-word': 0 };

function assetKind(path) {
  if (/_test\.go$/.test(path)) return 'test';
  if (/_schema\.go$/.test(path)) return 'schema';
  if (/\.go$/.test(path)) return 'impl';
  if (/verdict-.*\.md$/.test(path)) return 'verdict';
  if (/launch\.md$/.test(path)) return 'launch';
  if (/\.md$/.test(path)) return 'docs';
  return 'other';
}

const ASSET_POINTS = { impl: 100, test: 100, schema: 25, docs: 50, verdict: 0, launch: 0, other: 10 };

export function freshOrgState(name, config) {
  const roles = {};
  for (const r of (config?.roles ?? [])) {
    roles[r.id] = { id: r.id, title: r.title ?? r.id, type: r.type ?? 'specialist', status: 'offline', lastTs: 0, gated: false };
  }
  return {
    name,
    title: ORG_TITLES[name] ?? name,
    budgetTokens: config?.run_config?.budget_tokens ?? 0,
    roles,
    tokensUsed: 0,
    costUsd: 0,
    score: 0,
    seenPaths: new Set(),
    assets: [],
    achievements: new Set(),
    msgWindow: [],   // timestamps of counted messages this minute
    xorgWindow: [],  // timestamps of counted xorg this minute
    xorgTotal: 0,
    respawns: 0,
  };
}

export function freshState(configs) {
  const orgs = {};
  for (const [name, cfg] of Object.entries(configs)) orgs[name] = freshOrgState(name, cfg);
  return {
    orgs,
    ticker: [],
    achievementsFeed: [],
    activeQuestion: null,
    activeGate: null,
    firstWordAt: null,
    firstXorgAt: null,
    startedAt: Date.now(),
    beats: [], // structured recent message/xorg/asset events — for courier animation, not display text
  };
}

function pushTicker(state, line) {
  state.ticker.push({ ts: Date.now(), line });
  if (state.ticker.length > 60) state.ticker.shift();
}

function pushBeat(state, beat) {
  state.beats.push({ ts: Date.now(), ...beat });
  if (state.beats.length > 40) state.beats.shift();
}

// Diplomat/Ambassador are studio bragging rights — Herald racks up xorg
// volume just by being the hub that replies to both studios, which would
// otherwise win it an achievement for doing its ordinary job. Excluded here
// the same way it's excluded from the leaderboard.
const STUDIO_ONLY_ACHIEVEMENTS = new Set(['diplomat', 'ambassador']);

function grantAchievement(state, org, key, label) {
  if (HOUSE_ORGS.has(org) && STUDIO_ONLY_ACHIEVEMENTS.has(key)) return;
  const o = state.orgs[org];
  if (!o || o.achievements.has(key)) return;
  o.achievements.add(key);
  o.score += ACHIEVEMENT_POINTS[key] ?? 0;
  state.achievementsFeed.push({ org, key, label, ts: Date.now() });
  pushTicker(state, `🏆 ${o.title}: ${label.toUpperCase()}`);
}

function countInWindow(windowArr, nowTs, cap) {
  const cutoff = nowTs - 60_000;
  while (windowArr.length && windowArr[0] < cutoff) windowArr.shift();
  return windowArr.length < cap;
}

// Applies one parsed bus event (already JSON.parse'd) to state in place,
// returning state for chaining. `org` is the org name this event came from
// (the bridge tags each line by which `org events` stream it read).
export function applyEvent(state, org, ev) {
  const o = state.orgs[org];
  if (!o) return state;
  const ts = ev.ts ?? Date.now();

  switch (ev.type) {
    case 'status': {
      if (ev.from && o.roles[ev.from]) {
        if (ev.data?.to) o.roles[ev.from].status = ev.data.to;
        o.roles[ev.from].lastTs = ts;
      }
      if (ev.reason === 'idle-nudge') {
        pushTicker(state, `⏱ ${o.title}: idle watchdog nudged ${ev.from ?? 'a role'}`);
      }
      break;
    }
    case 'message': {
      if (!state.firstWordAt) { state.firstWordAt = ts; grantAchievement(state, org, 'first-word', 'First Word'); }
      if (countInWindow(o.msgWindow, ts, MSG_CAP_PER_MIN)) {
        o.msgWindow.push(ts);
        o.score += MSG_POINTS;
      }
      pushTicker(state, `💬 ${o.title}/${ev.from ?? '?'}: ${ev.subject ?? ev.msg?.slice(0, 60) ?? ''}`);
      if (ev.to) pushBeat(state, { kind: 'message', org, from: ev.from, to: ev.to });
      break;
    }
    case 'xorg': {
      const fromOrg = (ev.from ?? '').split(':')[0];
      const scoringOrg = state.orgs[fromOrg] ? fromOrg : org;
      const so = state.orgs[scoringOrg];
      if (!state.firstXorgAt) { state.firstXorgAt = ts; grantAchievement(state, scoringOrg, 'diplomat', 'Diplomat'); }
      so.xorgTotal += 1;
      if (so.xorgTotal >= 10) grantAchievement(state, scoringOrg, 'ambassador', 'Ambassador');
      if (countInWindow(so.xorgWindow, ts, XORG_CAP_PER_MIN)) {
        so.xorgWindow.push(ts);
        so.score += XORG_POINTS;
      }
      pushTicker(state, `🛰 ${ev.from} → ${ev.to}: ${ev.subject ?? ''}`);
      pushBeat(state, { kind: 'xorg', org: scoringOrg, from: ev.from, to: ev.to });
      break;
    }
    case 'asset': {
      if (!ev.path) break;
      if (o.seenPaths.has(ev.path)) break; // first-write-per-path only — closes the pass-6 loophole
      o.seenPaths.add(ev.path);
      const kind = assetKind(ev.path);
      const pts = ASSET_POINTS[kind] ?? 0;
      o.score += pts;
      const fname = ev.path.split('/').pop();
      o.assets.push({ path: ev.path, fname, kind, points: pts, ts, from: ev.from });
      pushTicker(state, `📦 ${o.title}/${ev.from ?? '?'}: ${fname} (+${pts})`);
      pushBeat(state, { kind: 'asset', org, from: ev.from });
      break;
    }
    case 'tool': {
      if (ev.decision === 'deny') {
        o.score += TOOL_DENY_POINTS;
        pushTicker(state, `⛔ ${o.title}/${ev.from ?? '?'}: ${ev.tool ?? 'tool'} denied`);
      }
      break;
    }
    case 'audit': {
      // See grounding note at top of file: only a fence-worded context/reasoning
      // counts as a Firewall — a routine pending-approval audit does not.
      const text = `${ev.reason ?? ''} ${ev.data?.context ?? ''} ${ev.data?.reasoning ?? ''}`.toLowerCase();
      const isFenceBlock = /fence|scanmessages|blocked message/.test(text) && !/pending human approval/.test(text);
      if (isFenceBlock) {
        grantAchievement(state, org, 'firewall', 'Firewall'); // awards FENCE_BLOCK_POINTS via ACHIEVEMENT_POINTS.firewall
        pushTicker(state, `🚨 ${o.title}: fence blocked a message to ${ev.from ?? '?'}`);
      }
      break;
    }
    case 'usage': {
      const tokens = ev.data?.tokens ?? 0;
      const cost = ev.data?.cost_usd ?? 0;
      o.tokensUsed += tokens;
      o.costUsd += cost;
      break;
    }
    case 'question': {
      // Distinguish a real ask_human question from a routine tool-approval
      // prompt — both are `type: "question"` on the bus, but only ask_human
      // omits `data.action` (confirmed live 2026-09-16).
      if (!ev.data?.action) {
        state.activeQuestion = {
          id: ev.id, org, role: ev.from, question: ev.data?.question ?? '',
          ts, deadline: ts + 60_000, tally: {},
        };
        pushTicker(state, `❓ ${o.title}/${ev.from}: ${ev.data?.question ?? ''}`);
      }
      break;
    }
    case 'gate': {
      state.activeGate = { id: ev.id, org, role: ev.from, name: ev.name ?? ev.data?.name, description: ev.description ?? ev.data?.description, ts };
      if (ev.from && o.roles[ev.from]) o.roles[ev.from].gated = true;
      pushTicker(state, `🔒 ${o.title}/${ev.from}: gate raised — ${ev.name ?? ev.data?.name ?? ''}`);
      break;
    }
    default:
      break;
  }
  return state;
}

export function computePowerFraction(orgState) {
  if (!orgState.budgetTokens) return 1;
  return Math.max(0, 1 - orgState.tokensUsed / orgState.budgetTokens);
}

export function leaderboard(state) {
  return Object.values(state.orgs)
    .filter((o) => !HOUSE_ORGS.has(o.name))
    .map((o) => ({ name: o.name, title: o.title, score: Math.round(o.score * (1 + computePowerFraction(o))) }))
    .sort((a, b) => b.score - a.score);
}

// JSON-serializable snapshot for the SSE feed — Sets don't survive JSON.stringify.
export function snapshot(state) {
  const orgs = {};
  for (const [name, o] of Object.entries(state.orgs)) {
    orgs[name] = {
      name: o.name, title: o.title, budgetTokens: o.budgetTokens,
      roles: o.roles, tokensUsed: o.tokensUsed, costUsd: o.costUsd,
      score: Math.round(o.score), power: computePowerFraction(o),
      assets: o.assets.slice(-12), achievements: [...o.achievements],
    };
  }
  return {
    orgs, ticker: state.ticker.slice(-30), achievementsFeed: state.achievementsFeed.slice(-20),
    activeQuestion: state.activeQuestion, activeGate: state.activeGate,
    leaderboard: leaderboard(state), elapsedMs: Date.now() - state.startedAt,
    beats: state.beats.slice(-10),
    cardVotes: state.cardVotes ?? {},
  };
}
