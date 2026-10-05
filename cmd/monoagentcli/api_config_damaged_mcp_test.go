package main

// A saved row that cannot be read, through the command and through the tools, on two copies of one
// database: the removal of it (`api config unset --all`, api_config_set with unset all) stands behind
// the same gate, --yes for the command and --allow-api-exposure for the server, and is the same
// document; everything else that reads the row stops at it in the same words, which are the ones a
// model passes on to the user. A row that a newer version saved is the same error in both, and is
// never offered a reset.

import (
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/apiconfig"
	"github.com/monoes/mono-agent/internal/testdb"
)

// damagedPair is two databases that hold the same saved row: the command's and the tool's.
func damagedPair(t *testing.T, row string) (dbCLI, dbTool string) {
	t.Helper()
	dbCLI = configTest(t)
	dbTool = testdb.Path(t)
	useInstaller(t, &fakeAutostart{})
	fakeAPIMonomind(t)
	putRowAt(t, dbCLI, row)
	putRowAt(t, dbTool, row)
	return dbCLI, dbTool
}

// bothKeep says that neither database was written: the row is what it was.
func bothKeep(t *testing.T, row string, dbs ...string) {
	t.Helper()
	for _, db := range dbs {
		if v, ok := rowAt(t, db); !ok || v != row {
			t.Errorf("a refused call wrote: %q, %v", v, ok)
		}
	}
}

// Without --yes the command refuses to remove a row that cannot be read (exit 3), and without
// --allow-api-exposure the tool refuses it: with the one reason the command's dry run reports.
func TestAPIConfigSetToolRefusesTheRepairAsTheCommandRefusesItWithoutYes(t *testing.T) {
	for name, row := range unreadableRows {
		t.Run(name, func(t *testing.T) {
			dbCLI, dbTool := damagedPair(t, row)

			if _, _, err := runAPI(t, dbCLI, "default", true, "config", "unset", "--all"); err == nil || exitCodeFor(err) != 3 || !strings.Contains(err.Error(), "--yes") {
				t.Fatalf("the command without --yes: exit %d, %v", exitCodeFor(err), err)
			}
			dry, _, err := runAPI(t, dbCLI, "default", true, "config", "unset", "--all", "--dry-run")
			if err != nil {
				t.Fatal(err)
			}
			report := decodeChange(t, dry)
			if !report.RemovedUnreadableRow || len(report.Widening) != 1 || report.Widening[0].Key != apiconfig.WideningKeySavedSettings {
				t.Fatalf("the case does not exercise what it names: %+v", report)
			}

			text, isErr := mcpOnce(t, mcpOptions(t, dbTool, "default", false), "api_config_set", map[string]any{"unset": "all"})
			if !isErr || !strings.Contains(text, "saved_settings: "+report.Widening[0].Reason) {
				t.Errorf("the tool without --allow-api-exposure (error %v) lacks the command's reason %q:\n%s", isErr, report.Widening[0].Reason, text)
			}
			if strings.Contains(text, "supersecret") {
				t.Errorf("the refusal repeats what the row held:\n%s", text)
			}
			bothKeep(t, row, dbCLI, dbTool)
		})
	}
}

// With --yes the command removes the row, and with --allow-api-exposure the tool does: the same
// document, the field that says so, and the same gap where the row was.
func TestAPIConfigSetToolRepairsWhatTheCommandRepairsWithYes(t *testing.T) {
	for name, row := range unreadableRows {
		t.Run(name, func(t *testing.T) {
			dbCLI, dbTool := damagedPair(t, row)

			cli, _, err := runAPI(t, dbCLI, "default", true, "config", "unset", "--all", "--yes")
			if err != nil {
				t.Fatal(err)
			}
			tool, isErr := mcpOnce(t, mcpOptions(t, dbTool, "default", true), "api_config_set", map[string]any{"unset": "all"})
			if isErr {
				t.Fatalf("api_config_set failed: %s", tool)
			}
			doc := decodeChange(t, cli)
			if !doc.Applied || !doc.RemovedUnreadableRow || len(doc.Widening) != 1 || doc.Widening[0].Key != apiconfig.WideningKeySavedSettings || !strings.Contains(cli, `"removed_unreadable_row": true`) {
				t.Errorf("the case does not exercise what it names: %+v", doc)
			}
			if want := asMCPDoc(cli); tool != want {
				t.Errorf("api_config_set is not the document of the command: %s", firstDifference(want, tool))
			}
			for _, db := range []string{dbCLI, dbTool} {
				if v, ok := rowAt(t, db); ok {
					t.Errorf("the row is still there: %q", v)
				}
			}
		})
	}
}

