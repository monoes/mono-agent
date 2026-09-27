package main

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strconv"

	"github.com/monoes/mono-agent/internal/vault"
	"github.com/spf13/cobra"
)

// newImageCmd manages the active profile's image vault (vault_images): the
// files workflows generate or the user uploads, addressable as @img-NNN.
// The GUI's Image Vault page goes through these commands.
func newImageCmd(cfg *globalConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "image",
		Short: "Manage the image vault: list, add, label, export, delete",
		Long: "Images belong to the active profile and are addressed by id (img-001, …). " +
			"Workflows refer to them as @img-001.",
	}
	cmd.AddCommand(
		newImageListCmd(cfg),
		newImageGetCmd(cfg),
		newImageDataCmd(cfg),
		newImageAddCmd(cfg),
		newImageLabelCmd(cfg),
		newImageDeleteCmd(cfg),
		newImageSearchCmd(cfg),
		newImageStatsCmd(cfg),
		newImageExportCmd(cfg),
	)
	return cmd
}

func withImageDB(cfg *globalConfig, fn func(db *sql.DB) error) error {
	db, err := initDB(cfg)
	if err != nil {
		return fmt.Errorf("initializing database: %w", err)
	}
	defer db.Close()
	return fn(db.DB)
}

// imageErr maps a vault lookup miss to exit 2.
func imageErr(err error) error {
	if errors.Is(err, vault.ErrImageNotFound) {
		return errNotFound("%v", err)
	}
	return err
}

func printImages(cmd *cobra.Command, cfg *globalConfig, images []vault.ImageEntry) error {
	if cfg.JSONOutput {
		return writeJSONTo(cmd.OutOrStdout(), images)
	}
	if len(images) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No images.")
		return nil
	}
	table := newPlainTable(cmd.OutOrStdout(), []string{"ID", "Label", "Source", "Size", "Added"}, nil)
	for _, im := range images {
		table.Append([]string{im.ID, truncateStr(im.Label, 40), im.Source, strconv.FormatInt(im.SizeBytes, 10), im.CreatedAt})
	}
	table.Render()
	return nil
}

func printImage(cmd *cobra.Command, cfg *globalConfig, im *vault.ImageEntry) error {
	if cfg.JSONOutput {
		return writeJSONTo(cmd.OutOrStdout(), im)
	}
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "ID:       %s\nLabel:    %s\nFile:     %s\nSize:     %d bytes\nSource:   %s\n", im.ID, im.Label, im.Path, im.SizeBytes, im.Source)
	if im.WorkflowID != "" {
		fmt.Fprintf(w, "Workflow: %s\n", im.WorkflowID)
	}
	if im.ExecutionID != "" {
		fmt.Fprintf(w, "Run:      %s\n", im.ExecutionID)
	}
	fmt.Fprintf(w, "Added:    %s\n", im.CreatedAt)
	return nil
}

func positiveLimit(limit int) error {
	if limit <= 0 {
		return errInvalidInput("--limit must be positive, got %d", limit)
	}
	return nil
}

func newImageListCmd(cfg *globalConfig) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the profile's images, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := positiveLimit(limit); err != nil {
				return err
			}
			return withImageDB(cfg, func(db *sql.DB) error {
				images, err := vault.ListImages(cmd.Context(), db, cfg.ProfileID, limit)
				if err != nil {
					return err
				}
				return printImages(cmd, cfg, images)
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 200, "Most images to list")
	return cmd
}

func newImageSearchCmd(cfg *globalConfig) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "search <text>",
		Short: "Find images whose label, filename, source or workflow contains text",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := positiveLimit(limit); err != nil {
				return err
			}
			return withImageDB(cfg, func(db *sql.DB) error {
				images, err := vault.SearchImages(cmd.Context(), db, cfg.ProfileID, args[0], limit)
				if err != nil {
					return err
				}
				return printImages(cmd, cfg, images)
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 100, "Most images to return")
	return cmd
}

func newImageGetCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Show one image's details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withImageDB(cfg, func(db *sql.DB) error {
				im, err := vault.GetImage(cmd.Context(), db, cfg.ProfileID, args[0])
				if err != nil {
					return imageErr(err)
				}
				return printImage(cmd, cfg, im)
			})
		},
	}
}

// imageMIMEType guesses a vault file's type from its extension; vault
// files without a recognised one are PNGs (vault.Register's default).
func imageMIMEType(path string) string {
	if t := mime.TypeByExtension(filepath.Ext(path)); t != "" {
		return t
	}
	return "image/png"
}

func newImageDataCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "data <id>",
		Short: "Print an image's contents as a base64 data URL",
		Long: "Prints the image as a data: URL (\"data:image/png;base64,…\"), which a web page can " +
			"display directly. With --json: {id, mime_type, size_bytes, data_url}. Without, the " +
			"data URL alone.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withImageDB(cfg, func(db *sql.DB) error {
				im, err := vault.GetImage(cmd.Context(), db, cfg.ProfileID, args[0])
				if err != nil {
					return imageErr(err)
				}
				data, err := os.ReadFile(im.Path)
				if err != nil {
					return fmt.Errorf("vault image file read: %w", err)
				}
				mimeType := imageMIMEType(im.Path)
				dataURL := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
				if !cfg.JSONOutput {
					_, err := fmt.Fprintln(cmd.OutOrStdout(), dataURL)
					return err
				}
				return writeJSONTo(cmd.OutOrStdout(), map[string]any{
					"id": im.ID, "mime_type": mimeType, "size_bytes": len(data), "data_url": dataURL,
				})
			})
		},
	}
}

func newImageAddCmd(cfg *globalConfig) *cobra.Command {
	var label, source string
	cmd := &cobra.Command{
		Use:   "add <file>",
		Short: "Copy an image file into the vault",
		Long: "Copies the file into the profile's vault folder as img-NNN<ext> and prints the new " +
			"image. The original file is left where it is.",
		Example: `  monoagentcli image add ~/Pictures/logo.png --label "Logo"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if fi, err := os.Stat(args[0]); err != nil {
				return errInvalidInput("%v", err)
			} else if fi.IsDir() {
				return errInvalidInput("%s is a directory, not an image file", args[0])
			}
			return withImageDB(cfg, func(db *sql.DB) error {
				ctx := vault.ContextWithProfileID(cmd.Context(), cfg.ProfileID)
				id, err := vault.Register(ctx, db, args[0], source, "", "")
				// Register indexes the image in the knowledge graph in the
				// background; let that finish before this process exits.
				defer vault.Wait()
				if err != nil {
					return fmt.Errorf("vault register: %w", err)
				}
				if label != "" {
					if err := vault.SetImageLabel(ctx, db, cfg.ProfileID, id, label); err != nil {
						return err
					}
				}
				im, err := vault.GetImage(ctx, db, cfg.ProfileID, id)
				if err != nil {
					return err
				}
				return printImage(cmd, cfg, im)
			})
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "Name to show for the image")
	cmd.Flags().StringVar(&source, "source", "upload", "Where the image came from")
	return cmd
}

func newImageLabelCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "label <id> [label]",
		Short: "Rename an image; without a label, clear it",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			label := ""
			if len(args) == 2 {
				label = args[1]
			}
			return withImageDB(cfg, func(db *sql.DB) error {
				if err := vault.SetImageLabel(cmd.Context(), db, cfg.ProfileID, args[0], label); err != nil {
					return imageErr(err)
				}
				if cfg.JSONOutput {
					return writeJSONTo(cmd.OutOrStdout(), map[string]string{"id": args[0], "label": label})
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Labelled %s %q.\n", args[0], label)
				return nil
			})
		},
	}
}

func newImageDeleteCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete an image and its file",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withImageDB(cfg, func(db *sql.DB) error {
				if err := vault.DeleteImage(cmd.Context(), db, cfg.ProfileID, args[0]); err != nil {
					return imageErr(err)
				}
				if cfg.JSONOutput {
					return writeJSONTo(cmd.OutOrStdout(), map[string]string{"id": args[0]})
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Deleted %s.\n", args[0])
				return nil
			})
		},
	}
}

func newImageStatsCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "stats",
		Short: "Count the profile's images and their total size",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withImageDB(cfg, func(db *sql.DB) error {
				st, err := vault.GetImageStats(cmd.Context(), db, cfg.ProfileID)
				if err != nil {
					return err
				}
				if cfg.JSONOutput {
					return writeJSONTo(cmd.OutOrStdout(), st)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%d images, %d bytes\n", st.Count, st.TotalBytes)
				return nil
			})
		},
	}
}

func newImageExportCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:     "export <id> <dest-file>",
		Short:   "Copy an image out of the vault to a file",
		Example: `  monoagentcli image export img-004 ~/Desktop/banner.png`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[1] == "" {
				return errInvalidInput("destination file is empty")
			}
			if fi, err := os.Stat(args[1]); err == nil && fi.IsDir() {
				return errInvalidInput("%s is a directory; give a file name", args[1])
			}
			return withImageDB(cfg, func(db *sql.DB) error {
				im, err := vault.GetImage(cmd.Context(), db, cfg.ProfileID, args[0])
				if err != nil {
					return imageErr(err)
				}
				dest, err := filepath.Abs(args[1])
				if err != nil {
					return errInvalidInput("%v", err)
				}
				if err := copyImageFile(im.Path, dest); err != nil {
					return err
				}
				if cfg.JSONOutput {
					return writeJSONTo(cmd.OutOrStdout(), map[string]string{"id": im.ID, "path": dest})
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Saved %s to %s.\n", im.ID, dest)
				return nil
			})
		},
	}
}

func copyImageFile(src, dst string) (retErr error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); retErr == nil {
			retErr = cerr
		}
	}()
	_, err = io.Copy(out, in)
	return err
}
