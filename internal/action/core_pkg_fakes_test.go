package action

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/monoes/mono-agent/internal/browser"
	"github.com/rs/zerolog"
)

// fakePkg is an in-memory PackageContext.
type fakePkg struct {
	id        string
	startURL  string
	domains   []string
	permitted []string
	fragments map[string]*FragmentDef
	selectors map[string]*SelectorEntry
	scripts   map[string]string
	native    string
}

func (p *fakePkg) ID() string               { return p.id }
func (p *fakePkg) StartURL() string         { return p.startURL }
func (p *fakePkg) Domains() []string        { return p.domains }
func (p *fakePkg) PermittedSteps() []string { return p.permitted }
func (p *fakePkg) Native() string           { return p.native }
func (p *fakePkg) Selector(k string) (*SelectorEntry, bool) {
	e, ok := p.selectors[k]
	return e, ok
}
func (p *fakePkg) Fragment(n string) (*FragmentDef, error) {
	if f, ok := p.fragments[n]; ok {
		return f, nil
	}
	return nil, errors.New("no such fragment")
}
func (p *fakePkg) Script(n string) (string, error) {
	if s, ok := p.scripts[n]; ok {
		return s, nil
	}
	return "", errors.New("no such script")
}
func (p *fakePkg) ResolveAction(ref string) (*ActionDef, PackageContext, error) {
	return nil, nil, errors.New("not installed")
}

// obsRecord captures SelectorObserver calls.
type obsRecord struct {
	mu    sync.Mutex
	calls []string
}

func (o *obsRecord) ObserveSelector(id, key string, idx int, ok, healed bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, fmt.Sprintf("%s/%s idx=%d ok=%v healed=%v", id, key, idx, ok, healed))
}

// markPage is a page whose DOM is a set of CSS selectors that are present,
// a current URL, and an EvalCDP that understands the aria/text matcher:
// ariaHits maps a {"role","name"} / {"text"} JSON to whether it matches.
type markPage struct {
	browser.PageInterface
	present  map[string]bool
	url      string
	navs     []string
	ariaHits map[string]bool
	marked   map[string]bool // marker selectors currently present
	cleaned  int
}

var (
	wantRe  = regexp.MustCompile(`const want = (\{[^\n]*\});`)
	tokenRe = regexp.MustCompile(`setAttribute\("data-monoagent-sel", "([0-9a-f]+)"\)`)
)

func (p *markPage) EvalCDP(js string) (interface{}, error) {
	if strings.Contains(js, "removeAttribute") {
		p.cleaned++
		p.marked = nil
		return true, nil
	}
	w := wantRe.FindStringSubmatch(js)
	t := tokenRe.FindStringSubmatch(js)
	if w == nil || t == nil {
		return nil, fmt.Errorf("unexpected script")
	}
	if !p.ariaHits[w[1]] {
		return false, nil
	}
	if p.marked == nil {
		p.marked = map[string]bool{}
	}
	p.marked[fmt.Sprintf(`[data-monoagent-sel="%s"]`, t[1])] = true
	return true, nil
}

func (p *markPage) Element(sel string, _ time.Duration) (browser.ElementHandle, error) {
	if p.present[sel] || p.marked[sel] {
		return &domElement{n: &domNode{text: sel}}, nil
	}
	return nil, fmt.Errorf("element not found: %s", sel)
}
func (p *markPage) ElementX(xp string, t time.Duration) (browser.ElementHandle, error) {
	return p.Element(xp, t)
}
func (p *markPage) GetURL() (string, error) { return p.url, nil }
func (p *markPage) Navigate(u string) error {
	p.navs = append(p.navs, u)
	p.url = u
	return nil
}
func (p *markPage) Timeout(time.Duration) browser.PageInterface { return p }
func (p *markPage) WaitLoad() error                             { return nil }

func newPkgExecutor(page browser.PageInterface, pkg PackageContext) *ActionExecutor {
	ae := NewActionExecutor(context.Background(), page, nil, nil, nil, nil, zerolog.Nop())
	ae.SetPackage(pkg)
	return ae
}

func (p *markPage) Race(sels []string, _ time.Duration) (int, browser.ElementHandle, error) {
	for i, s := range sels {
		if p.present[s] || p.marked[s] {
			return i, &domElement{n: &domNode{text: s}}, nil
		}
	}
	return -1, nil, errors.New("none of the selectors matched")
}
