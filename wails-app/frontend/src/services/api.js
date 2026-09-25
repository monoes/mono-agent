// Thin wrapper around Wails Go bindings with error handling.
import * as GoApp from '../wailsjs/go/main/App'
import { EventsOn } from '../wailsjs/runtime/runtime'

// Global error bus. Read methods degrade to safe defaults ([]/null/0) so pages
// keep rendering, but every failure is also broadcast on `api:error` so a toast
// can surface it — otherwise a dead backend is indistinguishable from empty data.
const errorBus = new EventTarget()

export function onApiError(callback) {
  const handler = (e) => callback(e.detail)
  errorBus.addEventListener('api:error', handler)
  return () => errorBus.removeEventListener('api:error', handler)
}

function reportError(op, e) {
  const message = e?.message || String(e)
  console.warn(`API error (${op}):`, e)
  errorBus.dispatchEvent(new CustomEvent('api:error', { detail: { op, message } }))
}

// notify surfaces a message on the same toast bus for non-API failures, so UI
// code can report errors without a blocking native alert().
export function notify(op, message) {
  errorBus.dispatchEvent(new CustomEvent('api:error', { detail: { op, message: String(message) } }))
}

// Wrap a binding call so failures are reported and fall back to `fallback`.
const guard = (op, fallback) => (e) => { reportError(op, e); return fallback }

// Mutation fallback: a thrown binding call (or unparseable output) becomes the
// same {error} shape the Go side returns, so callers have one check.
const asError = (e) => ({ error: e?.message || String(e) })

// Parses a StreamAIChat/StreamAgentChat result, throwing on the synchronous
// {"error": "..."} shape those two return when the chat process never even
// started (see the comment on streamAIChat below) — turns that JSON payload
// into an actual promise rejection so callers' existing catch blocks run.
function parseStreamResult(s) {
  const r = JSON.parse(s)
  if (r?.error) throw new Error(r.error)
  return r
}

