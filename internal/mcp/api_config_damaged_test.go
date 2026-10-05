package mcp

// A saved row that cannot be read is invalid saved data the user can fix: every tool that reads it
// stops with the command's message, which starts `the saved settings are damaged` and names the repair
// (`monoagentcli api config unset --all --yes`). The repair through api_config_set (unset all) is a
// widening of unknown size, since what the row limited cannot be told, so it stands behind the
// operator's --allow-api-exposure like any other: refused without it, allowed with it, and the result
// says removed_unreadable_row. A row in a newer format is an error that nothing here removes and that
// is never offered a reset.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/apiconfig"
)

// unreadableRows are the kinds of damage: a row that is not JSON at all, and one that is JSON this
// version refuses. The last holds a value that must never come back in a message.
var unreadableRows = map[string]string{
	"not json":               `{"v":1,`,
	"an empty value":         ``,
	"a version of zero":      `{"v":0}`,
	"a field of a bad type":  `{"v":1,"max_concurrent":true}`,
	"a value in broken text": `{"v":1,"v1_addr":"supersecret.example:9443",`,
}

const (
	damagedStart      = "the saved settings are damaged"
	repairWithItsGate = "`monoagentcli api config unset --all --yes` removes them"
	newerRow          = `{"v":2,"v1_addr":":9443","later":{"x":1}}`
)

