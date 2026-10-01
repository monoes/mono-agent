package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/personphoto"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/vault"
	"github.com/olekukonko/tablewriter/tw"
	"github.com/spf13/cobra"
)

func newPeopleCmd(cfg *globalConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "people",
		Aliases: []string{"person"},
		Short:   "Manage discovered people/profiles",
		Long:    "List, view, delete, and import people discovered during search and interaction actions.",
	}

	cmd.AddCommand(
		newPeopleListCmd(cfg),
		newPeopleCountCmd(cfg),
		newPeopleGetCmd(cfg),
		newPeopleInteractionsCmd(cfg),
		newPeoplePostsCmd(cfg),
		newPeopleDeleteCmd(cfg),
		newPeopleImportCmd(cfg),
		newPeopleMessagesCmd(cfg),
		newPeopleStatusCmd(cfg),
		newPeopleReviewCmd(cfg),
		newPeopleTagCmd(cfg),
		newPeopleLinksCmd(cfg),
		newPeoplePhotosCmd(cfg),
	)

	return cmd
}

// peopleFilter is the platform/search filter `people list` and `people count`
// share, so a page of results and its total always agree.
type peopleFilter struct {
	platform string
	search   string
}

// where returns the WHERE clause (profile-scoped) and its parameters. The
// platform matches case-insensitively; the search matches username or name.
func (f peopleFilter) where(profileID string) (string, []interface{}) {
	clause := " WHERE profile_id = ?"
	params := []interface{}{profileID}
	if f.platform != "" {
		clause += " AND UPPER(platform) = ?"
		params = append(params, strings.ToUpper(f.platform))
	}
	if f.search != "" {
		clause += " AND (platform_username LIKE ? OR full_name LIKE ?)"
		s := "%" + f.search + "%"
		params = append(params, s, s)
	}
	return clause, params
}

func (f *peopleFilter) addFlags(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&f.platform, "platform", "p", "", "Filter by platform (case-insensitive)")
	cmd.Flags().StringVarP(&f.search, "search", "s", "", "Only people whose username or name contains this")
}

