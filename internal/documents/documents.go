// Package documents renders structured CV/cover-letter/tender-proposal
// data into HTML (via html/template's auto-escaping) and, via pdf.go, PDF.
// See docs/mastermind/specs/2026-09-05-document-generation-design.md.
package documents

import (
	"embed"
	"fmt"
	"html/template"
	"strings"
)

//go:embed templates/*.html
var templatesFS embed.FS

// DocType selects which template/struct pair Render uses.
type DocType string

const (
	DocTypeCV             DocType = "cv"
	DocTypeCoverLetter    DocType = "cover_letter"
	DocTypeTenderProposal DocType = "tender_proposal"
)

// CVData is the structured input for the cv template.
type CVData struct {
	Name       string            `json:"name"`
	Title      string            `json:"title"`
	Email      string            `json:"email"`
	Phone      string            `json:"phone"`
	Location   string            `json:"location"`
	Summary    string            `json:"summary"`
	Experience []ExperienceEntry `json:"experience"`
	Education  []EducationEntry  `json:"education"`
	Skills     []string          `json:"skills"`
}

// ExperienceEntry is one job history entry within CVData.
type ExperienceEntry struct {
	Company  string   `json:"company"`
	JobTitle string   `json:"jobTitle"`
	Dates    string   `json:"dates"`
	Bullets  []string `json:"bullets"`
}

// EducationEntry is one education entry within CVData.
type EducationEntry struct {
	School string `json:"school"`
	Degree string `json:"degree"`
	Dates  string `json:"dates"`
}

// CoverLetterData is the structured input for the cover_letter template.
type CoverLetterData struct {
	SenderName    string   `json:"senderName"`
	RecipientName string   `json:"recipientName"`
	CompanyName   string   `json:"companyName"`
	Date          string   `json:"date"`
	Paragraphs    []string `json:"paragraphs"`
}

// TenderProposalData is the structured input for the tender_proposal template.
type TenderProposalData struct {
	CompanyName string            `json:"companyName"`
	TenderTitle string            `json:"tenderTitle"`
	IssuingOrg  string            `json:"issuingOrg"`
	Date        string            `json:"date"`
	Sections    []ProposalSection `json:"sections"`
}

// ProposalSection is one section within TenderProposalData.
type ProposalSection struct {
	Heading string   `json:"heading"`
	Body    []string `json:"body"`
}

// Render executes the template for docType against data, returning the
// rendered HTML. Uses html/template, not text/template — its auto-escaping
// is the actual defense against a data field containing markup/script
// content (see the design spec's rationale).
func Render(docType DocType, data interface{}) (string, error) {
	filename, ok := map[DocType]string{
		DocTypeCV:             "cv.html",
		DocTypeCoverLetter:    "cover_letter.html",
		DocTypeTenderProposal: "tender_proposal.html",
	}[docType]
	if !ok {
		return "", fmt.Errorf("documents.Render: unknown doc type %q", docType)
	}

	tmpl, err := template.ParseFS(templatesFS, "templates/"+filename)
	if err != nil {
		return "", fmt.Errorf("documents.Render: parsing template: %w", err)
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("documents.Render: executing template: %w", err)
	}
	return buf.String(), nil
}
