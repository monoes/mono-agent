// internal/applications/applications.go

// Package applications tracks job and tender applications under one shared
// pipeline: a closed 4-stage status lifecycle, free-form tags, and an
// append-only audit trail, with kind-specific fields held in typed detail
// tables rather than a JSON blob. See
// docs/mastermind/specs/2026-09-05-applications-foundation-design.md.
package applications

import "errors"

// Kind is the closed set of application kinds.
type Kind string

const (
	KindJob    Kind = "job"
	KindTender Kind = "tender"
)

// Status is the closed 4-stage lifecycle.
type Status string

const (
	StatusPending   Status = "pending"
	StatusApplied   Status = "applied"
	StatusRejected  Status = "rejected"
	StatusCancelled Status = "cancelled"
)

// Actor identifies who performed a status transition.
type Actor string

const (
	ActorUser   Actor = "user"
	ActorSystem Actor = "system"
)

// Application is the shared pipeline row. Exactly one of Job/Tender is set,
// matching Kind.
type Application struct {
	ID        string
	ProfileID string
	Kind      Kind
	Status    Status
	Tags      []string
	CreatedAt string // RFC3339
	UpdatedAt string // RFC3339
	Job       *JobDetails
	Tender    *TenderDetails
}

// JobDetails holds job-kind-specific fields. Company and URL are required.
type JobDetails struct {
	Company         string
	URL             string
	Location        string
	Description     string
	CompensationMin *float64
	CompensationMax *float64
	Currency        string
	JobType         string
	IsRemote        *bool
	Source          string
	PostedAt        string // free-form date string, e.g. RFC3339 or "2026-01-15"
}

// TenderDetails holds tender-kind-specific fields. IssuingOrg, URL, and
// SubmissionDeadline are required.
type TenderDetails struct {
	IssuingOrg             string
	URL                    string
	Description            string
	SubmissionDeadline     string // free-form date string, required
	EstimatedValue         *float64
	Currency               string
	RequiredCertifications string // comma-separated for Phase 1
	BidDocumentsRequired   string // comma-separated for Phase 1
	Source                 string
	PublishedAt            string
}

// StatusLogEntry is one append-only row of an application's transition
// ledger. FromStatus is "" for the initial creation entry.
type StatusLogEntry struct {
	ID         string
	FromStatus string
	ToStatus   Status
	Actor      Actor
	Note       string
	CreatedAt  string
}

// ListFilter narrows Store.List. A zero-value field means "any".
type ListFilter struct {
	Kind   Kind
	Status Status
	Tag    string
}

// Sentinel errors. Callers use errors.Is against these; wrapped errors carry
// the specific detail (e.g. which field was missing) in their message.
var (
	// ErrNotFound is returned when an application ID does not exist under
	// the given profile.
	ErrNotFound = errors.New("applications: not found")
	// ErrInvalidTransition is returned when a status change is not allowed
	// by the transition graph (including from a terminal state).
	ErrInvalidTransition = errors.New("applications: invalid status transition")
	// ErrInvalidInput is returned for a missing required field or a
	// kind/detail mismatch.
	ErrInvalidInput = errors.New("applications: invalid input")
)
