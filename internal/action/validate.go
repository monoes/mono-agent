package action

// Issue is one validation finding.
type Issue struct {
	Severity string `json:"severity"` // "error" | "warning"
	StepID   string `json:"stepId,omitempty"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// Validate checks an action definition, optionally against the package it
// belongs to (pkg may be nil). Owned by the action-core builder.
func Validate(def *ActionDef, pkg PackageContext) []Issue { return nil }

// HasErrors reports whether any issue is an error.
func HasErrors(issues []Issue) bool {
	for _, i := range issues {
		if i.Severity == "error" {
			return true
		}
	}
	return false
}