export const api = {
  getDashboardStats:    () => GoApp.GetDashboardStats().catch(guard('dashboard stats', null)),
  listWorkflows:        () => GoApp.ListWorkflows().catch(guard('list workflows', [])),
  runWorkflow:          (id) => GoApp.RunWorkflow(id).catch(e => { reportError('run workflow', e); return `error: ${e}` }),
  runWorkflowWithInput: (id, input) => GoApp.RunWorkflowWithInput(id, input || '').catch(e => { reportError('run workflow', e); return `error: ${e}` }),
  // `workflow inputs --json`: which trigger fields a workflow reads, plus a
  // skeleton payload. Returns null rather than throwing — a run must not be
  // blocked because the question could not be asked.
  getWorkflowTriggerInputs: async (id) => {
    try {
      const raw = await GoApp.GetWorkflowTriggerInputs(id)
      const parsed = JSON.parse(raw)
      return parsed?.error ? null : parsed
    } catch (e) {
      console.warn('workflow inputs unavailable', e)
      return null
    }
  },
  setWorkflowActive:    (id, active) => GoApp.SetWorkflowActive(id, active),
  getRecentExecutions:  (limit = 20) => GoApp.GetRecentExecutions(limit).catch(guard('recent executions', [])),
  getWorkflowExecutions:(id, limit = 20) => GoApp.GetWorkflowExecutions(id, limit).catch(guard('workflow executions', [])),
  getExecutionDetail:   (id) => GoApp.GetExecutionDetail(id).catch(guard('execution detail', null)),
  cancelWorkflow:       (id) => GoApp.CancelWorkflow(id).catch(e => { reportError('cancel workflow', e); return `error: ${e}` }),
  getPeople:        (platform = '', search = '', limit = 50, offset = 0) => GoApp.GetPeople(platform, search, limit, offset).catch(guard('get people', null)),
  getPeopleCount:   (platform = '', search = '') => GoApp.GetPeopleCount(platform, search).catch(guard('people count', 0)),
  getSessions:      () => GoApp.GetSessions().catch(guard('get sessions', null)),
  deleteSession:    (id) => GoApp.DeleteSession(id),
  getSocialLists:   () => GoApp.GetSocialLists().catch(guard('social lists', null)),
  getTemplates:     () => GoApp.GetTemplates().catch(guard('templates', null)),
  getLogs:          () => GoApp.GetLogs().catch(guard('logs', [])),
  clearLogs:        () => GoApp.ClearLogs(),
  getDBPath:        () => GoApp.GetDBPath().catch(guard('db path', '')),
  exportData:       () => GoApp.ExportData(),
  isDBConnected:    () => GoApp.IsDBConnected().catch(() => false),
  isReady:          () => GoApp.IsReady().catch(() => false),
  openURL:          (url) => GoApp.OpenURL(url).catch(console.warn),
  getPersonDetail:      (id) => GoApp.GetPersonDetail(id).catch(guard('person detail', null)),
  getPersonInteractions:(id) => GoApp.GetPersonInteractions(id).catch(guard('person interactions', [])),
  getPersonPosts:   (personId) => GoApp.GetPersonPosts(personId).catch(guard('person posts', [])),
  getPersonMessages:(personId) => GoApp.GetPersonMessages(personId).catch(guard('person messages', [])),
  getAllPersonMessages:(limit) => GoApp.GetAllPersonMessages(limit ?? 200).catch(guard('all messages', [])),
  composePersonMessage:(personId, connectionId, subject, body, asDraft) => GoApp.ComposePersonMessage(personId, connectionId, subject, body, asDraft),
  getDraftPersonMessages: () => GoApp.GetDraftPersonMessages().catch(guard('draft messages', [])),
  sendDraftPersonMessage: (id) => GoApp.SendDraftPersonMessage(id),
  rejectDraftPersonMessage: (id) => GoApp.RejectDraftPersonMessage(id),
  getPendingPeopleApprovals: () => (GoApp.GetPendingPeopleApprovals ? GoApp.GetPendingPeopleApprovals() : Promise.resolve([])).catch(guard('pending people', [])),
  approvePendingPerson: (id, intro = '', sendNow = false) => GoApp.ApprovePendingPerson(id, intro, sendNow),
  rejectPendingPerson: (id) => GoApp.RejectPendingPerson(id),
  getLatestPersonStatus: (personId) => GoApp.GetLatestPersonStatus(personId).catch(guard('latest status', null)),
  addPersonStatus:       (personId, text) => GoApp.AddPersonStatus(personId, text).catch(guard('add status', null)),
  getPersonStatusHistory:(personId, limit) => GoApp.GetPersonStatusHistory(personId, limit ?? 0).catch(guard('status history', [])),
  getPostDetail:    (postId)   => GoApp.GetPostDetail(postId).catch(guard('post detail', null)),
  getPostComments:  (postId)   => GoApp.GetPostComments(postId).catch(guard('post comments', [])),
  getAllTags:            ()  => GoApp.GetAllTags().catch(guard('tags', [])),
  getPersonTags:        (personId) => GoApp.GetPersonTags(personId).catch(guard('person tags', [])),
  addPersonTag:         (personId, name, color) => GoApp.AddPersonTag(personId, name, color).catch(guard('add tag', null)),
  updateTagColor:       (tagId, color) => (GoApp.UpdateTagColor ? GoApp.UpdateTagColor(tagId, color) : Promise.resolve(false)).catch(() => false),
  removePersonTag:      (personId, tagId) => GoApp.RemovePersonTag(personId, tagId).catch(e => reportError('remove tag', e)),
  getPeopleTagsMap:     (ids) => GoApp.GetPeopleTagsMap(ids).catch(guard('tags map', {})),
  listConnections:      (platform = '') => GoApp.ListConnections(platform).catch(guard('list connections', [])),
  listPlatforms:        (connectVia = '') => GoApp.ListPlatformsJSON(connectVia).then(s => JSON.parse(s)).catch(guard('list platforms', [])),
  testConnection:       (id) => GoApp.TestConnection(id).catch(e => { reportError('test connection', e); return `error: ${e}` }),
  testSession:          (id) => GoApp.TestSession(id).catch(e => { reportError('test session', e); return `error: ${e}` }),
  removeConnection:     (id) => GoApp.RemoveConnection(id).catch(e => { reportError('remove connection', e); return `error: ${e}` }),
  getConnectionsForPlatform: (platformID) => GoApp.GetConnectionsForPlatform(platformID).catch(guard('connections for platform', [])),
  saveConnectionDirect: (platformID, method, fieldValues) =>
    GoApp.SaveConnectionDirect(platformID, method, JSON.stringify(fieldValues))
      .catch(e => { reportError('save connection', e); return `error: ${e}` }),
  connectPlatformOAuth:   (platformID)                       => GoApp.ConnectPlatformOAuth(platformID),
  loginSocial:            (platform)                         => GoApp.LoginSocial(platform),
  confirmSocialLogin:     (platform)                         => GoApp.ConfirmSocialLogin(platform),
  getOAuthCredentials:    (platformID)                       => GoApp.GetOAuthCredentials(platformID).catch(guard('oauth credentials', '')),
  setOAuthCredentials:    (platformID, clientID, clientSecret) => GoApp.SetOAuthCredentials(platformID, clientID, clientSecret),
  // AI Providers
  listAIProviders:    () => GoApp.ListAIProviders().then(s => JSON.parse(s)).catch(guard('list AI providers', [])),
  saveAIProvider:     (provider) => GoApp.SaveAIProvider(JSON.stringify(provider)).then(s => JSON.parse(s)),
  deleteAIProvider:   (id) => GoApp.DeleteAIProvider(id).then(s => JSON.parse(s)),
  testAIProvider:     (id) => GoApp.TestAIProvider(id).then(s => JSON.parse(s)),
  getAIModels:        (providerID) => GoApp.GetAIModels(providerID).then(s => JSON.parse(s)).catch(guard('AI models', [])),
  getAIRegistry:      () => GoApp.GetAIRegistry().then(s => JSON.parse(s)).catch(guard('AI registry', [])),
  // Agent Chat (monomind delegation — local AI agent runtimes)
  scanAgentRuntimes:  () => GoApp.ScanAgentRuntimes().then(s => JSON.parse(s)).catch(guard('scan agent runtimes', null)),
  // binary is a runtime's ScanEntry.binary (from scanAgentRuntimes) — required
  // for antigravity/codex, which discover their own model catalog by shelling
  // out to themselves; harmless to omit for claude (curated list, ignores it).
  getAgentRuntimeModels: (runtimeID, binary) => GoApp.GetAgentRuntimeModels(runtimeID, binary || '').then(s => JSON.parse(s)).catch(guard('agent runtime models', [])),
  // New chat bindings (interactive-agent-chat plan §"Proposed Wails
  // bindings"). Every call goes through parseStreamResult, same as
  // streamAIChat/streamAgentChat above: a synchronous {"error":...} shape
  // must become a real rejection, never a resolved value the caller has to
  // remember to check. A business-status reply (e.g. StartChatTurn's
  // {ok:false,status:"busy"}) is NOT that shape, so it passes through as a
  // normal value for the caller to branch on.
  createChatConversation: (backend, workflowID, runtimeID, providerID, model) =>
    GoApp.CreateChatConversation(backend, workflowID, runtimeID, providerID, model).then(parseStreamResult),
  startChatTurn: (conversationID, turnID, message, tools, allowRuns) =>
    GoApp.StartChatTurn(conversationID, turnID, message, tools, allowRuns).then(parseStreamResult),
  stopChatTurn: (conversationID, turnID) =>
    GoApp.StopChatTurn(conversationID, turnID).then(parseStreamResult),
  listChatConversations: (cursor = '', limit = 50) =>
    GoApp.ListChatConversations(cursor, limit).then(parseStreamResult),
  getChatTurns: (conversationID, cursor = '', limit = 50) =>
    GoApp.GetChatTurns(conversationID, cursor, limit).then(parseStreamResult),
  getChatEvents: (conversationID, turnID, afterSeq = 0, limit = 200) =>
    GoApp.GetChatEvents(conversationID, turnID, afterSeq, limit).then(parseStreamResult),
  deleteChatConversation: (conversationID) =>
    GoApp.DeleteChatConversation(conversationID).then(parseStreamResult),
  // Orgs (monomind Org Runtime v2)
  listOrgs:           () => GoApp.ListOrgs().then(s => JSON.parse(s)).catch(guard('list orgs', null)),
  getOrgStatus:       (name = '') => GoApp.GetOrgStatus(name).then(s => JSON.parse(s)).catch(guard('org status', null)),
  getOrgLogs:         (name, run = '') => GoApp.GetOrgLogs(name, run).then(s => JSON.parse(s)).catch(guard('org logs', null)),
  getOrgReport:       (name, all = false, run = '') => GoApp.GetOrgReport(name, all, run).then(s => JSON.parse(s)).catch(guard('org report', null)),
  getOrgCosts:        (name, run = '') => GoApp.GetOrgCosts(name, run).then(s => JSON.parse(s)).catch(guard('org costs', null)),
  getOrgFlow:         (name, run = '') => GoApp.GetOrgFlow(name, run).then(s => JSON.parse(s)).catch(guard('org flow', null)),
  getOrgQuestions:    (name) => GoApp.GetOrgQuestions(name).then(s => JSON.parse(s)).catch(guard('org questions', null)),
  getOrgApprovals:    (name) => GoApp.GetOrgApprovals(name).then(s => JSON.parse(s)).catch(guard('org approvals', null)),
  getOrgGates:        (name) => GoApp.GetOrgGates(name).then(s => JSON.parse(s)).catch(guard('org gates', null)),
  getOrgDecisions:    (name, run = '') => GoApp.GetOrgDecisions(name, run).then(s => JSON.parse(s)).catch(guard('org decisions', null)),
  getOrgMemoryStats:  (name) => GoApp.GetOrgMemoryStats(name).then(s => JSON.parse(s)).catch(guard('org memory stats', null)),
  answerOrgQuestion:  (name, questionID, answer) => GoApp.AnswerOrgQuestion(name, questionID, answer).then(s => JSON.parse(s)),
  approveOrgAction:   (name, role, action) => GoApp.ApproveOrgAction(name, role, action).then(s => JSON.parse(s)),
  denyOrgAction:      (name, role, action) => GoApp.DenyOrgAction(name, role, action).then(s => JSON.parse(s)),
  gateApproveOrgAction: (name, gateID, resolution = '') => GoApp.GateApproveOrgAction(name, gateID, resolution).then(s => JSON.parse(s)),
  gateRejectOrgAction:  (name, gateID, resolution = '') => GoApp.GateRejectOrgAction(name, gateID, resolution).then(s => JSON.parse(s)),
  streamOrgEvents:    (orgName) => GoApp.StreamOrgEvents(orgName).then(s => JSON.parse(s)),
  stopOrgEvents:      (orgName) => GoApp.StopOrgEvents(orgName).then(s => JSON.parse(s)).catch(guard('stop org events', null)),
  runOrg:             (orgName, task = '') => GoApp.RunOrg(orgName, task).then(s => JSON.parse(s)),
  // Org Designer — direct config-file read/write, distinct from the org
  // observe/action surface above (which proxies `monoagentcli org <sub>`,
  // read-only + question/gate actions). See wails-app/app_orgs_design.go.
  getOrgDesign:        (name) => GoApp.GetOrgDesign(name).then(s => JSON.parse(s)).catch(guard('org design', null)),
  listOrgDesigns:      () => GoApp.ListOrgDesigns().then(s => JSON.parse(s)).catch(guard('org designs', null)),
  createOrgDesign:     (spec) => GoApp.CreateOrgDesign(JSON.stringify(spec)).then(s => JSON.parse(s)),
  deleteOrgDesign:     (name) => GoApp.DeleteOrgDesign(name).then(s => JSON.parse(s)),
  addOrgRole:          (name, role) => GoApp.AddOrgRole(name, JSON.stringify(role)).then(s => JSON.parse(s)),
  updateOrgRole:       (name, roleID, patch) => GoApp.UpdateOrgRole(name, roleID, JSON.stringify(patch)).then(s => JSON.parse(s)),
  removeOrgRole:       (name, roleID, strategy = 'reparent') => GoApp.RemoveOrgRole(name, roleID, strategy).then(s => JSON.parse(s)),
  setOrgRoleReportsTo: (name, roleID, parentID = '') => GoApp.SetOrgRoleReportsTo(name, roleID, parentID).then(s => JSON.parse(s)),
  promoteRoleToRoot:   (name, roleID) => GoApp.PromoteRoleToRoot(name, roleID).then(s => JSON.parse(s)),
  chooseInstructionsFile: () => GoApp.ChooseInstructionsFile(),
  saveOrgLayout:       (name, layout) => GoApp.SaveOrgLayout(name, JSON.stringify(layout)).then(s => JSON.parse(s)),
  saveOrgDesign:       (name, doc) => GoApp.SaveOrgDesign(name, JSON.stringify(doc)).then(s => JSON.parse(s)),
  validateOrgDesign:   (name) => GoApp.ValidateOrgDesign(name).then(s => JSON.parse(s)).catch(guard('validate org design', null)),
  reloadOrg:           (name) => GoApp.ReloadOrg(name).then(s => JSON.parse(s)),
  // Org × workflow unification — grants, automations, automation roles,
  // autonomy, holding groups. Every call shells `monoagentcli org …` (see
  // wails-app/app_org_unification.go). Reads fall back to null and toast;
  // mutations resolve to the CLI's JSON or {error} so callers check res.error.
  listOrgAutomations:        (org) => GoApp.ListOrgAutomations(org).then(s => JSON.parse(s)).catch(guard('org automations', null)),
  listUnassignedAutomations: () => GoApp.ListUnassignedAutomations().then(s => JSON.parse(s)).catch(guard('unassigned automations', null)),
  addOrgAutomation:          (org, workflowID, alias) => GoApp.AddOrgAutomation(org, workflowID, alias).then(s => JSON.parse(s)).catch(asError),
  removeOrgAutomation:       (org, alias) => GoApp.RemoveOrgAutomation(org, alias).then(s => JSON.parse(s)).catch(asError),
  listOrgGrants:             (org) => GoApp.ListOrgGrants(org).then(s => JSON.parse(s)).catch(guard('org grants', null)),
  setOrgGrant:               (org, spec) => GoApp.SetOrgGrant(org, JSON.stringify(spec)).then(s => JSON.parse(s)).catch(asError),
  removeOrgGrant:            (org, role, alias) => GoApp.RemoveOrgGrant(org, role, alias).then(s => JSON.parse(s)).catch(asError),
  addAutomationRole:         (org, spec) => GoApp.AddAutomationRole(org, JSON.stringify(spec)).then(s => JSON.parse(s)).catch(asError),
  removeAutomationRole:      (org, roleID) => GoApp.RemoveAutomationRole(org, roleID).then(s => JSON.parse(s)).catch(asError),
  getEffectiveTools:         (org, roleID) => GoApp.GetEffectiveTools(org, roleID).then(s => JSON.parse(s)).catch(guard('effective tools', null)),
  getOrgAutonomy:            (org) => GoApp.GetOrgAutonomy(org).then(s => JSON.parse(s)).catch(guard('org autonomy', null)),
  setOrgAutonomy:            (org, spec) => GoApp.SetOrgAutonomy(org, JSON.stringify(spec)).then(s => JSON.parse(s)).catch(asError),
  pauseOrgAutonomy:          (org, duration = '') => GoApp.PauseOrgAutonomy(org, duration).then(s => JSON.parse(s)).catch(asError),
  resumeOrgAutonomy:         (org) => GoApp.ResumeOrgAutonomy(org).then(s => JSON.parse(s)).catch(asError),
  listOrgDecisionLog:        (org, run = '') => GoApp.ListOrgDecisionLog(org, run).then(s => JSON.parse(s)).catch(guard('decision log', null)),
  listNeedsYou:              (org) => GoApp.ListNeedsYou(org).then(s => JSON.parse(s)).catch(guard('needs you', null)),
  // C-35: messages queued for the org's next start (read-only `org queued`).
  listOrgQueuedMessages:     (org) => GoApp.ListOrgQueuedMessages(org).then(s => JSON.parse(s)).catch(asError),
  startOrgGroup:             (holding) => GoApp.StartOrgGroup(holding).then(s => JSON.parse(s)).catch(asError),
  stopOrgGroup:              (holding) => GoApp.StopOrgGroup(holding).then(s => JSON.parse(s)).catch(asError),
  orgGroupStatus:            (holding) => GoApp.OrgGroupStatus(holding).then(s => JSON.parse(s)).catch(guard('org group status', null)),
  sendOrgMessage:            (org, spec) => GoApp.SendOrgMessage(org, JSON.stringify(spec)).then(s => JSON.parse(s)).catch(asError),
  getDaemonStatus:           () => GoApp.GetDaemonStatus().then(s => JSON.parse(s)).catch(guard('daemon status', null)),
  // Per-profile monomind setup — see wails-app/app_monomind_init.go.
  isMonomindInitialized:   () => GoApp.IsMonomindInitialized().catch(guard('monomind init status', false)),
  initializeMonomindProfile: () => GoApp.InitializeMonomindProfile().then(s => JSON.parse(s)),
  // Chat result artifacts (chat/chatArtifacts.js) — both already existed as
  // typed Go methods (GetWorkflow returns a real profile_id-checked error
  // for a deleted/cross-profile id; ListProfileDocuments is already
  // profile-scoped) with generated Wails bindings, just no api.js wrapper
  // yet. Typed struct returns, not JSON strings — no .then(JSON.parse).
  getWorkflow:            (id) => GoApp.GetWorkflow(id).catch(guard('get workflow', null)),
  listProfileDocuments:   () => GoApp.ListProfileDocuments().catch(guard('list profile documents', [])),
  getProfileDocument:     (id) => GoApp.GetProfileDocument(id).catch(guard('get profile document', null)),
}

