package main

import "fmt"

// ─────────────────────────────────────────────────────────────────────────────
// Tags
// ─────────────────────────────────────────────────────────────────────────────

type TagInfo struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// GetAllTags returns every tag in the active profile, ordered by name —
// `people tag list`.
func (a *App) GetAllTags() []TagInfo {
	var tags []TagInfo
	if err := a.runMonoCLI("", &tags, "people", "tag", "list"); err != nil {
		return nil
	}
	return tags
}

// GetPersonTags returns all tags attached to the given person —
// `people tag list --person`.
func (a *App) GetPersonTags(personId string) []TagInfo {
	var tags []TagInfo
	if err := a.runMonoCLI("", &tags, "people", "tag", "list", "--person="+personId); err != nil {
		return nil
	}
	return tags
}

// AddPersonTag tags a person, creating the tag when new; a colour recolours
// an existing tag everywhere — `people tag add`. Returns the tag, or nil
// when it could not be added (unknown person, bad colour, 10 tags already).
func (a *App) AddPersonTag(personId, tagName, color string) *TagInfo {
	args := []string{"people", "tag", "add"}
	if color != "" {
		args = append(args, "--color="+color)
	}
	var t TagInfo
	if err := a.runMonoCLI("", &t, append(args, "--", personId, tagName)...); err != nil {
		a.emitLog("PEOPLE", "WARN", fmt.Sprintf("tagging %s with %q: %v", personId, tagName, err))
		return nil
	}
	return &t
}

// UpdateTagColor recolours a tag for everyone who has it — `people tag color`.
func (a *App) UpdateTagColor(tagId, color string) bool {
	if err := a.runMonoCLI("", nil, "people", "tag", "color", "--", tagId, color); err != nil {
		a.emitLog("PEOPLE", "WARN", fmt.Sprintf("recolouring tag %s: %v", tagId, err))
		return false
	}
	return true
}

// RemovePersonTag unlinks a tag from a person (the tag itself stays) —
// `people tag remove`.
func (a *App) RemovePersonTag(personId, tagId string) {
	if err := a.runMonoCLI("", nil, "people", "tag", "remove", "--", personId, tagId); err != nil {
		a.emitLog("PEOPLE", "WARN", fmt.Sprintf("untagging %s: %v", personId, err))
	}
}

// GetPeopleTagsMap returns a map of personId → []TagInfo for a slice of
// person IDs, in one call — `people tag map`. People without tags are left
// out.
func (a *App) GetPeopleTagsMap(personIds []string) map[string][]TagInfo {
	if len(personIds) == 0 {
		return nil
	}
	var byPerson map[string][]TagInfo
	if err := a.runMonoCLI("", &byPerson, append([]string{"people", "tag", "map", "--"}, personIds...)...); err != nil {
		return nil
	}
	return byPerson
}

// ─────────────────────────────────────────────────────────────────────────────
// Social Lists
// ─────────────────────────────────────────────────────────────────────────────

type SocialListInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ListType  string `json:"list_type"`
	ItemCount int    `json:"item_count"`
	CreatedAt string `json:"created_at"`
}

// GetSocialLists returns the active profile's social lists, newest first —
// `list ls`.
func (a *App) GetSocialLists() []SocialListInfo {
	var lists []SocialListInfo
	if err := a.runMonoCLI("", &lists, "list", "ls"); err != nil {
		return nil
	}
	return lists
}
