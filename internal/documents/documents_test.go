package documents_test

import (
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/documents"
)

func TestRenderCV(t *testing.T) {
	html, err := documents.Render(documents.DocTypeCV, documents.CVData{
		Name:    "Jane Doe",
		Title:   "Backend Engineer",
		Summary: "Experienced backend engineer.",
		Experience: []documents.ExperienceEntry{
			{Company: "Acme", JobTitle: "Senior Engineer", Dates: "2020-2025", Bullets: []string{"Built things"}},
		},
		Skills: []string{"Go", "SQL"},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{"Jane Doe", "Backend Engineer", "Senior Engineer, Acme", "Built things", "Go"} {
		if !strings.Contains(html, want) {
			t.Errorf("expected rendered CV to contain %q, got:\n%s", want, html)
		}
	}
}

func TestRenderCVEscapesUntrustedContent(t *testing.T) {
	html, err := documents.Render(documents.DocTypeCV, documents.CVData{
		Name: "<script>alert(1)</script>",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(html, "<script>") {
		t.Fatal("expected html/template to escape script content, found raw <script> tag")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Fatalf("expected escaped script tag in output, got:\n%s", html)
	}
}

func TestRenderCoverLetter(t *testing.T) {
	html, err := documents.Render(documents.DocTypeCoverLetter, documents.CoverLetterData{
		SenderName: "Jane Doe", RecipientName: "Hiring Manager", CompanyName: "Acme",
		Paragraphs: []string{"I am writing to apply for the role."},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{"Jane Doe", "Hiring Manager", "Acme", "I am writing to apply"} {
		if !strings.Contains(html, want) {
			t.Errorf("expected rendered cover letter to contain %q", want)
		}
	}
}

func TestRenderTenderProposal(t *testing.T) {
	html, err := documents.Render(documents.DocTypeTenderProposal, documents.TenderProposalData{
		CompanyName: "Acme Contracting", TenderTitle: "Road Maintenance Tender", IssuingOrg: "Ministry of Roads",
		Sections: []documents.ProposalSection{{Heading: "Technical Approach", Body: []string{"We propose..."}}},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{"Road Maintenance Tender", "Acme Contracting", "Ministry of Roads", "Technical Approach", "We propose"} {
		if !strings.Contains(html, want) {
			t.Errorf("expected rendered tender proposal to contain %q", want)
		}
	}
}

func TestRenderUnknownDocType(t *testing.T) {
	if _, err := documents.Render(documents.DocType("nonsense"), nil); err == nil {
		t.Fatal("expected error for unknown doc type, got nil")
	}
}