// The Wails runtime (window.runtime / window.go) only exists inside the desktop
// shell. In a plain browser (vite dev server) EventsOn would throw, so no-op
// after one warning instead of crashing the page.
export function hasWails() {
  return typeof window !== 'undefined' && !!(window.runtime || window.go)
}

let warnedNoWails = false

// Subscribe via EventsOn and return its cancel function — removing by event
// name would kill ALL listeners for the event, including App-level ones.
export function subscribeEvent(name, callback) {
  if (!hasWails()) {
    if (!warnedNoWails) {
      warnedNoWails = true
      console.warn('Wails runtime not found — event subscriptions disabled (plain browser dev mode?)')
    }
    return () => {}
  }
  return EventsOn(name, callback)
}

export function onLogEntry(callback) {
  return subscribeEvent('log:entry', callback)
}

export function onConnectionProgress(callback) {
  return subscribeEvent('conn:progress', callback)
}

export function onConnectionDone(callback) {
  return subscribeEvent('conn:done', callback)
}

export function onConnectionOpened(callback) {
  return subscribeEvent('conn:opened', callback)
}

export function onAIChunk(callback) {
  return subscribeEvent('ai:chunk', callback)
}

export function onAITool(callback) {
  return subscribeEvent('ai:tool', callback)
}

