package crawl

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/workflow"
)

const productHTML = `<!doctype html>
<html><head><title>Shop</title></head><body>
<h1 class="title">Blue Kettle</h1>
<span class="price">$29</span>
<img class="hero" src="/kettle.png">
<ul>
  <li class="card"><b class="name">Alpha</b><i class="cost">$1</i></li>
  <li class="card"><b class="name">Beta</b><i class="cost">$2</i></li>
</ul>
</body></html>`

// fakeGenerator returns a canned config in AgentGenerator.GenerateConfig's
// shape and records what it was asked.
type fakeGenerator struct {
	out     map[string]interface{}
	err     error
	gotHTML string
	gotPurp string
}

func (f *fakeGenerator) GenerateConfig(_ context.Context, _, htmlContent, purpose string, _ map[string]interface{}) (map[string]interface{}, error) {
	f.gotHTML = htmlContent
	f.gotPurp = purpose
	return f.out, f.err
}

// servePage serves html from an httptest server reachable through the
// node's SSRF-safe client: the page's hostname resolves to a public IP and
// the raw dialer is redirected to the loopback listener.
func servePage(t *testing.T, html string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}))
	t.Cleanup(srv.Close)

	origResolver, origDial := dnsResolver, rawDial
	t.Cleanup(func() { dnsResolver, rawDial = origResolver, origDial })
	dnsResolver = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	rawDial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	return "http://shop.example/product"
}

func runExtract(t *testing.T, node *ExtractPageNode, config map[string]interface{}) map[string]interface{} {
	t.Helper()
	outs, err := node.Execute(context.Background(), workflow.NodeInput{}, config)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(outs) != 1 || len(outs[0].Items) != 1 {
		t.Fatalf("Execute() = %+v, want one output with one item", outs)
	}
	return outs[0].Items[0].JSON
}

func TestExtractPage_NaturalUsesGeneratedFields(t *testing.T) {
	pageURL := servePage(t, productHTML)
	gen := &fakeGenerator{out: map[string]interface{}{
		"config_name": "extract_shop_example_1234",
		"fields": map[string]interface{}{
			"title": map[string]interface{}{"xpath": "h1.title", "type": "text", "data": nil},
			"price": map[string]interface{}{"xpath": ".price", "type": "text"},
			"image": map[string]interface{}{"xpath": "img.hero@src", "type": "text"},
		},
	}}
	got := runExtract(t, &ExtractPageNode{Generator: gen}, map[string]interface{}{
		"url": pageURL, "extract_mode": "natural", "prompt": "product title, price and image",
	})

	if e, ok := got["error"]; ok {
		t.Fatalf("unexpected error field: %v", e)
	}
	want := map[string]interface{}{"title": "Blue Kettle", "price": "$29", "image": "/kettle.png"}
	if !reflect.DeepEqual(got["extracted"], want) {
		t.Errorf("extracted = %#v, want %#v", got["extracted"], want)
	}
	wantSel := map[string]string{"title": "h1.title", "price": ".price", "image": "img.hero@src"}
	if !reflect.DeepEqual(got["selectors_used"], wantSel) {
		t.Errorf("selectors_used = %#v, want %#v (config_name must never be a selector)", got["selectors_used"], wantSel)
	}
}

func TestExtractPage_NaturalSendsPageHTMLToGenerator(t *testing.T) {
	pageURL := servePage(t, productHTML)
	gen := &fakeGenerator{out: map[string]interface{}{
		"fields": map[string]interface{}{"title": map[string]interface{}{"xpath": "h1.title"}},
	}}
	runExtract(t, &ExtractPageNode{Generator: gen}, map[string]interface{}{
		"url": pageURL, "prompt": "the title",
	})
	// Selectors are generated against the DOM, so the agent needs markup,
	// not the markdown rendering.
	if !strings.Contains(gen.gotHTML, `class="price"`) {
		t.Errorf("generator got %q, want the page HTML", gen.gotHTML)
	}
	if !strings.Contains(gen.gotPurp, "the title") || !strings.Contains(gen.gotPurp, "CSS") {
		t.Errorf("purpose = %q, want the user prompt plus the CSS-selector instructions", gen.gotPurp)
	}
}

func TestExtractPage_NaturalUsesGeneratedListSelector(t *testing.T) {
	pageURL := servePage(t, productHTML)
	gen := &fakeGenerator{out: map[string]interface{}{
		"config_name":   "extract_cards",
		"list_selector": "li.card",
		"fields": map[string]interface{}{
			"name": map[string]interface{}{"xpath": ".name", "type": "text"},
			"cost": map[string]interface{}{"xpath": ".cost", "type": "text"},
		},
	}}
	got := runExtract(t, &ExtractPageNode{Generator: gen}, map[string]interface{}{
		"url": pageURL, "prompt": "every card's name and cost",
	})

	want := []interface{}{
		map[string]interface{}{"name": "Alpha", "cost": "$1"},
		map[string]interface{}{"name": "Beta", "cost": "$2"},
	}
	if !reflect.DeepEqual(got["extracted"], want) {
		t.Errorf("extracted = %#v, want %#v", got["extracted"], want)
	}
	if got["list_selector"] != "li.card" || got["count"] != 2 {
		t.Errorf("list_selector = %v, count = %v, want li.card, 2", got["list_selector"], got["count"])
	}
}

