package vault

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/monoes/mono-agent/internal/secrets"
	vaultstore "github.com/monoes/mono-agent/internal/vault"
	"github.com/monoes/mono-agent/internal/workflow"
)

// copyMap shallow-copies an item's JSON map before mutating it, matching the
// convention every other node package uses (see internal/nodes/image/image.go).
func copyMap(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// SecretSaveNode writes a credential into the profile's encrypted vault
// (~/.monoagent — see internal/secrets), pulling the field values to encrypt
// out of the current item's own JSON data (e.g. a token a prior node in the
// same workflow just obtained). It never receives or logs plaintext itself
// beyond what secrets.Add already handles.
type SecretSaveNode struct{}

func (n *SecretSaveNode) Type() string { return "vault.secret_save" }

func (n *SecretSaveNode) Execute(ctx context.Context, input workflow.NodeInput, config map[string]interface{}) ([]workflow.NodeOutput, error) {
	kind, _ := config["kind"].(string)
	if kind == "" {
		kind = "secret"
	}
	if kind != "secret" && kind != "login" {
		return nil, fmt.Errorf("vault.secret_save: kind must be \"secret\" or \"login\", got %q", kind)
	}
	name, _ := config["name"].(string)
	if name == "" {
		return nil, fmt.Errorf("vault.secret_save: name is required")
	}
	// The engine auto-parses any config string starting with "[" or "{"
	// into a native Go value before Execute ever sees it (see
	// ExpressionEngine.resolveValue in internal/workflow/expression.go) —
	// so field_keys arrives as []interface{}, not the raw JSON string a
	// user typed into the textarea. core.set's "assignments" field (the
	// same type=textarea-holds-a-JSON-array shape) expects the identical
	// pre-parsed type for the same reason.
	rawFieldKeys, ok := config["field_keys"].([]interface{})
	if !ok || len(rawFieldKeys) == 0 {
		return nil, fmt.Errorf("vault.secret_save: field_keys must be a non-empty JSON array of item field names")
	}
	fieldKeys := make([]string, len(rawFieldKeys))
	for i, k := range rawFieldKeys {
		s, ok := k.(string)
		if !ok {
			return nil, fmt.Errorf("vault.secret_save: field_keys[%d] must be a string", i)
		}
		fieldKeys[i] = s
	}
	username, _ := config["username"].(string)
	url, _ := config["url"].(string)
	notes, _ := config["notes"].(string)
	outputField, _ := config["output_field"].(string)
	if outputField == "" {
		outputField = "vault_id"
	}

	db := vaultstore.DBFromContext(ctx)
	if db == nil {
		return nil, fmt.Errorf("vault.secret_save: no database in context")
	}
	profileID := vaultstore.ProfileIDFromContext(ctx)

	outItems := make([]workflow.Item, 0, len(input.Items))
	for itemIdx, item := range input.Items {
		newJSON := copyMap(item.JSON)

		fields := make(map[string]string, len(fieldKeys))
		for _, key := range fieldKeys {
			v, ok := item.JSON[key]
			// A present-but-null field (ok=true, v=nil) is just as unusable
			// as a missing one: without this check, v.(string) below fails
			// silently and fmt.Sprint(nil) would have stored the literal
			// string "<nil>" as the secret's field value.
			if !ok || v == nil {
				return nil, fmt.Errorf("vault.secret_save: item %d has no field %q", itemIdx, key)
			}
			if s, ok := v.(string); ok {
				fields[key] = s
				continue
			}
			// Numbers/bools round-trip fine as their JSON text (e.g. "42",
			// "true"). Anything structured (map/slice — e.g. re-saving a
			// vault.secret_get "credential" object under one field) must go
			// through json.Marshal too: fmt.Sprint would produce Go's
			// %v-style map[k:v] syntax, not valid JSON, silently corrupting
			// the value on any round trip.
			encoded, err := json.Marshal(v)
			if err != nil {
				return nil, fmt.Errorf("vault.secret_save: item %d field %q: %w", itemIdx, key, err)
			}
			fields[key] = string(encoded)
		}

		id, err := secrets.Add(ctx, db, profileID, kind, name, fields, username, url, notes)
		if err != nil {
			return nil, fmt.Errorf("vault.secret_save: item %d: %w", itemIdx, err)
		}
		newJSON[outputField] = id
		outItems = append(outItems, workflow.Item{JSON: newJSON})
	}
	return []workflow.NodeOutput{{Handle: "main", Items: outItems}}, nil
}