export function onAIError(callback) {
  return subscribeEvent('ai:error', callback)
}

export function onOrgEvent(callback) {
  return subscribeEvent('org:event', callback)
}

export function onAgentSession(callback) {
  return subscribeEvent('agent:session', callback)
}

// onChatEvent streams the new GUI chat supervisor's journal — one call per
// chat:event envelope ({version,profileId,conversationId,turnId,seq,at,type,
// payload}), covering both the agent and provider backends. useChatStream.js
// is the sole consumer; it filters by conversationId/turnId itself rather
// than this helper doing it, so multiple independent subscribers (this
// panel's live turn view, a future activity/detail view) never fight over
// one disposer (see the EventsOff footgun this file's onLogEntry-style
// helpers already avoid).
export function onChatEvent(callback) {
  return subscribeEvent('chat:event', callback)
}

export function onOrgEventsClosed(callback) {
  return subscribeEvent('org:eventsClosed', callback)
}

// onOrgRunStatus fires when RunOrg's tracked subprocess starts/exits.
// Payload: {orgName, status: 'running' | 'stopped' | 'error'}.
export function onOrgRunStatus(callback) {
  return subscribeEvent('org:runStatus', callback)
}

// onOrgDesignUpdated fires for ANY change to an org's config file —
// an in-app canvas save, an AI chat tool call, or an external
// `monoagentcli org`/hand edit — via a single event name regardless of
// origin (payload.origin is "ui" | "external", diagnostic only, never
// branch UI behavior on it). Payload:
// {v, orgName, profileID, origin, deleted, valid, errors, org}.
export function onOrgDesignUpdated(callback) {
  return subscribeEvent('org:designUpdated', callback)
}

