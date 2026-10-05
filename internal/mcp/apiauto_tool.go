package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/monoes/mono-agent/internal/jev/jevconf"
	"github.com/monoes/mono-agent/internal/openaiapi"
)

// apiAutoTools is the tool over the `auto` model of the OpenAI-compatible API, which is a Jev surface
// of the profile (internal/jev/jevconf) and not a setting of the server: what `monoagentcli jev
// enable|disable api_auto` does, for the MCP server's profile.
func apiAutoTools() []tool {
	return []tool{{
		name: "api_auto_set",
		description: "Switch the auto model of the OpenAI-compatible API (the Jev surface api_auto) on or off for the active profile, as `monoagentcli jev enable|disable api_auto` does: " +
			"the document of that command's --json (profile_id, surface, enabled and, when switching on, threshold and egress) plus auto, what api_status says of the auto model after the change " +
			"(available, or what it is missing: the surface, a Jev key). enabled is required. " +
			"Switching on needs acknowledge_egress: true, because while it is on the first 4,000 characters of the last user message of each request for the model auto (of an image request, its prompt) " +
			"and the names, descriptions and validated cost and latency of the models the server serves go to TypeSafe; without it the call is refused, shows exactly that, and nothing changes. " +
			"acknowledge_egress is set by the caller: like --yes of the command it makes the data visible before it moves, and it is not an access control. Switching off needs no acknowledgement. " +
			"This tool never creates, stores, uses or shows the Jev key (the user stores it with `monoagentcli jev key set` in their own terminal, or TYPESAFE_API_KEY is set where the server runs): " +
			"with no key the surface is on and auto stays unavailable, which auto says. " +
			"It changes nothing else: the profile's threshold, its other Jev surfaces and other profiles are untouched. Which models auto may pick is auto_confinement (api_config_set), not this.",
		schema: objSchema(map[string]interface{}{
			"enabled":            boolParam("true switches the auto model on for the active profile, false switches it off"),
			"acknowledge_egress": boolParam("Needed with enabled true: says that the user knows what is sent to TypeSafe while it is on (the refusal lists it). Not needed to switch off"),
		}, "enabled"),
		annotations: map[string]bool{"readOnlyHint": false},
		mutating:    true,
		handler:     toolAPIAutoSet,
	}}
}

func toolAPIAutoSet(ctx context.Context, s *Server, args json.RawMessage) (interface{}, error) {
	var a struct {
		Enabled           *bool `json:"enabled"`
		AcknowledgeEgress bool  `json:"acknowledge_egress"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if a.Enabled == nil {
		return nil, errors.New("enabled is required: true switches the auto model on, false switches it off")
	}
	surface := jevconf.APIAuto
	if *a.Enabled && !a.AcknowledgeEgress {
		return nil, egressRefusal(surface)
	}
	rt, err := s.runtime()
	if err != nil {
		return nil, err
	}
	if err := jevconf.SetEnabled(rt.db.DB, rt.profileID, surface, *a.Enabled); err != nil {
		return nil, fmt.Errorf("switch the auto model: %w", err)
	}

	// The document of `jev enable|disable --json`.
	doc := map[string]interface{}{
		"profile_id": rt.profileID,
		"surface":    string(surface),
		"enabled":    *a.Enabled,
	}
	if *a.Enabled {
		doc["threshold"] = jevconf.Threshold(rt.db.DB, rt.profileID, surface, jevconf.DefaultThreshold[surface])
		doc["egress"] = jevconf.Egress[surface]
	}
	// And what `api status --json` says of the auto model now, which the command says on stderr when
	// there is no key. The key is only asked for, never read: this never decrypts anything.
	st := openaiapi.DefaultAuto(rt.db.DB).Status(ctx, rt.profileID)
	doc["auto"] = openaiapi.AutoReport{Available: st.Available, Missing: st.Missing, KeySource: st.KeySource}
	return doc, nil
}

// egressRefusal is the refusal to switch a surface on without the acknowledgement: what would leave
// the machine, in the words `jev enable` prints, and how to go on.
func egressRefusal(surface jevconf.Surface) error {
	var b strings.Builder
	b.WriteString("Switching the auto model on sends data to TypeSafe (Jev) for every request that asks for the model auto:")
	for _, e := range jevconf.Egress[surface] {
		b.WriteString("\n- " + e)
	}
	b.WriteString("\nPass acknowledge_egress: true to switch it on once the user has agreed to that. Nothing was changed. Switching it off needs no acknowledgement.")
	return errors.New(b.String())
}
