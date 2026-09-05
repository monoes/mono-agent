// internal/nodes/applications/create_schema.go
package applicationsnodes

// CreateNodeSchema documents the config keys CreateNode.Execute reads out
// of its map[string]interface{} config. See
// internal/tools/schemagen's package doc for the tag grammar.
type CreateNodeSchema struct {
	Kind string `json:"kind" schema:"label=Kind,type=select,required,options=job|tender,help=Which kind of application to create."`

	ProfileID string `json:"profile_id" schema:"label=Profile ID,type=text,default=default,help=Which profile owns this application. Defaults to 'default'."`

	Company     string `json:"company" schema:"label=Company,type=text,help=Job kind only: the hiring company (required for kind=job)."`
	URL         string `json:"url" schema:"label=URL,type=text,required,help=The job posting or tender URL."`
	Location    string `json:"location" schema:"label=Location,type=text,help=Job kind only: job location."`
	Description string `json:"description" schema:"label=Description,type=textarea,help=Free-text description."`

	CompensationMin float64 `json:"compensation_min" schema:"label=Compensation Min,type=number,help=Job kind only: minimum compensation."`
	CompensationMax float64 `json:"compensation_max" schema:"label=Compensation Max,type=number,help=Job kind only: maximum compensation."`
	Currency        string  `json:"currency" schema:"label=Currency,type=text,help=Currency code， e.g. USD."`
	JobType         string  `json:"job_type" schema:"label=Job Type,type=text,help=Job kind only: e.g. full_time， contract， internship."`
	IsRemote        bool    `json:"is_remote" schema:"label=Is Remote,type=boolean,help=Job kind only: whether the role is remote."`
	Source          string  `json:"source" schema:"label=Source,type=text,default=manual,help=Where this application came from， e.g. manual， linkedin."`
	PostedAt        string  `json:"posted_at" schema:"label=Posted At,type=text,help=Job kind only: when the job was posted."`

	IssuingOrg             string  `json:"issuing_org" schema:"label=Issuing Organization,type=text,help=Tender kind only: the organization issuing the tender (required for kind=tender)."`
	SubmissionDeadline     string  `json:"submission_deadline" schema:"label=Submission Deadline,type=text,help=Tender kind only: required."`
	EstimatedValue         float64 `json:"estimated_value" schema:"label=Estimated Value,type=number,help=Tender kind only."`
	RequiredCertifications string  `json:"required_certifications" schema:"label=Required Certifications,type=text,help=Tender kind only: comma-separated."`
	BidDocumentsRequired   string  `json:"bid_documents_required" schema:"label=Bid Documents Required,type=text,help=Tender kind only: comma-separated."`
	PublishedAt            string  `json:"published_at" schema:"label=Published At,type=text,help=Tender kind only."`
}