// putRow writes the saved row as it is, damaged or not.
func (f *configFixture) putRow(value string) {
	f.t.Helper()
	if _, err := f.Side.DB.Exec(`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, apiconfig.Row, value); err != nil {
		f.t.Fatal(err)
	}
}

// row is the stored text of the saved row, and whether there is one.
func (f *configFixture) row() (string, bool) {
	f.t.Helper()
	var v string
	err := f.Side.DB.QueryRow(`SELECT value FROM settings WHERE key = ?`, apiconfig.Row).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false
	}
	if err != nil {
		f.t.Fatal(err)
	}
	return v, true
}

// loadError is what apiconfig says of the row that is stored: the message every surface passes on.
func (f *configFixture) loadError() error {
	f.t.Helper()
	_, err := apiconfig.Load(context.Background(), f.Side.DB)
	if err == nil {
		f.t.Fatal("the row is readable")
	}
	return err
}

// Without the operator's flag the removal of a row that cannot be read is refused, as a change that
// reaches further is, with the one reason of the gate and the command that does it for the user.
func TestAPIConfigSetRefusesToRemoveAnUnreadableRowWithoutTheOperatorsFlag(t *testing.T) {
	for name, doc := range unreadableRows {
		t.Run(name, func(t *testing.T) {
			f := newConfigFixture(t, configSetup{})
			f.putRow(doc)
			_, err := f.call("api_config_set", unset("all"))
			if err == nil {
				t.Fatal("the row was removed without the operator's flag")
			}
			msg := err.Error()
			for _, want := range []string{
				"saved_settings: The saved settings cannot be read, so what they limited cannot be told",
				"--allow-api-exposure", "MONOAGENT_MCP_ALLOW_API_EXPOSURE=1", "`monoagentcli api config unset --all --yes`", "nothing was saved",
			} {
				if !strings.Contains(msg, want) {
					t.Errorf("the refusal must say %q:\n%s", want, msg)
				}
			}
			if strings.HasPrefix(msg, damagedStart) {
				t.Errorf("the refusal of the gate is not the message of the damage: %q", msg)
			}
			if strings.Contains(msg, "supersecret") {
				t.Errorf("the refusal repeats what the row held: %q", msg)
			}
			// The repair is the command's (and the operator's flag); the refusal names no other way.
			if strings.Contains(msg, "desktop app") {
				t.Errorf("the refusal sends the user to the desktop app for a repair that is the command's: %q", msg)
			}
			if v, ok := f.row(); !ok || v != doc {
				t.Errorf("a refused call wrote: %q, %v", v, ok)
			}
		})
	}
}

// With it the row is removed in the same call, and the result says so.
func TestAPIConfigSetRemovesAnUnreadableRowWhenTheOperatorAllowsExposure(t *testing.T) {
	for name, doc := range unreadableRows {
		t.Run(name, func(t *testing.T) {
			f := newConfigFixture(t, configSetup{allowExposure: true})
			f.putRow(doc)
			text := f.mustCall("api_config_set", unset("all"))
			r := decodeChangeResult(t, text)
			if !r.Applied || !r.RemovedUnreadableRow || len(r.Changed) != 0 || len(r.Widening) != 1 || r.Widening[0].Key != apiconfig.WideningKeySavedSettings {
				t.Errorf("applied %v, removed %v, changed %v, widening %v", r.Applied, r.RemovedUnreadableRow, r.Changed, r.Widening)
			}
			if !strings.Contains(text, `"removed_unreadable_row": true`) || strings.Contains(text, "supersecret") {
				t.Errorf("the document:\n%s", text)
			}
			if v, ok := f.row(); ok {
				t.Errorf("the row is still there: %q", v)
			}
			f.mustCall("api_config_get", nil) // the settings read again
		})
	}
}

// Nothing is said of a repair when the row was readable.
func TestAPIConfigSetUnsetAllOfAReadableRowSaysNothingOfARepair(t *testing.T) {
	f := newConfigFixture(t, configSetup{})
	f.save("max_concurrent=8")
	text := f.mustCall("api_config_set", unset("all"))
	if strings.Contains(text, "removed_unreadable_row") || strings.Contains(text, apiconfig.WideningKeySavedSettings) {
		t.Errorf("a readable row was removed as if it were damaged:\n%s", text)
	}
}

// Every other change stops at the damage, with the gate closed or open, in the command's words.
func TestAPIConfigSetOfAnythingElseStopsAtADamagedRowWithTheMessageOfTheCommand(t *testing.T) {
	for name, doc := range unreadableRows {
		for _, gate := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, gate %v", name, gate), func(t *testing.T) {
				f := newConfigFixture(t, configSetup{allowExposure: gate})
				f.putRow(doc)
				want := f.loadError().Error()
				for _, args := range []map[string]any{
					set(map[string]any{"max_concurrent": "8"}),
					unset([]string{"max_concurrent"}),
					unset([]string{"v1_addr", "confinement"}),
				} {
					_, err := f.call("api_config_set", args)
					if err == nil || err.Error() != want {
						t.Errorf("%v (gate %v): %v, want %q", args, gate, err, want)
						continue
					}
					if !strings.HasPrefix(want, damagedStart) || !strings.Contains(want, repairWithItsGate) || strings.Contains(want, "\n") || strings.Contains(want, "supersecret") {
						t.Errorf("the message of the command: %q", want)
					}
				}
				if v, ok := f.row(); !ok || v != doc {
					t.Errorf("the row was touched: %q, %v", v, ok)
				}
			})
		}
	}
}

// The tools that read the settings stop at the damage with the same message, not a rewording of it:
// a model that is told the settings are damaged tells the user, and the user finds the command in it.
func TestTheToolsThatReadTheSavedSettingsPassTheDamageOn(t *testing.T) {
	for name, doc := range unreadableRows {
		t.Run(name, func(t *testing.T) {
			f := newConfigFixture(t, configSetup{allowExposure: true})
			f.putRow(doc)
			want := f.loadError().Error()
			for _, tool := range []string{"api_config_get", "api_status", "api_models_list"} {
				text, err := f.call(tool, nil)
				if err == nil || err.Error() != want {
					t.Errorf("%s: %v (text %q), want the error %q", tool, err, text, want)
				}
			}
			if v, ok := f.row(); !ok || v != doc {
				t.Errorf("a read touched the row: %q, %v", v, ok)
			}
		})
	}
}

// A row that a newer version saved is not damaged: an error for every tool, the gate open or not, that
// does not offer a reset and that nothing here removes.
func TestARowOfANewerFormatIsAnErrorThatNothingRemovesAndNoResetIsOffered(t *testing.T) {
	for _, gate := range []bool{false, true} {
		f := newConfigFixture(t, configSetup{allowExposure: gate})
		f.putRow(newerRow)
		for _, c := range []struct {
			tool string
			args map[string]any
		}{
			{"api_config_get", nil}, {"api_status", nil}, {"api_models_list", nil},
			{"api_config_set", unset("all")},
			{"api_config_set", set(map[string]any{"max_concurrent": "8"})},
			{"api_config_set", unset([]string{"max_concurrent"})},
		} {
			text, err := f.call(c.tool, c.args)
			if err == nil {
				t.Errorf("%s %v (gate %v) answered: %q", c.tool, c.args, gate, text)
				continue
			}
			msg := err.Error()
			if !errors.Is(err, apiconfig.ErrTooNew) || !strings.Contains(msg, "monoagentcli that wrote it") || !strings.Contains(msg, apiconfig.RemoveRowSQL) {
				t.Errorf("%s %v (gate %v): %q", c.tool, c.args, gate, msg)
			}
			if strings.HasPrefix(msg, damagedStart) || strings.Contains(msg, "unset --all") || strings.Contains(msg, "removed_unreadable_row") {
				t.Errorf("%s %v (gate %v): a newer row is not damaged and is not offered a reset: %q", c.tool, c.args, gate, msg)
			}
		}
		if v, ok := f.row(); !ok || v != newerRow {
			t.Errorf("gate %v: the row was touched: %q, %v", gate, v, ok)
		}
	}
}
