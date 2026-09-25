package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"

	"github.com/monoes/mono-agent/internal/peopletags"
	"github.com/spf13/cobra"
)

// newPeopleTagCmd manages people's tags. The GUI's People page makes every
// tag change through these commands.
func newPeopleTagCmd(cfg *globalConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tag",
		Short: "Tag people: list, add, remove, recolour",
		Long: "Tags belong to the profile and are matched by name, case-insensitively. A person " +
			"has at most 10. Tags are addressed by id or name.",
	}
	cmd.AddCommand(newPeopleTagListCmd(cfg), newPeopleTagAddCmd(cfg), newPeopleTagRemoveCmd(cfg), newPeopleTagColorCmd(cfg))
	return cmd
}

func newPeopleTagListCmd(cfg *globalConfig) *cobra.Command {
	var person string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the profile's tags, or one person's",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withTagDB(cfg, func(db *sql.DB) error {
				tags, err := peopletags.List(cmd.Context(), db, cfg.ProfileID, person)
				if err != nil {
					return tagErr(err)
				}
				if cfg.JSONOutput {
					return printReviewJSON(tags)
				}
				return printTags(cfg, tags...)
			})
		},
	}
	cmd.Flags().StringVar(&person, "person", "", "Only this person's tags")
	return cmd
}

func newPeopleTagAddCmd(cfg *globalConfig) *cobra.Command {
	var color string
	cmd := &cobra.Command{
		Use:   "add <person-id> <tag-name>",
		Short: "Tag a person, creating the tag when new",
		Long: "Tags a person with the profile's tag of that name, creating it when new. --color " +
			"sets a new tag's colour, and recolours an existing tag everywhere.",
		Example: `  monoagentcli people tag add 3f2a… "hot lead" --color "#ff5500"`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withTagDB(cfg, func(db *sql.DB) error {
				t, err := peopletags.Add(cmd.Context(), db, cfg.ProfileID, args[0], args[1], color)
				if err != nil {
					return tagErr(err)
				}
				return printTags(cfg, t)
			})
		},
	}
	cmd.Flags().StringVar(&color, "color", "", "Hex colour, e.g. #ff5500")
	return cmd
}

func newPeopleTagRemoveCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <person-id> <tag>",
		Short: "Untag a person (the tag itself stays)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withTagDB(cfg, func(db *sql.DB) error {
				if err := peopletags.Remove(cmd.Context(), db, cfg.ProfileID, args[0], args[1]); err != nil {
					return tagErr(err)
				}
				if cfg.JSONOutput {
					return printReviewJSON(map[string]string{"person_id": args[0], "removed": args[1]})
				}
				fmt.Printf("Removed %s from %s.\n", args[1], args[0])
				return nil
			})
		},
	}
}

func newPeopleTagColorCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:     "color <tag> <hex-colour>",
		Short:   "Recolour a tag for everyone who has it",
		Example: `  monoagentcli people tag color "hot lead" "#ff5500"`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withTagDB(cfg, func(db *sql.DB) error {
				t, err := peopletags.SetColor(cmd.Context(), db, cfg.ProfileID, args[0], args[1])
				if err != nil {
					return tagErr(err)
				}
				return printTags(cfg, t)
			})
		},
	}
}

func withTagDB(cfg *globalConfig, fn func(db *sql.DB) error) error {
	db, err := initDB(cfg)
	if err != nil {
		return fmt.Errorf("initializing database: %w", err)
	}
	defer db.Close()
	return fn(db.DB)
}

func printTags(cfg *globalConfig, tags ...peopletags.Tag) error {
	if cfg.JSONOutput && len(tags) == 1 {
		return printReviewJSON(tags[0])
	}
	if len(tags) == 0 {
		fmt.Println("No tags.")
		return nil
	}
	table := newPlainTable(os.Stdout, []string{"ID", "Name", "Color"}, nil)
	for _, t := range tags {
		table.Append([]string{t.ID, t.Name, t.Color})
	}
	table.Render()
	return nil
}

func tagErr(err error) error {
	switch {
	case errors.Is(err, peopletags.ErrNotFound):
		return errNotFound("%v", err)
	case errors.Is(err, peopletags.ErrInvalid):
		return errInvalidInput("%v", err)
	}
	return err
}