func TestExtractPage_NaturalConfigListSelectorWins(t *testing.T) {
	pageURL := servePage(t, productHTML)
	gen := &fakeGenerator{out: map[string]interface{}{
		"list_selector": "ul",
		"fields":        map[string]interface{}{"name": map[string]interface{}{"xpath": ".name"}},
	}}
	got := runExtract(t, &ExtractPageNode{Generator: gen}, map[string]interface{}{
		"url": pageURL, "prompt": "names", "list_selector": "li.card",
	})
	if got["count"] != 2 {
		t.Errorf("count = %v, want 2 (the configured list_selector overrides the generated one)", got["count"])
	}
}

func TestExtractPage_NaturalNoUsableFieldsFallsBackToMarkdown(t *testing.T) {
	cases := map[string]map[string]interface{}{
		"no fields":    {"config_name": "extract_shop"},
		"empty fields": {"config_name": "extract_shop", "fields": map[string]interface{}{}},
		"xpath only": {"config_name": "extract_shop", "fields": map[string]interface{}{
			"title": map[string]interface{}{"xpath": "//h1[@class='title']", "type": "text"},
		}},
	}
	for name, generated := range cases {
		t.Run(name, func(t *testing.T) {
			pageURL := servePage(t, productHTML)
			got := runExtract(t, &ExtractPageNode{Generator: &fakeGenerator{out: generated}}, map[string]interface{}{
				"url": pageURL, "prompt": "the title",
			})
			errMsg, _ := got["error"].(string)
			if !strings.Contains(errMsg, "no usable CSS selectors") {
				t.Errorf("error = %q, want a no-usable-selectors message", errMsg)
			}
			md, _ := got["extracted"].(string)
			if !strings.Contains(md, "Blue Kettle") {
				t.Errorf("extracted = %#v, want the markdown fallback", got["extracted"])
			}
		})
	}
}

func TestExtractPage_NaturalGeneratorErrorFallsBackToMarkdown(t *testing.T) {
	pageURL := servePage(t, productHTML)
	got := runExtract(t, &ExtractPageNode{Generator: &fakeGenerator{err: errors.New("cache-only mode")}}, map[string]interface{}{
		"url": pageURL, "prompt": "the title",
	})
	if errMsg, _ := got["error"].(string); !strings.Contains(errMsg, "AI generation failed") {
		t.Errorf("error = %q, want the generation-failed message", errMsg)
	}
	if md, _ := got["extracted"].(string); !strings.Contains(md, "Blue Kettle") {
		t.Errorf("extracted = %#v, want the markdown fallback", got["extracted"])
	}
}

func TestExtractPage_CSSMode(t *testing.T) {
	pageURL := servePage(t, productHTML)
	got := runExtract(t, &ExtractPageNode{}, map[string]interface{}{
		"url": pageURL, "extract_mode": "css",
		"fields": `{"title": "h1.title", "image": "img.hero@src"}`,
	})
	want := map[string]interface{}{"title": "Blue Kettle", "image": "/kettle.png"}
	if !reflect.DeepEqual(got["extracted"], want) {
		t.Errorf("extracted = %#v, want %#v", got["extracted"], want)
	}

	got = runExtract(t, &ExtractPageNode{}, map[string]interface{}{
		"url": pageURL, "extract_mode": "css", "list_selector": "li.card",
		"fields": map[string]interface{}{"name": ".name"},
	})
	wantList := []interface{}{map[string]interface{}{"name": "Alpha"}, map[string]interface{}{"name": "Beta"}}
	if !reflect.DeepEqual(got["extracted"], wantList) {
		t.Errorf("extracted = %#v, want %#v", got["extracted"], wantList)
	}
}

func TestExtractPage_NaturalWithoutGenerator(t *testing.T) {
	pageURL := servePage(t, productHTML)
	_, err := (&ExtractPageNode{}).Execute(context.Background(), workflow.NodeInput{}, map[string]interface{}{
		"url": pageURL, "prompt": "the title",
	})
	if err == nil || !strings.Contains(err.Error(), "local agent generator") {
		t.Errorf("Execute() error = %v, want the missing-generator error", err)
	}
}

func TestRegisterAll_NilGeneratorStaysNil(t *testing.T) {
	r := workflow.NewNodeTypeRegistry()
	RegisterAll(r, nil)
	factory, ok := r.Get("ai.extract_page")
	if !ok {
		t.Fatal("ai.extract_page not registered")
	}
	if node := factory().(*ExtractPageNode); node.Generator != nil {
		t.Errorf("Generator = %#v, want nil so natural mode reports the missing generator", node.Generator)
	}
}

func TestConfigToSelectors(t *testing.T) {
	got, err := configToSelectors(map[string]interface{}{
		"config_name":   "extract_x",
		"list_selector": "li",
		"fields": map[string]interface{}{
			"a": map[string]interface{}{"xpath": "h1", "type": "text", "data": nil},
			"b": "span.b",                                          // bare selector string
			"c": map[string]interface{}{"xpath": "//div/span"},     // XPath: unusable by goquery
			"d": map[string]interface{}{"xpath": "  "},             // blank
			"e": map[string]interface{}{"type": "text"},            // no selector
			"f": map[string]interface{}{"xpath": "(//a)[1]/@href"}, // XPath
		},
	})
	if err != nil {
		t.Fatalf("configToSelectors() error = %v", err)
	}
	want := map[string]string{"a": "h1", "b": "span.b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("configToSelectors() = %#v, want %#v", got, want)
	}
}
