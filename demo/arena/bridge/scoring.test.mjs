import test from 'node:test';
import assert from 'node:assert/strict';
import { freshState, applyEvent, snapshot, leaderboard } from './scoring.mjs';

const CONFIGS = {
  forge: { run_config: { budget_tokens: 1000 }, roles: [{ id: 'cto', title: 'CTO', type: 'boss' }] },
  anvil: { run_config: { budget_tokens: 1000 }, roles: [{ id: 'cto', title: 'CTO', type: 'boss' }] },
  herald: { run_config: { budget_tokens: 1000 }, roles: [{ id: 'editor', title: 'Editor', type: 'boss' }] },
};

test('asset scores only on first write to a path (closes the edit-farming loophole)', () => {
  const s = freshState(CONFIGS);
  applyEvent(s, 'forge', { type: 'asset', from: 'dev', path: 'a.go', ts: 1 });
  applyEvent(s, 'forge', { type: 'asset', from: 'dev', path: 'a.go', ts: 2 });
  applyEvent(s, 'forge', { type: 'asset', from: 'dev', path: 'a.go', ts: 3 });
  assert.equal(s.orgs.forge.score, 100); // one impl-file credit, not three
});

test('message and xorg points are capped per rolling minute', () => {
  const s = freshState(CONFIGS);
  for (let i = 0; i < 10; i++) applyEvent(s, 'forge', { type: 'message', from: 'cto', ts: i * 1000 });
  assert.equal(s.orgs.forge.score, 60); // capped at 6 * 10
  for (let i = 0; i < 10; i++) applyEvent(s, 'anvil', { type: 'xorg', from: 'anvil:cto', to: 'herald:editor', ts: i * 1000 });
  assert.equal(s.orgs.anvil.score, 75); // capped at 3 * 25
});

test('herald is excluded from the leaderboard (house, not a competitor)', () => {
  const s = freshState(CONFIGS);
  applyEvent(s, 'herald', { type: 'asset', from: 'writer', path: 'docs/arena/launch.md', ts: 1 });
  const board = leaderboard(s);
  assert.ok(!board.some((r) => r.name === 'herald'));
});

test('a pending tool-approval audit is NOT scored as a fence Firewall', () => {
  const s = freshState(CONFIGS);
  applyEvent(s, 'anvil', {
    type: 'audit', from: 'dev', reason: 'decision-trace', ts: 1,
    data: { decisionType: 'tool', context: 'tool call: Bash', reasoning: 'Tool "Bash" is pending human approval', outcome: 'denied' },
  });
  assert.equal(s.orgs.anvil.achievements.has('firewall'), false);
  assert.equal(s.orgs.anvil.score, 0);
});

test('a genuine fence block IS scored as Firewall', () => {
  const s = freshState(CONFIGS);
  applyEvent(s, 'anvil', {
    type: 'audit', from: 'dev', reason: 'decision-trace', ts: 1,
    data: { decisionType: 'message', context: 'fence scanMessages blocked message body', outcome: 'denied' },
  });
  assert.equal(s.orgs.anvil.achievements.has('firewall'), true);
  assert.equal(s.orgs.anvil.score, 50);
});

test('a question with data.action is a tool approval, not an ask_human poll', () => {
  const s = freshState(CONFIGS);
  applyEvent(s, 'anvil', { type: 'question', from: 'dev', ts: 1, data: { question: 'Approval required for Bash', action: 'Bash' } });
  assert.equal(s.activeQuestion, null);
  applyEvent(s, 'anvil', { type: 'question', from: 'cto', ts: 2, data: { question: 'Ship with retry or without?' } });
  assert.ok(s.activeQuestion);
  assert.equal(s.activeQuestion.question, 'Ship with retry or without?');
});

test('herald (house) does not earn Diplomat/Ambassador just for routing replies to both studios', () => {
  const s = freshState(CONFIGS);
  for (let i = 0; i < 10; i++) {
    applyEvent(s, 'herald', { type: 'xorg', from: 'herald:editor', to: i % 2 ? 'forge:cto' : 'anvil:cto', ts: i * 10000 });
  }
  assert.equal(s.orgs.herald.achievements.has('diplomat'), false);
  assert.equal(s.orgs.herald.achievements.has('ambassador'), false);
});

test('a studio still earns Diplomat for its own first cross-org message', () => {
  const s = freshState(CONFIGS);
  applyEvent(s, 'forge', { type: 'xorg', from: 'forge:cto', to: 'herald:editor', ts: 1 });
  assert.equal(s.orgs.forge.achievements.has('diplomat'), true);
});

test('cardVotes survive into the snapshot sent to clients', () => {
  const s = freshState(CONFIGS);
  s.cardVotes = { mole: 3 };
  assert.deepEqual(snapshot(s).cardVotes, { mole: 3 });
});

test('snapshot is JSON-serializable', () => {
  const s = freshState(CONFIGS);
  applyEvent(s, 'forge', { type: 'asset', from: 'dev', path: 'a.go', ts: 1 });
  const json = JSON.stringify(snapshot(s));
  assert.doesNotThrow(() => JSON.parse(json));
});
