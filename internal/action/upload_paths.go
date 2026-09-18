package action

import "strings"

// splitUploadPaths returns the file paths an upload step names: step.Text,
// or step.Value when it is a string, split on commas with blanks dropped.
func splitUploadPaths(step StepDef) []string {
	filePath := step.Text
	if filePath == "" {
		if s, ok := step.Value.(string); ok {
			filePath = s
		}
	}
	var files []string
	for _, f := range strings.Split(filePath, ",") {
		if f = strings.TrimSpace(f); f != "" {
			files = append(files, f)
		}
	}
	return files
}
