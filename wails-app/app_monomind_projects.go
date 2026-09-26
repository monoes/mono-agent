package main

// MonomindProjectInfo is one suggestion in the New Profile modal's "pick an
// existing monomind project" list.
type MonomindProjectInfo struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// ListMonomindProjects suggests existing monomind projects for the New
// Profile modal to offer — picking one fills both the profile's name and
// its root directory (`profile projects`: monomind's own project list,
// minus folders that are no longer monomind projects or already belong to
// a profile). Never errors: an empty result just means the modal shows no
// suggestions.
func (a *App) ListMonomindProjects() []MonomindProjectInfo {
	projects := []MonomindProjectInfo{}
	if err := a.cliJSON(profileCLITimeout, &projects, "profile", "projects"); err != nil || projects == nil {
		return []MonomindProjectInfo{}
	}
	return projects
}
