package workflow

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"
)

// SchedulerInterface abstracts the scheduler for testability.
type SchedulerInterface interface {
	AddWorkflowJob(spec string, fn func()) (cron.EntryID, error)
	RemoveJob(id cron.EntryID)
}

// triggerEntry holds state for an active trigger registration.
// For schedule triggers it holds the cron EntryID; for webhook triggers the path.
type triggerEntry struct {
	kind        string // "schedule", "webhook", or "provider"
	cronID      cron.EntryID
	webhookPath string
	deactivate  func() // provider entries
}

// TriggerManager activates and deactivates triggers for workflows.
// It coordinates three trigger types: manual (no-op), schedule, and webhook.
type TriggerManager struct {
	store         WorkflowStore
	webhookServer *WebhookServer
	scheduler     SchedulerInterface
	triggerFn     func(workflowID string, nodeID string, items []Item)
	mu            sync.Mutex
	active        map[string]map[string]*triggerEntry // workflowID → nodeID → entry
	providers     map[string]TriggerSource            // node type → provider
	logger        zerolog.Logger
}

// NewTriggerManager creates a TriggerManager.
// triggerFn is called when any trigger fires with the workflowID, nodeID, and initial items.
func NewTriggerManager(
	store WorkflowStore,
	webhookServer *WebhookServer,
	scheduler SchedulerInterface,
	triggerFn func(workflowID string, nodeID string, items []Item),
	logger zerolog.Logger,
) *TriggerManager {
	return &TriggerManager{
		store:         store,
		webhookServer: webhookServer,
		scheduler:     scheduler,
		triggerFn:     triggerFn,
		active:        make(map[string]map[string]*triggerEntry),
		providers:     make(map[string]TriggerSource),
		logger:        logger,
	}
}

// RegisterProvider routes trigger nodes of nodeType to p.
func (tm *TriggerManager) RegisterProvider(nodeType string, p TriggerSource) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.providers[nodeType] = p
}

