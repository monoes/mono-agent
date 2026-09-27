package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/monoes/mono-agent/internal/storage"
)

// ─────────────────────────────────────────────────────────────────────────────
// People
// ─────────────────────────────────────────────────────────────────────────────

type PersonInfo struct {
	ID             string `json:"id"`
	Username       string `json:"username"`
	Platform       string `json:"platform"`
	FullName       string `json:"full_name"`
	ImageURL       string `json:"image_url"`
	ProfileURL     string `json:"profile_url"`
	FollowerCount  string `json:"follower_count"`
	FollowingCount int    `json:"following_count"`
	IsVerified     bool   `json:"is_verified"`
	JobTitle       string `json:"job_title"`
	Category       string `json:"category"`
	CreatedAt      string `json:"created_at"`
}

type PersonDetailInfo struct {
	ID             string `json:"id"`
	Username       string `json:"username"`
	Platform       string `json:"platform"`
	FullName       string `json:"full_name"`
	ImageURL       string `json:"image_url"`
	ProfileURL     string `json:"profile_url"`
	FollowerCount  string `json:"follower_count"`
	FollowingCount int    `json:"following_count"`
	ContentCount   int    `json:"content_count"`
	IsVerified     bool   `json:"is_verified"`
	JobTitle       string `json:"job_title"`
	Category       string `json:"category"`
	Introduction   string `json:"introduction"`
	Website        string `json:"website"`
	ContactDetails string `json:"contact_details"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type PersonInteraction struct {
	ExecutionID      string `json:"execution_id"`
	NodeName         string `json:"node_name"`
	NodeType         string `json:"node_type"`
	Platform         string `json:"platform"`
	Link             string `json:"link"`
	Status           string `json:"status"`
	CommentText      string `json:"comment_text"`
	LastInteractedAt string `json:"last_interacted_at"`
	CreatedAt        string `json:"created_at"`
}

// PostSummary is returned by GetPersonPosts.
type PostSummary struct {
	ID           string `json:"id"`
	Shortcode    string `json:"shortcode"`
	URL          string `json:"url"`
	ThumbnailURL string `json:"thumbnail_url"`
	LikeCount    int    `json:"like_count"`
	CommentCount int    `json:"comment_count"`
	Caption      string `json:"caption"`
	PostedAt     string `json:"posted_at"`
	ScrapedAt    string `json:"scraped_at"`
	WeLiked      bool   `json:"we_liked"`
	WeCommented  bool   `json:"we_commented"`
}

// PostDetail is returned by GetPostDetail.
type PostDetail struct {
	ID           string `json:"id"`
	Shortcode    string `json:"shortcode"`
	URL          string `json:"url"`
	ThumbnailURL string `json:"thumbnail_url"`
	LikeCount    int    `json:"like_count"`
	CommentCount int    `json:"comment_count"`
	Caption      string `json:"caption"`
	PostedAt     string `json:"posted_at"`
	ScrapedAt    string `json:"scraped_at"`
}

// PostComment is returned by GetPostComments.
type PostComment struct {
	ID         string `json:"id"`
	Author     string `json:"author"`
	Text       string `json:"text"`
	Timestamp  string `json:"timestamp"`
	LikesCount int    `json:"likes_count"`
	ReplyCount int    `json:"reply_count"`
}

// peopleFilterArgs turns the People page's filter into `people list|count`
// flags; "ALL" means no platform filter.
func peopleFilterArgs(platform, search string) []string {
	var args []string
	if platform != "" && platform != "ALL" {
		args = append(args, "--platform="+platform)
	}
	if search != "" {
		args = append(args, "--search="+search)
	}
	return args
}

// GetPeople returns one page of the active profile's people, newest first —
// `people list`. limit <= 0 means every person (and ignores offset).
func (a *App) GetPeople(platform, search string, limit, offset int) []PersonInfo {
	args := append([]string{"people", "list"}, peopleFilterArgs(platform, search)...)
	if limit > 0 {
		args = append(args, "--limit", strconv.Itoa(limit), "--offset", strconv.Itoa(offset))
	} else {
		args = append(args, "--limit", "0")
	}
	// The CLI calls the username platform_username.
	var rows []struct {
		PersonInfo
		PlatformUsername string `json:"platform_username"`
	}
	if err := a.runMonoCLI("", &rows, args...); err != nil {
		return nil
	}
	people := make([]PersonInfo, len(rows))
	for i, r := range rows {
		people[i] = r.PersonInfo
		people[i].Username = r.PlatformUsername
	}
	return people
}

// GetPeopleCount is the People page's total for the same filter —
// `people count`.
func (a *App) GetPeopleCount(platform, search string) int {
	var res struct {
		Count int `json:"count"`
	}
	if err := a.runMonoCLI("", &res, append([]string{"people", "count"}, peopleFilterArgs(platform, search)...)...); err != nil {
		return 0
	}
	return res.Count
}

// GetPersonDetail returns one person, or nil when not in the active
// profile — `people get`.
func (a *App) GetPersonDetail(id string) *PersonDetailInfo {
	var p struct {
		PersonDetailInfo
		PlatformUsername string `json:"platform_username"`
	}
	if err := a.runMonoCLI("", &p, "people", "get", id); err != nil {
		return nil
	}
	p.Username = p.PlatformUsername
	return &p.PersonDetailInfo
}

// GetPersonInteractions lists what the profile's workflows did to a person,
// newest first — `people interactions`.
func (a *App) GetPersonInteractions(id string) []PersonInteraction {
	var out []PersonInteraction
	if err := a.runMonoCLI("", &out, "people", "interactions", id); err != nil {
		return nil
	}
	return out
}

// GetPersonMessages lists a person's messages, newest first
// (`people messages list <id>`). Each carries read_at.
func (a *App) GetPersonMessages(personID string) []*storage.PersonMessage {
	out := []*storage.PersonMessage{}
	if err := a.runMonoCLI("", &out, "people", "messages", "list", personID, "--limit", "100"); err != nil {
		return nil
	}
	return out
}

// GetAllPersonMessages is the cross-person feed (`people messages all`).
func (a *App) GetAllPersonMessages(limit int) []*storage.PersonMessageWithPerson {
	if limit <= 0 {
		limit = 100
	}
	out := []*storage.PersonMessageWithPerson{}
	if err := a.runMonoCLI("", &out, "people", "messages", "all", "--limit", strconv.Itoa(limit)); err != nil {
		return nil
	}
	return out
}

// MarkPersonMessagesRead marks inbound messages read — the given ids, or
// every message of personID when ids is empty (`people messages read`).
func (a *App) MarkPersonMessagesRead(personID string, ids []string) error {
	args := []string{"people", "messages", "read"}
	if personID != "" {
		args = append(args, "--person", personID)
	}
	return a.runMonoCLI("", nil, append(args, ids...)...)
}

// MarkPersonMessageUnread marks one inbound message unread again.
func (a *App) MarkPersonMessageUnread(id string) error {
	return a.runMonoCLI("", nil, "people", "messages", "unread", id)
}

// AddPersonMessage records a message/interaction for a person —
// `people messages add`.
func (a *App) AddPersonMessage(personID, source, externalID, direction, sender, subject, body string) error {
	args := []string{"people", "messages", "add", "--source=" + source}
	for _, f := range []struct{ flag, value string }{
		{"external-id", externalID}, {"direction", direction}, {"sender", sender},
		{"subject", subject}, {"body", body},
	} {
		if f.value != "" {
			args = append(args, "--"+f.flag+"="+f.value)
		}
	}
	return a.runMonoCLI("", nil, append(args, "--", personID)...)
}

// ComposePersonMessage sends (or drafts, when asDraft is true) an email to a
// person via the given Outlook connection and records it on their message
// history — `people messages compose`.
func (a *App) ComposePersonMessage(personID, connectionID, subject, body string, asDraft bool) (*storage.PersonMessage, error) {
	args := []string{"people", "messages", "compose",
		"--connection=" + connectionID, "--subject=" + subject, "--body=" + body}
	if asDraft {
		args = append(args, "--draft")
	}
	var msg storage.PersonMessage
	if err := a.runMonoCLI("", &msg, append(args, "--", personID)...); err != nil {
		return nil, err
	}
	return &msg, nil
}

// GetDraftPersonMessages returns all draft (unsent) outbound messages for the
// active profile, for review in the Human in Loop section —
// `people messages drafts`.
func (a *App) GetDraftPersonMessages() ([]*storage.PersonMessageWithPerson, error) {
	var drafts []*storage.PersonMessageWithPerson
	if err := a.runMonoCLI("", &drafts, "people", "messages", "drafts"); err != nil {
		return nil, err
	}
	return drafts, nil
}

// SendDraftPersonMessage sends a previously-created draft and marks it as
// sent — `people messages send-draft`.
func (a *App) SendDraftPersonMessage(personMessageID string) (*storage.PersonMessage, error) {
	var msg storage.PersonMessage
	if err := a.runMonoCLI("", &msg, "people", "messages", "send-draft", personMessageID); err != nil {
		return nil, err
	}
	return &msg, nil
}

// RejectDraftPersonMessage discards a draft, best-effort from the Outlook
// Drafts folder too — `people messages reject-draft`.
func (a *App) RejectDraftPersonMessage(personMessageID string) error {
	return a.runMonoCLI("", nil, "people", "messages", "reject-draft", personMessageID)
}

// PendingPersonApproval represents a lead or contact waiting for human review in Human-in-Loop.
type PendingPersonApproval struct {
	ID               string `json:"id"`
	Platform         string `json:"platform"`
	PlatformUsername string `json:"platform_username"`
	FullName         string `json:"full_name"`
	ImageUrl         string `json:"image_url"`
	ProfileUrl       string `json:"profile_url"`
	JobTitle         string `json:"job_title"`
	Category         string `json:"category"`
	Introduction     string `json:"introduction"`
	CreatedAt        string `json:"created_at"`
	// Suggestion is TypeSafe Jev's cached suggestion ({suggest, p,
	// intro_fit, intro_fit_p, model, at}) when the profile enabled surface
	// people_review; absent otherwise.
	Suggestion map[string]interface{} `json:"suggestion,omitempty"`
}

// GetPendingPeopleApprovals returns the active profile's people staged for
// review (category "pending_approval") — `people review list --suggest`.
func (a *App) GetPendingPeopleApprovals() ([]*PendingPersonApproval, error) {
	results := []*PendingPersonApproval{}
	if err := a.runMonoCLI("", &results, "people", "review", "list", "--suggest"); err != nil {
		return nil, err
	}
	return results, nil
}

// ApprovePendingPerson approves a person, replacing the introduction when
// editedIntroduction is set — `people review approve`. With sendNow the CLI
// also picks the profile's dispatch workflow (refusing, before approving,
// when there is none, several or only an inactive one) and returns the run,
// which starts here like any GUI run so it shows up in the run tracking.
func (a *App) ApprovePendingPerson(personID, editedIntroduction string, sendNow bool) error {
	args := []string{"people", "review", "approve", personID}
	if strings.TrimSpace(editedIntroduction) != "" {
		args = append(args, "--intro", editedIntroduction)
	}
	if sendNow {
		args = append(args, "--send-plan")
	}
	var res struct {
		Send *struct {
			WorkflowID string                 `json:"workflow_id"`
			Input      map[string]interface{} `json:"input"`
		} `json:"send"`
	}
	if err := a.runMonoCLI("", &res, args...); err != nil {
		return err
	}
	if res.Send == nil {
		return nil
	}
	input, err := json.Marshal(res.Send.Input)
	if err != nil {
		return err
	}
	if err := a.RunWorkflowWithInput(res.Send.WorkflowID, string(input)); err != nil {
		return fmt.Errorf("approved, but sending failed: %w", err)
	}
	return nil
}

// RejectPendingPerson takes a person out of the queue — `people review reject`.
func (a *App) RejectPendingPerson(personID string) error {
	return a.runMonoCLI("", nil, "people", "review", "reject", personID)
}

// GetLatestPersonStatus returns the most recent status update for a person,
// or nil if none exists yet — `people status get`.
func (a *App) GetLatestPersonStatus(personId string) *storage.PersonStatusUpdate {
	var u *storage.PersonStatusUpdate
	if err := a.runMonoCLI("", &u, "people", "status", "get", personId); err != nil {
		return nil
	}
	return u
}

// AddPersonStatus appends a new status update for a person —
// `people status set`.
func (a *App) AddPersonStatus(personId, text string) (*storage.PersonStatusUpdate, error) {
	var u storage.PersonStatusUpdate
	if err := a.runMonoCLI("", &u, "people", "status", "set", "--", personId, text); err != nil {
		return nil, err
	}
	return &u, nil
}

// GetPersonStatusHistory returns every status update for a person, newest
// first — `people status history`. limit <= 0 means no cap.
func (a *App) GetPersonStatusHistory(personId string, limit int) []*storage.PersonStatusUpdate {
	args := []string{"people", "status", "history", personId}
	if limit > 0 {
		args = append(args, "--limit", strconv.Itoa(limit))
	}
	var updates []*storage.PersonStatusUpdate
	if err := a.runMonoCLI("", &updates, args...); err != nil {
		return nil
	}
	return updates
}

// GetPersonPosts returns all scraped posts for a person, with
// we_liked/we_commented flags — `people posts list`.
func (a *App) GetPersonPosts(personID string) []PostSummary {
	posts := []PostSummary{}
	if err := a.runMonoCLI("", &posts, "people", "posts", "list", personID); err != nil || posts == nil {
		return []PostSummary{}
	}
	return posts
}

// GetPostDetail returns full metadata for a single post by ID —
// `people posts get`.
func (a *App) GetPostDetail(postID string) *PostDetail {
	var p PostDetail
	if err := a.runMonoCLI("", &p, "people", "posts", "get", postID); err != nil {
		return nil
	}
	return &p
}

// GetPostComments returns all scraped comments for a post, oldest first —
// `people posts comments`.
func (a *App) GetPostComments(postID string) []PostComment {
	comments := []PostComment{}
	if err := a.runMonoCLI("", &comments, "people", "posts", "comments", postID); err != nil || comments == nil {
		return []PostComment{}
	}
	return comments
}
