package action

// Driver-neutral element lookups. The runtime session provider hands the
// executor an *extension.ExtensionPage, so every lookup a step needs must
// work through browser.PageInterface (plus the optional EvalCDP capability),
// not only through an unwrapped *rod.Page.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/go-rod/rod"

	"github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/browser"
)

// cdpEvaluator is implemented by pages that can run JavaScript in the page's
// main world outside the page CSP (extension.ExtensionPage via
// chrome.debugger Runtime.evaluate).
type cdpEvaluator interface {
	EvalCDP(js string) (interface{}, error)
}

// errXPathMultiUnsupported is returned when a page can neither be unwrapped
// to Rod nor evaluate JavaScript through CDP, so an XPath cannot be turned
// into a list of element handles. It is an error, never an empty success.
var errXPathMultiUnsupported = errors.New("XPath multi-element lookup is not supported by this page driver")

// xpathMarkAttr is the attribute elementsByXPath tags matched nodes with so
// the page driver can select them by CSS.
const xpathMarkAttr = "data-monoagent-xpath"

// elementsByXPath returns every element matched by xpath, in document order.
//
// Rod pages use Rod's native ElementsX. Other pages must expose EvalCDP: the
// XPath is evaluated in the page (ORDERED_NODE_SNAPSHOT_TYPE), each matched
// element is tagged with a one-off token, the tagged elements are resolved
// with page.Elements (querySelectorAll order is document order, the same as
// the ordered snapshot), and the tags are removed again. Non-element nodes
// (attribute, text) in the snapshot are skipped.
func elementsByXPath(page browser.PageInterface, xpath string, timeout time.Duration) ([]browser.ElementHandle, error) {
	if rodPage := unwrapRodPage(page); rodPage != nil {
		var elems []browser.ElementHandle
		err := rod.Try(func() {
			for _, re := range rodPage.Timeout(timeout).MustElementsX(xpath) {
				elems = append(elems, browser.NewRodElement(re))
			}
		})
		return elems, err
	}

	ev, ok := page.(cdpEvaluator)
	if !ok {
		return nil, fmt.Errorf("%w (page %T)", errXPathMultiUnsupported, page)
	}

	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	xpJSON, _ := json.Marshal(xpath)
	markJS := fmt.Sprintf(`(() => {
  const snap = document.evaluate(%s, document, null, XPathResult.ORDERED_NODE_SNAPSHOT_TYPE, null);
  let n = 0;
  for (let i = 0; i < snap.snapshotLength; i++) {
    const node = snap.snapshotItem(i);
    if (!node || node.nodeType !== 1) continue;
    node.setAttribute(%q, %q);
    n++;
  }
  return n;
})()`, xpJSON, xpathMarkAttr, token)

	raw, err := ev.EvalCDP(markJS)
	if err != nil {
		return nil, fmt.Errorf("evaluate xpath %q: %w", xpath, err)
	}
	marked, _ := toFloat64Ok(raw)
	if marked <= 0 {
		return nil, nil
	}

	sel := fmt.Sprintf(`[%s="%s"]`, xpathMarkAttr, token)
	elems, err := page.Elements(sel)
	// Best-effort cleanup: the handles do not depend on the tag.
	_, _ = ev.EvalCDP(fmt.Sprintf(`(() => { document.querySelectorAll(%q).forEach(e => e.removeAttribute(%q)); return true; })()`, sel, xpathMarkAttr))
	if err != nil {
		return nil, fmt.Errorf("resolve xpath %q matches: %w", xpath, err)
	}
	if len(elems) == 0 {
		return nil, fmt.Errorf("xpath %q matched %d element(s) but none could be resolved", xpath, int(marked))
	}
	return elems, nil
}

