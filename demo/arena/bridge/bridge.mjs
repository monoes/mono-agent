#!/usr/bin/env node
// Org Arena bridge — the ONLY thing that talks to the org runtime. Never
// talks to agents; only ever reads the bus. See demo/arena/README.md.
//
// Usage:
//   node bridge.mjs live --root <path> [--orgs forge,anvil,herald] [--port 4300]
//   node bridge.mjs replay --root <path> --run-map forge=run-id,anvil=run-id,herald=run-id
//                          [--speed 12] [--port 4300]
//
// `--root` is the directory that contains .monomind/ (the worktree or repo
// root you ran `monomind org run` from).

import http from 'node:http';
import fs from 'node:fs';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { freshState, applyEvent, snapshot } from './scoring.mjs';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const WEB_DIR = path.join(HERE, '..', 'web');

function parseArgs(argv) {
  const mode = argv[0];
  const rest = argv.slice(1);
  const out = { mode, orgs: ['forge', 'anvil', 'herald'], port: 4300, speed: 12 };
  for (let i = 0; i < rest.length; i++) {
    const a = rest[i];
    if (a === '--root') out.root = rest[++i];
    else if (a === '--orgs') out.orgs = rest[++i].split(',');
    else if (a === '--port') out.port = Number(rest[++i]);
    else if (a === '--speed') out.speed = Number(rest[++i]);
    else if (a === '--run-map') {
      out.runMap = {};
      for (const pair of rest[++i].split(',')) {
        const [org, run] = pair.split('=');
        out.runMap[org] = run;
      }
    }
  }
  if (!out.root) { console.error('missing --root <path to .monomind parent>'); process.exit(1); }
  if (out.mode === 'replay' && !out.runMap) { console.error('replay mode needs --run-map forge=run-id,...'); process.exit(1); }
  return out;
}

function loadOrgConfig(root, org) {
  const live = path.join(root, '.monomind', 'orgs', `${org}.json`);
  const tracked = path.join(HERE, '..', 'orgs', `${org}.json`);
  const p = fs.existsSync(live) ? live : tracked;
  return JSON.parse(fs.readFileSync(p, 'utf-8'));
}

// ---------- SSE broadcast plumbing ----------
const clients = new Set();
let broadcastTimer = null;
function scheduleBroadcast(state) {
  if (broadcastTimer) return;
  broadcastTimer = setTimeout(() => {
    broadcastTimer = null;
    const payload = `data: ${JSON.stringify(snapshot(state))}\n\n`;
    for (const res of clients) res.write(payload);
  }, 150);
}

// ---------- live mode: tail `org events <org> --follow` ----------
function startLiveTail(root, org, onLine) {
  // --follow keeps this process alive for the whole show; a dead provider
  // or a stopped org just ends the child, which we log and move on from —
  // the show doesn't need every org up every second.
  const child = spawn('npx', ['-y', 'monomind@latest', 'org', 'events', org, '--follow'], { cwd: root });
  let buf = '';
  child.stdout.on('data', (chunk) => {
    buf += chunk.toString('utf-8');
    const lines = buf.split('\n');
    buf = lines.pop();
    for (const line of lines) {
      if (!line.trim()) continue;
      try { onLine(JSON.parse(line)); } catch { /* partial/non-JSON line, skip */ }
    }
  });
  child.on('exit', (code) => console.error(`[bridge] org events ${org} exited (${code})`));
  return child;
}

// ---------- replay mode: play back a saved bus.jsonl at N x speed ----------
function startReplay(root, org, runId, speed, onLine) {
  const busPath = path.join(root, '.monomind', 'orgs', org, runId, 'bus.jsonl');
  const lines = fs.readFileSync(busPath, 'utf-8').split('\n').filter(Boolean);
  const events = lines.map((l) => JSON.parse(l));
  if (!events.length) return;
  const t0 = events[0].ts;
  for (const ev of events) {
    const delay = (ev.ts - t0) / speed;
    setTimeout(() => onLine(ev), delay);
  }
  const totalMs = (events[events.length - 1].ts - t0) / speed;
  console.error(`[bridge] replay ${org}/${runId}: ${events.length} events over ${(totalMs / 1000).toFixed(1)}s`);
}

// ---------- HTTP + SSE + vote API ----------
function serveFile(res, filePath, contentType) {
  fs.readFile(filePath, (err, data) => {
    if (err) { res.writeHead(404); res.end('not found'); return; }
    res.writeHead(200, { 'Content-Type': contentType });
    res.end(data);
  });
}

function startServer(state, port) {
  const server = http.createServer((req, res) => {
    const url = new URL(req.url, `http://${req.headers.host}`);
    if (url.pathname === '/' ) return serveFile(res, path.join(WEB_DIR, 'index.html'), 'text/html');
    if (url.pathname === '/vote') return serveFile(res, path.join(WEB_DIR, 'vote.html'), 'text/html');
    if (url.pathname === '/events') {
      res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache', Connection: 'keep-alive' });
      res.write(`data: ${JSON.stringify(snapshot(state))}\n\n`);
      clients.add(res);
      req.on('close', () => clients.delete(res));
      return;
    }
    if (url.pathname === '/api/state') {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify(snapshot(state)));
      return;
    }
    if (url.pathname === '/api/vote' && req.method === 'POST') {
      let body = '';
      req.on('data', (c) => (body += c));
      req.on('end', () => {
        try {
          const { kind, choice } = JSON.parse(body || '{}');
          if (kind === 'card' && typeof choice === 'string') {
            state.cardVotes = state.cardVotes || {};
            state.cardVotes[choice] = (state.cardVotes[choice] || 0) + 1;
          } else if (kind === 'question' && state.activeQuestion) {
            const key = choice === 'yes' ? 'yes' : 'no';
            state.activeQuestion.tally[key] = (state.activeQuestion.tally[key] || 0) + 1;
          }
          scheduleBroadcast(state);
          res.writeHead(200, { 'Content-Type': 'application/json' });
          res.end(JSON.stringify({ ok: true }));
        } catch (e) {
          res.writeHead(400); res.end(String(e));
        }
      });
      return;
    }
    if (url.pathname === '/api/reset-votes' && req.method === 'POST') {
      state.cardVotes = {};
      scheduleBroadcast(state);
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end('{"ok":true}');
      return;
    }
    res.writeHead(404); res.end('not found');
  });
  server.listen(port, () => console.error(`[bridge] listening on http://localhost:${port}  (map: /, vote: /vote)`));
  return server;
}

// ---------- clear the active question 60s after it was raised ----------
function armQuestionClock(state) {
  setInterval(() => {
    if (state.activeQuestion && Date.now() > state.activeQuestion.deadline) {
      state.activeQuestion = null;
      scheduleBroadcast(state);
    }
  }, 1000);
}

// ---------- main ----------
const args = parseArgs(process.argv.slice(2));
const configs = {};
for (const org of args.orgs) configs[org] = loadOrgConfig(args.root, org);
const state = freshState(configs);
state.cardVotes = {};
armQuestionClock(state);

if (args.mode === 'live') {
  for (const org of args.orgs) {
    startLiveTail(args.root, org, (ev) => { applyEvent(state, org, ev); scheduleBroadcast(state); });
  }
} else if (args.mode === 'replay') {
  for (const org of args.orgs) {
    const runId = args.runMap[org];
    if (!runId) continue;
    startReplay(args.root, org, runId, args.speed, (ev) => { applyEvent(state, org, ev); scheduleBroadcast(state); });
  }
} else {
  console.error('usage: node bridge.mjs <live|replay> --root <path> [...]');
  process.exit(1);
}

startServer(state, args.port);