// activateProvider registers a provider-backed trigger node.
func (tm *TriggerManager) activateProvider(w *Workflow, node *WorkflowNode, p TriggerSource) error {
	if node.Config == nil {
		if err := node.ParseConfig(); err != nil {
			return fmt.Errorf("parse config: %w", err)
		}
	}
	tm.mu.Lock()
	if tm.active[w.ID] != nil {
		if _, exists := tm.active[w.ID][node.ID]; exists {
			tm.mu.Unlock()
			return nil
		}
	}
	tm.mu.Unlock()

	wfID, nID := w.ID, node.ID
	stop, err := p.Activate(w, node, func(items []Item) { tm.triggerFn(wfID, nID, items) })
	if err != nil {
		return err
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()
	if tm.active[w.ID] == nil {
		tm.active[w.ID] = make(map[string]*triggerEntry)
	}
	if _, exists := tm.active[w.ID][node.ID]; exists {
		// Lost a race with a concurrent activation; keep the first one.
		if stop != nil {
			stop()
		}
		return nil
	}
	tm.active[w.ID][node.ID] = &triggerEntry{kind: "provider", deactivate: stop}
	tm.logger.Info().Str("workflow_id", w.ID).Str("node_id", node.ID).Str("type", node.Type).Msg("provider trigger activated")
	return nil
}

// ActivateWorkflow registers all trigger nodes in the given workflow.
// It is idempotent: nodes already registered are skipped.
func (tm *TriggerManager) ActivateWorkflow(ctx context.Context, w *Workflow) error {
	var errs []error

	for i := range w.Nodes {
		node := &w.Nodes[i]

		if node.Disabled {
			continue
		}

		switch node.Type {
		case "trigger.manual":
			// Manual triggers are fired by direct API call — nothing to register.
			tm.logger.Debug().
				Str("workflow_id", w.ID).
				Str("node_id", node.ID).
				Msg("manual trigger: no background registration needed")

		case "trigger.schedule":
			if err := tm.activateSchedule(w.ID, node); err != nil {
				errs = append(errs, fmt.Errorf("node %s (%s): %w", node.ID, node.Type, err))
			}

		case "trigger.webhook":
			if err := tm.activateWebhook(w.ID, node); err != nil {
				errs = append(errs, fmt.Errorf("node %s (%s): %w", node.ID, node.Type, err))
			}

		default:
			tm.mu.Lock()
			p := tm.providers[node.Type]
			tm.mu.Unlock()
			if p != nil {
				if err := tm.activateProvider(w, node, p); err != nil {
					errs = append(errs, fmt.Errorf("node %s (%s): %w", node.ID, node.Type, err))
				}
			}
		}
	}

	if len(errs) > 0 {
		combined := errs[0]
		for _, e := range errs[1:] {
			combined = fmt.Errorf("%w; %v", combined, e)
		}
		return combined
	}
	return nil
}

// activateSchedule registers a cron job for a trigger.schedule node.
func (tm *TriggerManager) activateSchedule(workflowID string, node *WorkflowNode) error {
	key := node.ID

	// Parse config outside the lock — no shared state involved.
	if node.Config == nil {
		if err := node.ParseConfig(); err != nil {
			return fmt.Errorf("parse config: %w", err)
		}
	}

	spec, ok := node.Config["cron"].(string)
	if !ok || spec == "" {
		return fmt.Errorf("trigger.schedule: missing or invalid \"cron\" field in config")
	}
	// Always pin the timezone — the underlying cron instance evaluates specs
	// in time.Local when no CRON_TZ prefix is present, so a default-UTC
	// schedule would silently fire at local time on non-UTC hosts.
	tz, _ := node.Config["timezone"].(string)
	if tz == "" {
		tz = "UTC"
	}
	spec = fmt.Sprintf("CRON_TZ=%s %s", tz, spec)

	// Capture loop variables for the closure.
	wfID := workflowID
	nID := node.ID

	// Hold the mutex through the entire check-then-act to prevent TOCTOU races.
	tm.mu.Lock()
	if tm.active[workflowID] != nil {
		if _, exists := tm.active[workflowID][key]; exists {
			tm.mu.Unlock()
			tm.logger.Debug().
				Str("workflow_id", workflowID).
				Str("node_id", node.ID).
				Msg("schedule trigger already active, skipping")
			return nil
		}
	}

	entryID, err := tm.scheduler.AddWorkflowJob(spec, func() {
		items := []Item{
			{
				JSON: map[string]interface{}{
					"trigger_type": "schedule",
					"timestamp":    time.Now().UTC().Format(time.RFC3339),
				},
			},
		}
		tm.triggerFn(wfID, nID, items)
	})
	if err != nil {
		tm.mu.Unlock()
		return fmt.Errorf("add cron job with spec %q: %w", spec, err)
	}

	if tm.active[workflowID] == nil {
		tm.active[workflowID] = make(map[string]*triggerEntry)
	}
	tm.active[workflowID][key] = &triggerEntry{
		kind:   "schedule",
		cronID: entryID,
	}
	tm.mu.Unlock()

	tm.logger.Info().
		Str("workflow_id", workflowID).
		Str("node_id", node.ID).
		Str("cron", spec).
		Msg("schedule trigger activated")

	return nil
}

// activateWebhook registers a webhook route for a trigger.webhook node.
func (tm *TriggerManager) activateWebhook(workflowID string, node *WorkflowNode) error {
	key := node.ID

	// Parse config outside the lock — no shared state involved.
	if node.Config == nil {
		if err := node.ParseConfig(); err != nil {
			return fmt.Errorf("parse config: %w", err)
		}
	}

	path, ok := node.Config["path"].(string)
	if !ok || path == "" {
		return fmt.Errorf("trigger.webhook: missing or invalid \"path\" field in config")
	}

	method, _ := node.Config["method"].(string)
	if method == "" {
		method = "POST"
	}

	hmacSecret, _ := node.Config["hmac_secret"].(string)
	authHeader, _ := node.Config["auth_header"].(string)
	authToken, _ := node.Config["auth_token"].(string)

	// Capture loop variables for the closure.
	wfID := workflowID
	nID := node.ID

	reg := &WebhookRegistration{
		WorkflowID: workflowID,
		NodeID:     node.ID,
		Path:       path,
		Method:     method,
		HMACSecret: hmacSecret,
		AuthHeader: authHeader,
		AuthToken:  authToken,
		TriggerFn: func(items []Item) {
			tm.triggerFn(wfID, nID, items)
		},
	}

	// Hold the mutex through the entire check-then-act to prevent TOCTOU races.
	tm.mu.Lock()
	if tm.active[workflowID] != nil {
		if _, exists := tm.active[workflowID][key]; exists {
			tm.mu.Unlock()
			tm.logger.Debug().
				Str("workflow_id", workflowID).
				Str("node_id", node.ID).
				Msg("webhook trigger already active, skipping")
			return nil
		}
	}

	if err := tm.webhookServer.Register(reg); err != nil {
		tm.mu.Unlock()
		return fmt.Errorf("register webhook path %q: %w", path, err)
	}

	if tm.active[workflowID] == nil {
		tm.active[workflowID] = make(map[string]*triggerEntry)
	}
	tm.active[workflowID][key] = &triggerEntry{
		kind:        "webhook",
		webhookPath: path,
	}
	tm.mu.Unlock()

	tm.logger.Info().
		Str("workflow_id", workflowID).
		Str("node_id", node.ID).
		Str("path", path).
		Str("method", method).
		Msg("webhook trigger activated")

	return nil
}

// DeactivateWorkflow removes all triggers for the given workflow.
// Ownership is per exact workflow ID, so a workflow named "a" can never tear
// down the triggers of an unrelated workflow whose ID merely starts with
// "a_" (e.g. "a_b").
func (tm *TriggerManager) DeactivateWorkflow(workflowID string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	entries := tm.active[workflowID]
	if entries == nil {
		return
	}
	for key, entry := range entries {
		tm.deactivateEntry(key, entry)
	}
	delete(tm.active, workflowID)
}

// DeactivateAll removes all triggers (called on shutdown).
func (tm *TriggerManager) DeactivateAll() {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	for _, entries := range tm.active {
		for key, entry := range entries {
			tm.deactivateEntry(key, entry)
		}
	}
	// Clear the registry so a later ActivateWorkflow (an engine restarted in
	// the same process) registers triggers again instead of skipping them as
	// "already active".
	tm.active = make(map[string]map[string]*triggerEntry)
}

// deactivateEntry tears down a single trigger entry. Callers remove it from
// tm.active. Must be called with tm.mu held.
func (tm *TriggerManager) deactivateEntry(key string, entry *triggerEntry) {
	switch entry.kind {
	case "schedule":
		tm.scheduler.RemoveJob(entry.cronID)
		tm.logger.Info().
			Str("key", key).
			Msg("schedule trigger deactivated")

	case "webhook":
		tm.webhookServer.Deregister(entry.webhookPath)
		tm.logger.Info().
			Str("key", key).
			Str("path", entry.webhookPath).
			Msg("webhook trigger deactivated")

	case "provider":
		if entry.deactivate != nil {
			entry.deactivate()
		}
		tm.logger.Info().
			Str("key", key).
			Msg("provider trigger deactivated")
	}
}
