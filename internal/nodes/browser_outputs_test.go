package nodes

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/workflow"
)

// outPkg is a declarative (native "") or native package context.
type outPkg struct {
	action.PackageContext
	native string
}

func (p outPkg) ID() string                                    { return "acme" }
func (p outPkg) StartURL() string                              { return "" }
func (p outPkg) Domains() []string                             { return nil }
func (p outPkg) PermittedSteps() []string                      { return nil }
func (p outPkg) Native() string                                { return p.native }
func (p outPkg) Trust() string                                 { return "local" }
func (p outPkg) Selector(string) (*action.SelectorEntry, bool) { return nil, false }

type outSource struct{ pkg outPkg }

func (s outSource) Load(a, t string) ([]byte, error) {
	if a != "acme" || t != "title" {
		return nil, fmt.Errorf("not found %s/%s", a, t)
	}
	return []byte(`{"actionType":"title","automation":"acme","sideEffects":"read",
	  "outputs":{"success":["title","missing"]},
	  "steps":[{"id":"t","type":"set_variable","variable_name":"title","value":"Hello"}]}`), nil
}
func (s outSource) List() ([]string, error)              { return []string{"acme/title"}, nil }
func (s outSource) Package(string) action.PackageContext { return s.pkg }

// closePage is a blank tab: nothing is done with it but URL checks and Close.
type closePage struct{ browser.PageInterface }

func (closePage) Close() error            { return nil }
func (closePage) GetURL() (string, error) { return "about:blank", nil }

type pageProvider struct{}

func (pageProvider) GetPage(context.Context, string, string) (browser.PageInterface, error) {
	return closePage{}, nil
}

func runTitle(t *testing.T, native string, in []workflow.Item) []workflow.Item {
	t.Helper()
	action.SetDefSource(outSource{pkg: outPkg{native: native}})
	prev := globalSessionProvider
	SetGlobalSessionProvider(pageProvider{})
	t.Cleanup(func() { action.SetDefSource(nil); SetGlobalSessionProvider(prev) })

	out, err := NewBrowserNode("acme", "title").Execute(context.Background(), workflow.NodeInput{Items: in}, map[string]interface{}{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return out[0].Items
}

// A declarative action's outputs.success variables become the node output,
// merged over the input item.
func TestBrowserNode_DeclaredOutputs(t *testing.T) {
	items := runTitle(t, "", []workflow.Item{workflow.NewItem(map[string]interface{}{"row": 1.0})})
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	want := map[string]interface{}{"row": 1.0, "title": "Hello"}
	if !reflect.DeepEqual(items[0].JSON, want) {
		t.Errorf("output = %v, want %v", items[0].JSON, want)
	}
}

// A native (built-in style) package keeps the old output: the input passes
// through untouched.
func TestBrowserNode_NativeOutputsUnchanged(t *testing.T) {
	in := []workflow.Item{workflow.NewItem(map[string]interface{}{"row": 1.0})}
	items := runTitle(t, "acme", in)
	if len(items) != 1 || !reflect.DeepEqual(items[0].JSON, map[string]interface{}{"row": 1.0}) {
		t.Errorf("native output changed: %v", items)
	}
}