func newPeopleListCmd(cfg *globalConfig) *cobra.Command {
	var (
		filter peopleFilter
		limit  int
		offset int
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List people in the database, newest first",
		Example: `  monoagentcli people list
  monoagentcli people list --platform instagram --limit 20
  monoagentcli people list --search sam --limit 50 --offset 50 --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 0 || offset < 0 {
				return errInvalidInput("--limit and --offset must not be negative")
			}
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.Close()

			where, params := filter.where(cfg.ProfileID)
			query := `SELECT id, platform_username, platform, COALESCE(full_name,''),
			                 COALESCE(image_url,''), COALESCE(profile_url,''),
			                 COALESCE(follower_count,''), COALESCE(following_count,0), COALESCE(is_verified,0),
			                 COALESCE(category,''), COALESCE(job_title,''), COALESCE(created_at,'')
			          FROM people` + where + " ORDER BY created_at DESC"
			switch {
			case limit > 0:
				query += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
			case offset > 0:
				query += fmt.Sprintf(" LIMIT -1 OFFSET %d", offset)
			}

			rows, err := db.DB.Query(query, params...)
			if err != nil {
				return fmt.Errorf("querying people: %w", err)
			}
			defer rows.Close()

			type personSummary struct {
				ID               string `json:"id"`
				PlatformUsername string `json:"platform_username"`
				Platform         string `json:"platform"`
				FullName         string `json:"full_name"`
				ImageURL         string `json:"image_url"`
				ProfileURL       string `json:"profile_url"`
				FollowerCount    string `json:"follower_count"`
				FollowingCount   int    `json:"following_count"`
				IsVerified       bool   `json:"is_verified"`
				Category         string `json:"category,omitempty"`
				JobTitle         string `json:"job_title,omitempty"`
				CreatedAt        string `json:"created_at"`
			}

			people := []personSummary{}
			for rows.Next() {
				var p personSummary
				var verified sql.NullInt64
				if err := rows.Scan(
					&p.ID, &p.PlatformUsername, &p.Platform, &p.FullName,
					&p.ImageURL, &p.ProfileURL,
					&p.FollowerCount, &p.FollowingCount,
					&verified, &p.Category, &p.JobTitle, &p.CreatedAt,
				); err != nil {
					return fmt.Errorf("scanning person: %w", err)
				}
				p.IsVerified = verified.Valid && verified.Int64 != 0
				people = append(people, p)
			}
			if err := rows.Err(); err != nil {
				return fmt.Errorf("iterating people: %w", err)
			}

			if cfg.JSONOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(people)
			}

			if len(people) == 0 {
				fmt.Println("No people found.")
				return nil
			}

			table := newPlainTable(os.Stdout, []string{"ID", "Username", "Platform", "Name", "Followers", "Verified"}, nil)

			for _, p := range people {
				shortID := p.ID
				if len(shortID) > 8 {
					shortID = shortID[:8]
				}
				verifiedStr := ""
				if p.IsVerified {
					verifiedStr = "yes"
				}
				table.Append([]string{
					shortID,
					truncateStr(p.PlatformUsername, 20),
					p.Platform,
					truncateStr(p.FullName, 20),
					p.FollowerCount,
					verifiedStr,
				})
			}
			table.Render()
			fmt.Fprintf(os.Stderr, "\nTotal: %d person(s)\n", len(people))
			return nil
		},
	}

	filter.addFlags(cmd)
	cmd.Flags().IntVarP(&limit, "limit", "n", 50, "Maximum number of results (0 = no limit)")
	cmd.Flags().IntVar(&offset, "offset", 0, "Skip this many results first, for paging")

	return cmd
}

// newPeopleCountCmd counts the people `people list` would page through with
// the same filters — the People page's total.
func newPeopleCountCmd(cfg *globalConfig) *cobra.Command {
	var filter peopleFilter
	cmd := &cobra.Command{
		Use:     "count",
		Short:   "Count people, with the same filters as people list",
		Example: `  monoagentcli --json people count --platform linkedin --search sam`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.Close()

			where, params := filter.where(cfg.ProfileID)
			var count int
			if err := db.DB.QueryRow("SELECT COUNT(*) FROM people"+where, params...).Scan(&count); err != nil {
				return fmt.Errorf("counting people: %w", err)
			}
			if cfg.JSONOutput {
				return printReviewJSON(map[string]int{"count": count})
			}
			fmt.Println(count)
			return nil
		},
	}
	filter.addFlags(cmd)
	return cmd
}

func newPeoplePhotosCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "photos",
		Short: "Download the profile photos still stored as remote URLs",
		Long: "Saves a local copy (in the image vault) of every person's photo that is still a remote URL, " +
			"and points the person at it. Photos that can no longer be fetched keep their URL.",
		Example: `  monoagentcli --json people photos`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.Close()

			saved, errs := personphoto.Localize(cmd.Context(), db.DB, cfg.ProfileID, nil)
			vault.Wait()
			if cfg.JSONOutput {
				failed := make([]string, len(errs))
				for i, e := range errs {
					failed[i] = e.Error()
				}
				return printReviewJSON(map[string]interface{}{"saved": saved, "failed": failed})
			}
			fmt.Printf("saved %d photos, %d could not be fetched\n", saved, len(errs))
			for _, e := range errs {
				fmt.Fprintln(cmd.ErrOrStderr(), e)
			}
			return nil
		},
	}
}

func newPeopleGetCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Show detailed information about a person",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			personID := args[0]

			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.Close()

			var p storage.Person
			var verified, contentCount, followingCount sql.NullInt64
			var fullName, imageURL, contactDetails, website, profileURL sql.NullString
			var followerCount, introduction, category, jobTitle sql.NullString
			var headline, location, about, experience, education, details sql.NullString

			err = db.DB.QueryRow(
				`SELECT id, COALESCE(platform_username, ''), COALESCE(platform, ''), full_name,
				        image_url, contact_details,
				        website, COALESCE(content_count, 0), follower_count,
				        COALESCE(following_count, 0), introduction, COALESCE(is_verified, 0),
				        category, job_title,
				        created_at, updated_at, profile_url,
				        headline, location, about, experience, education, profile_details
				 FROM people WHERE id = ? AND profile_id = ?`, personID, cfg.ProfileID,
			).Scan(
				&p.ID, &p.PlatformUsername, &p.Platform, &fullName,
				&imageURL, &contactDetails, &website, &contentCount,
				&followerCount, &followingCount, &introduction,
				&verified, &category, &jobTitle, &p.CreatedAt, &p.UpdatedAt, &profileURL,
				&headline, &location, &about, &experience, &education, &details,
			)
			if err == sql.ErrNoRows {
				return errNotFound("person %q not found", personID)
			}
			if err != nil {
				return fmt.Errorf("querying person: %w", err)
			}

			p.FullName = fullName.String
			p.ImageURL = imageURL.String
			p.ContactDetails = contactDetails.String
			p.Website = website.String
			p.FollowerCount = followerCount.String
			p.ContentCount = int(contentCount.Int64)
			p.FollowingCount = int(followingCount.Int64)
			p.Introduction = introduction.String
			p.IsVerified = verified.Valid && verified.Int64 != 0
			p.Category = category.String
			p.JobTitle = jobTitle.String
			p.Headline = headline.String
			p.Location = location.String
			p.About = about.String
			p.Experience = jsonArrayOrNil(experience.String)
			p.Education = jsonArrayOrNil(education.String)
			p.ProfileDetails = jsonObjectOrNil(details.String)

			links := confirmedLinks(db, cfg.ProfileID, p.ID)
			if cfg.JSONOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(struct {
					storage.Person
					ProfileURL string          `json:"profile_url,omitempty"`
					Links      []confirmedLink `json:"links,omitempty"`
				}{p, profileURL.String, links})
			}

			table := newPlainTable(os.Stdout, []string{"Field", "Value"}, []tw.Align{tw.AlignRight, tw.AlignLeft})

			table.Append([]string{"ID", p.ID})
			table.Append([]string{"Username", p.PlatformUsername})
			table.Append([]string{"Platform", p.Platform})
			table.Append([]string{"Full Name", p.FullName})
			table.Append([]string{"Verified", fmt.Sprintf("%v", p.IsVerified)})
			table.Append([]string{"Category", p.Category})
			table.Append([]string{"Job Title", p.JobTitle})
			if p.Headline != "" {
				table.Append([]string{"Headline", p.Headline})
			}
			if p.Location != "" {
				table.Append([]string{"Location", p.Location})
			}
			table.Append([]string{"Followers", p.FollowerCount})
			table.Append([]string{"Following", fmt.Sprintf("%d", p.FollowingCount)})
			table.Append([]string{"Content Count", fmt.Sprintf("%d", p.ContentCount)})
			if p.Website != "" {
				table.Append([]string{"Website", p.Website})
			}
			if p.ContactDetails != "" {
				table.Append([]string{"Contact", p.ContactDetails})
			}
			if p.About != "" {
				table.Append([]string{"About", truncateStr(p.About, 60)})
			}
			if p.Introduction != "" {
				table.Append([]string{"Introduction", truncateStr(p.Introduction, 60)})
			}
			if p.ImageURL != "" {
				table.Append([]string{"Image", truncateStr(p.ImageURL, 60)})
			}
			for i, line := range profileEntryLines(p.Experience, "title", "company", "date_range") {
				table.Append([]string{labelOnFirst(i, "Experience"), line})
			}
			for i, line := range profileEntryLines(p.Education, "school", "degree", "date_range") {
				table.Append([]string{labelOnFirst(i, "Education"), line})
			}
			for _, row := range profileDetailRows(p.ProfileDetails) {
				table.Append(row)
			}
			for _, l := range links {
				table.Append([]string{"Same person as", l.String()})
			}
			table.Append([]string{"Created", p.CreatedAt.Format("2006-01-02 15:04:05")})
			table.Append([]string{"Updated", p.UpdatedAt.Format("2006-01-02 15:04:05")})
			table.Render()

			return nil
		},
	}
}

func newPeopleDeleteCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a person from the database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			personID := args[0]

			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.Close()

			result, err := db.DB.Exec("DELETE FROM people WHERE id = ? AND profile_id = ?", personID, cfg.ProfileID)
			if err != nil {
				return fmt.Errorf("deleting person: %w", err)
			}

			affected, _ := result.RowsAffected()
			if affected == 0 {
				return fmt.Errorf("person %q not found", personID)
			}

			fmt.Fprintf(os.Stdout, "Deleted person %s.\n", personID)
			return nil
		},
	}
}

func newPeopleImportCmd(cfg *globalConfig) *cobra.Command {
	var (
		filePath string
		platform string
	)

	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import people from a JSON array file",
		Example: `  monoagentcli people import --file people.json --platform instagram
  monoagentcli people import --file contacts.json --platform linkedin`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if filePath == "" {
				return fmt.Errorf("--file is required")
			}
			if platform == "" {
				return fmt.Errorf("--platform is required")
			}

			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.Close()

			data, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("reading file %s: %w", filePath, err)
			}

			var rawPeople []map[string]interface{}
			if err := json.Unmarshal(data, &rawPeople); err != nil {
				return fmt.Errorf("parsing JSON array: %w", err)
			}

			if len(rawPeople) == 0 {
				fmt.Fprintln(os.Stderr, "No people found in file.")
				return nil
			}

			tx, err := db.DB.Begin()
			if err != nil {
				return fmt.Errorf("beginning transaction: %w", err)
			}

			stmt, err := tx.Prepare(
				`INSERT INTO people (id, platform_username, platform, full_name, image_url,
				        contact_details, website, content_count, follower_count,
				        following_count, introduction, is_verified, category, job_title,
				        profile_id, created_at, updated_at)
				 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
				 ON CONFLICT(platform_username, platform, profile_id)
				 DO UPDATE SET
				   full_name       = excluded.full_name,
				   image_url       = excluded.image_url,
				   contact_details = excluded.contact_details,
				   website         = excluded.website,
				   content_count   = excluded.content_count,
				   follower_count  = excluded.follower_count,
				   following_count = excluded.following_count,
				   introduction    = excluded.introduction,
				   is_verified     = excluded.is_verified,
				   category        = excluded.category,
				   job_title       = excluded.job_title,
				   updated_at      = excluded.updated_at`,
			)
			if err != nil {
				tx.Rollback()
				return fmt.Errorf("preparing statement: %w", err)
			}
			defer stmt.Close()

			now := time.Now().UTC()
			var imported int

			for _, raw := range rawPeople {
				username, _ := raw["platform_username"].(string)
				if username == "" {
					username, _ = raw["username"].(string)
				}
				if username == "" {
					continue
				}

				fullName, _ := raw["full_name"].(string)
				imageURL, _ := raw["image_url"].(string)
				contactDetails, _ := raw["contact_details"].(string)
				website, _ := raw["website"].(string)
				introduction, _ := raw["introduction"].(string)
				category, _ := raw["category"].(string)
				jobTitle, _ := raw["job_title"].(string)
				followerCount, _ := raw["follower_count"].(string)

				var contentCount, followingCount int
				if v, ok := raw["content_count"].(float64); ok {
					contentCount = int(v)
				}
				if v, ok := raw["following_count"].(float64); ok {
					followingCount = int(v)
				}

				var isVerified int
				if v, ok := raw["is_verified"].(bool); ok && v {
					isVerified = 1
				}

				personID := storage.NewID()
				if id, ok := raw["id"].(string); ok && id != "" {
					personID = id
				}

				_, err := stmt.Exec(
					personID, username, strings.ToLower(platform),
					nullableStr(fullName), nullableStr(imageURL),
					nullableStr(contactDetails), nullableStr(website),
					contentCount, nullableStr(followerCount), followingCount,
					nullableStr(introduction), isVerified,
					nullableStr(category), nullableStr(jobTitle),
					cfg.ProfileID, now, now,
				)
				if err != nil {
					tx.Rollback()
					return fmt.Errorf("importing person %s: %w", username, err)
				}
				imported++
			}

			if err := tx.Commit(); err != nil {
				return fmt.Errorf("committing import: %w", err)
			}

			fmt.Fprintf(os.Stdout, "Imported %d person(s) for platform %s.\n", imported, platform)
			return nil
		},
	}

	cmd.Flags().StringVar(&filePath, "file", "", "Path to JSON file containing people array (required)")
	cmd.Flags().StringVar(&platform, "platform", "", "Platform for imported people (required)")
	_ = cmd.MarkFlagRequired("file")
	_ = cmd.MarkFlagRequired("platform")

	return cmd
}

// jsonArrayOrNil returns a stored JSON array as raw JSON, or nil when it is
// empty or not an array (so it drops out of the JSON output).
func jsonArrayOrNil(s string) json.RawMessage {
	var arr []json.RawMessage
	if s == "" || json.Unmarshal([]byte(s), &arr) != nil || len(arr) == 0 {
		return nil
	}
	return json.RawMessage(s)
}

// profileEntryLines renders each entry of a stored experience/education
// array as one line: the given fields that are set, joined by " · ".
func profileEntryLines(raw json.RawMessage, fields ...string) []string {
	var entries []map[string]interface{}
	if len(raw) == 0 || json.Unmarshal(raw, &entries) != nil {
		return nil
	}
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		var parts []string
		for _, f := range fields {
			if v, _ := e[f].(string); v != "" {
				parts = append(parts, v)
			}
		}
		if len(parts) > 0 {
			lines = append(lines, truncateStr(strings.Join(parts, " · "), 80))
		}
	}
	return lines
}

// jsonObjectOrNil returns a stored JSON object as raw JSON, or nil when it
// is empty or not an object.
func jsonObjectOrNil(s string) json.RawMessage {
	var obj map[string]json.RawMessage
	if s == "" || json.Unmarshal([]byte(s), &obj) != nil || len(obj) == 0 {
		return nil
	}
	return json.RawMessage(s)
}

// profileDetailLabels orders and labels the profile_details keys in the
// `people get` table; keys not listed follow, labelled by their name.
var profileDetailLabels = []struct{ key, label string }{
	{"profile_category", "Profile Category"},
	{"account_type", "Account Type"},
	{"verification_type", "Verification"},
	{"is_private", "Private"},
	{"pronouns", "Pronouns"},
	{"links", "Link"},
	{"threads_handle", "Threads"},
	{"contact", "Contact"},
	{"likes_count", "Likes"},
	{"friend_count", "Friends"},
	{"affiliates_count", "Affiliates"},
	{"connection_count", "Connections"},
	{"connection_degree", "Connection"},
	{"current_company", "Company"},
	{"join_date", "Joined"},
	{"birth_date", "Born"},
	{"language", "Language"},
	{"highlights", "Highlights"},
	{"pinned_post", "Pinned Post"},
	{"banner_url", "Banner"},
	{"platform_id", "Platform ID"},
}

// profileDetailRows renders a stored profile_details object as table rows:
// one row per link, lists joined, objects as their values.
func profileDetailRows(raw json.RawMessage) [][]string {
	var d map[string]interface{}
	if len(raw) == 0 || json.Unmarshal(raw, &d) != nil {
		return nil
	}
	var rows [][]string
	add := func(key, label string) {
		v, ok := d[key]
		if !ok {
			return
		}
		delete(d, key)
		if key == "links" {
			links, _ := v.([]interface{})
			for i, l := range links {
				m, _ := l.(map[string]interface{})
				u, _ := m["url"].(string)
				if t, _ := m["title"].(string); t != "" && t != u {
					u = t + " · " + u
				}
				if u != "" {
					rows = append(rows, []string{labelOnFirst(i, "Links"), truncateStr(u, 80)})
				}
			}
			return
		}
		if s := detailText(v); s != "" {
			rows = append(rows, []string{label, truncateStr(s, 80)})
		}
	}
	for _, l := range profileDetailLabels {
		add(l.key, l.label)
	}
	rest := make([]string, 0, len(d))
	for k := range d {
		rest = append(rest, k)
	}
	sort.Strings(rest)
	for _, k := range rest {
		add(k, k)
	}
	return rows
}

// detailText renders one profile_details value as text.
func detailText(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "yes"
		}
		return "no"
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case []interface{}:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			if s := detailText(e); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	case map[string]interface{}:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			if s := detailText(t[k]); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " · ")
	}
	return ""
}

func labelOnFirst(i int, label string) string {
	if i == 0 {
		return label
	}
	return ""
}

// nullableStr returns a sql.NullString; empty strings map to NULL.
func nullableStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