func randomToken() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("xpath marker token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// findElement looks up one selector (CSS or XPath) on any page.
func findElement(page browser.PageInterface, sel string, timeout time.Duration) (browser.ElementHandle, error) {
	var el browser.ElementHandle
	var err error
	if isXPath(sel) {
		el, err = page.ElementX(sel, timeout)
	} else {
		el, err = page.Element(sel, timeout)
	}
	if err == nil && el == nil {
		err = fmt.Errorf("element not found: %s", sel)
	}
	return el, err
}

// raceProbeTimeout bounds a single selector probe while polling a mixed
// CSS/XPath selector list, and racePollInterval spaces the polling rounds.
const (
	raceProbeTimeout = 300 * time.Millisecond
	racePollInterval = 200 * time.Millisecond
)

// findFirst waits up to timeout for any of selectors to match and returns the
// winning index and element. Earlier selectors win when several match at the
// same time.
//
// All-CSS lists use page.Race (one driver-side wait). Lists containing an
// XPath are polled: each round probes every selector in order with a short
// timeout until one matches or the deadline passes.
func findFirst(page browser.PageInterface, selectors []string, timeout time.Duration) (int, browser.ElementHandle, error) {
	if len(selectors) == 0 {
		return -1, nil, errors.New("no selectors")
	}
	if len(selectors) == 1 {
		el, err := findElement(page, selectors[0], timeout)
		if err != nil {
			return -1, nil, err
		}
		return 0, el, nil
	}

	allCSS := true
	for _, s := range selectors {
		if isXPath(s) {
			allCSS = false
			break
		}
	}
	if allCSS {
		idx, el, err := page.Race(selectors, timeout)
		if err != nil {
			return -1, nil, err
		}
		if el == nil {
			return -1, nil, fmt.Errorf("none of %d selectors matched within %v", len(selectors), timeout)
		}
		return idx, el, nil
	}

	deadline := time.Now().Add(timeout)
	var firstErr error
	for {
		for i, sel := range selectors {
			probe := raceProbeTimeout
			if rem := time.Until(deadline); rem < probe {
				probe = rem
			}
			if probe <= 0 {
				probe = time.Millisecond
			}
			el, err := findElement(page, sel, probe)
			if err == nil {
				return i, el, nil
			}
			if firstErr == nil {
				firstErr = err
			}
		}
		if time.Until(deadline) <= 0 {
			break
		}
		time.Sleep(minDuration(racePollInterval, time.Until(deadline)))
		if time.Until(deadline) <= 0 {
			break
		}
	}
	return -1, nil, fmt.Errorf("none of %d selectors matched within %v: %w", len(selectors), timeout, firstErr)
}

func minDuration(a, b time.Duration) time.Duration {
	if b < a {
		if b < 0 {
			return 0
		}
		return b
	}
	return a
}

// findWithAlternatives resolves primary, falling back to alternatives, on any
// page driver. Rod keeps bot.FindElementWithAlternatives' sequential probing;
// other drivers race the whole list (primary first).
func findWithAlternatives(page browser.PageInterface, primary string, alternatives []string, timeout time.Duration) (browser.ElementHandle, error) {
	var alts []string
	for _, a := range alternatives {
		if strings.TrimSpace(a) != "" {
			alts = append(alts, a)
		}
	}
	anyXPath := isXPath(primary)
	for _, a := range alts {
		anyXPath = anyXPath || isXPath(a)
	}
	if rodPage := unwrapRodPage(page); rodPage != nil && !anyXPath {
		el, err := bot.FindElementWithAlternatives(rodPage, primary, alts, timeout)
		if err != nil {
			return nil, err
		}
		return browser.NewRodElement(el), nil
	}
	_, el, err := findFirst(page, append([]string{primary}, alts...), timeout)
	if err != nil && len(alts) > 0 {
		return nil, fmt.Errorf("no element found for primary %q or %d alternatives within %v: %w", primary, len(alts), timeout, err)
	}
	return el, err
}

// waitForOutcomes waits for any of the labelled outcome selectors to appear
// and returns the winning label. Rod uses bot.WaitForOutcome; other drivers
// race the selectors (ordered by label for a deterministic tie-break).
func waitForOutcomes(page browser.PageInterface, outcomes map[string]string, timeout time.Duration) (string, error) {
	if len(outcomes) == 0 {
		return "", errors.New("no outcomes provided")
	}
	if rodPage := unwrapRodPage(page); rodPage != nil {
		label, _, err := bot.WaitForOutcome(rodPage, outcomes, timeout)
		return label, err
	}
	labels := make([]string, 0, len(outcomes))
	for l := range outcomes {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	sels := make([]string, len(labels))
	for i, l := range labels {
		sels[i] = outcomes[l]
	}
	idx, _, err := findFirst(page, sels, timeout)
	if err != nil {
		return "", err
	}
	return labels[idx], nil
}

// firstChildHref returns the href of the first descendant <a href> of an
// element, read from the element's outerHTML so it works on any driver.
func firstChildHref(elem browser.ElementHandle) (string, bool) {
	if rodElem := unwrapRodElement(elem); rodElem != nil {
		var childHref *string
		_ = rod.Try(func() {
			a := rodElem.MustElement("a[href]")
			childHref, _ = a.Attribute("href")
		})
		if childHref == nil {
			return "", false
		}
		return *childHref, true
	}
	outer, err := elem.HTML()
	if err != nil || outer == "" {
		return "", false
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(outer))
	if err != nil {
		return "", false
	}
	return doc.Find("a[href]").First().Attr("href")
}