// onMonomindInitEvent streams InitializeMonomindProfile's progress.
// Payload: {kind: 'line'|'error'|'done', message}.
export function onMonomindInitEvent(callback) {
  return subscribeEvent('monomind:initProgress', callback)
}

// onDocumentsChanged fires when the background document watcher discovers
// or updates files under the active profile's folder. Payload:
// {profileID, added}. Triggers a reload; the event carries no document
// data itself (mirrors onOrgDesignUpdated's payload-is-diagnostic-only
// discipline) -- callers always just re-fetch via listProfileDocuments.
export function onDocumentsChanged(callback) {
  return subscribeEvent('documents:changed', callback)
}

export const PLATFORMS = ['INSTAGRAM', 'LINKEDIN', 'X', 'TIKTOK']
export const STATES = ['PENDING', 'RUNNING', 'PAUSED', 'COMPLETED', 'FAILED', 'CANCELLED']

export const PLATFORM_COLORS = {
  INSTAGRAM: '#e1306c',
  LINKEDIN:  '#0077b5',
  X:         '#e7e9ea',
  TIKTOK:    '#ff0050',
  EMAIL:     '#6366f1',
  TELEGRAM:  '#26a5e4',
}

export const STATE_COLORS = {
  PENDING:   '#94a3b8',
  RUNNING:   '#00f5d4',
  PAUSED:    '#eab308',
  COMPLETED: '#10b981',
  FAILED:    '#ef4444',
  CANCELLED: '#6b7280',
}
