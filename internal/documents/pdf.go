package documents

import (
	"context"
	"fmt"
	"io"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// RenderPDFFunc is RenderPDF's implementation, exposed as a swappable
// package-level variable so tests can inject a fake and exercise callers
// like GenerateDocument without a real browser. RenderPDF always calls
// through this var, so production code goes through whichever
// implementation is currently assigned.
var RenderPDFFunc = renderPDFImpl

// RenderPDF launches a standalone, unauthenticated headless browser (the
// same launcher.New()/rod.New() pattern cmd/monoagentcli/crawl.go already
// uses — not the login-session infrastructure in
// internal/nodes/browser_adapter.go), loads html directly, and returns the
// rendered PDF bytes via Chrome's native "Print to PDF". The browser is
// launched and closed within this single call.
func RenderPDF(ctx context.Context, html string) ([]byte, error) {
	return RenderPDFFunc(ctx, html)
}

func renderPDFImpl(ctx context.Context, html string) ([]byte, error) {
	// NoSandbox(true): this browser only ever renders locally-generated,
	// html/template-escaped content (never navigates to a live URL — see
	// SetDocumentContent below) and is closed within this single call, so
	// Chrome's sandbox provides little protection here that matters, unlike
	// internal/apply/browser.go's interactive browser, which DOES navigate
	// to real external job-posting URLs and must keep its sandbox. Without
	// this, launch fails outright on CI runners that restrict unprivileged
	// user namespaces (confirmed directly: GitHub Actions' current Ubuntu
	// runner image rejects Chrome's own sandboxed zygote process with "No
	// usable sandbox!" even though the same code runs fine in a dev
	// sandbox that permits it).
	launchURL, err := launcher.New().Headless(true).NoSandbox(true).Launch()
	if err != nil {
		return nil, fmt.Errorf("documents.RenderPDF: launch browser: %w", err)
	}

	browser := rod.New().ControlURL(launchURL).Context(ctx)
	if err := browser.Connect(); err != nil {
		return nil, fmt.Errorf("documents.RenderPDF: connect to browser: %w", err)
	}
	defer browser.Close()

	page, err := browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return nil, fmt.Errorf("documents.RenderPDF: open page: %w", err)
	}
	defer page.Close()

	if err := page.SetDocumentContent(html); err != nil {
		return nil, fmt.Errorf("documents.RenderPDF: set document content: %w", err)
	}
	if err := page.WaitLoad(); err != nil {
		return nil, fmt.Errorf("documents.RenderPDF: wait load: %w", err)
	}

	stream, err := page.PDF(&proto.PagePrintToPDF{PrintBackground: true})
	if err != nil {
		return nil, fmt.Errorf("documents.RenderPDF: print to pdf: %w", err)
	}
	pdfBytes, err := io.ReadAll(stream)
	if err != nil {
		return nil, fmt.Errorf("documents.RenderPDF: reading pdf stream: %w", err)
	}
	return pdfBytes, nil
}