// What reads the row, or changes anything but all of it, stops at the damage with the command's exit
// 3 and its message, and the tool says the same words as an error.
type damagedReader struct {
	name string
	cli  []string
	tool string
	args map[string]any
}

var damagedReaders = []damagedReader{
	{"show", []string{"config", "show"}, "api_config_get", map[string]any{}},
	{"status", []string{"status"}, "api_status", map[string]any{}},
	{"models", []string{"models"}, "api_models_list", map[string]any{}},
	{"set", []string{"config", "set", "--max-concurrent", "8"}, "api_config_set", map[string]any{"set": map[string]any{"max_concurrent": "8"}}},
	{"unset", []string{"config", "unset", "max_concurrent"}, "api_config_set", map[string]any{"unset": []string{"max_concurrent"}}},
}

func TestTheToolsStopAtADamagedRowInTheWordsOfTheCommand(t *testing.T) {
	for name, row := range unreadableRows {
		t.Run(name, func(t *testing.T) {
			dbCLI, dbTool := damagedPair(t, row)
			for _, r := range damagedReaders {
				_, _, err := runAPI(t, dbCLI, "default", true, r.cli...)
				if err == nil || exitCodeFor(err) != 3 {
					t.Fatalf("%s: the command: exit %d, %v", r.name, exitCodeFor(err), err)
				}
				text, isErr := mcpOnce(t, mcpOptions(t, dbTool, "default", true), r.tool, r.args)
				if !isErr || text != err.Error() {
					t.Errorf("%s: the tool says %q (error %v), the command %q", r.name, text, isErr, err.Error())
				}
				if !strings.HasPrefix(text, damagedStart) || !strings.Contains(text, repairWithItsGate) || strings.Contains(text, "supersecret") || strings.Contains(text, "\n") {
					t.Errorf("%s: the message that a model passes on to the user: %q", r.name, text)
				}
			}
			bothKeep(t, row, dbCLI, dbTool)
		})
	}
}

// A row that a newer version saved is exit 1 for the command and an error for the tool, in the same
// words, and neither removes it (--yes and --allow-api-exposure included) or offers a reset.
func TestARowOfANewerFormatIsTheSameErrorInTheCommandAndInTheTools(t *testing.T) {
	const newer = `{"v":2,"v1_addr":":9443","later":{"x":1}}`
	dbCLI, dbTool := damagedPair(t, newer)
	for _, r := range append(damagedReaders, damagedReader{"unset all", []string{"config", "unset", "--all", "--yes"}, "api_config_set", map[string]any{"unset": "all"}}) {
		_, _, err := runAPI(t, dbCLI, "default", true, r.cli...)
		if err == nil || exitCodeFor(err) != 1 {
			t.Fatalf("%s: the command: exit %d, %v", r.name, exitCodeFor(err), err)
		}
		text, isErr := mcpOnce(t, mcpOptions(t, dbTool, "default", true), r.tool, r.args)
		if !isErr || text != err.Error() {
			t.Errorf("%s: the tool says %q (error %v), the command %q", r.name, text, isErr, err.Error())
		}
		if strings.HasPrefix(text, damagedStart) || strings.Contains(text, "unset --all") || !strings.Contains(text, apiconfig.RemoveRowSQL) {
			t.Errorf("%s: a newer row is not damaged and is not offered a reset: %q", r.name, text)
		}
	}
	bothKeep(t, newer, dbCLI, dbTool)
}
