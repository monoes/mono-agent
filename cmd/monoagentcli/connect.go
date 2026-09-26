package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/monoes/mono-agent/internal/connections"
	"github.com/monoes/mono-agent/internal/i18n"
	"github.com/spf13/cobra"
)

// newConnectCmd returns the `connect` cobra command.
// Usage: monoagentcli connect <platform>
// Also has subcommands: list, test, remove, refresh
func newConnectCmd(cfg *globalConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "connect <platform>",
		Short:   i18n.T("connect.short"),
		Long:    i18n.T("connect.long"),
		Example: i18n.T("connect.example"),
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return runConnectPlatform(cmd, cfg, args[0])
		},
	}

	cmd.AddCommand(
		newConnectListCmd(cfg),
		newConnectTestCmd(cfg),
		newConnectRemoveCmd(cfg),
		newConnectRefreshCmd(cfg),
		newConnectSetOAuthClientCmd(cfg),
		newConnectGetOAuthClientCmd(cfg),
		newConnectSaveCmd(cfg),
		newConnectForNodeCmd(cfg),
		newConnectOAuthCmd(cfg),
	)

	return cmd
}

// newConnectSetOAuthClientCmd persistently stores an OAuth app's client_id
// (and optional client_secret) for a platform, scoped to the active profile,
// so silent token refresh works for any process (including a long-running
// `monoagentcli daemon`) without needing a MONOAGENT_<PLATFORM>_CLIENT_ID env
// var set in that process's environment. Scoped per profile because two
// connections for the same platform under different profiles may need
// different Azure/OAuth app registrations (e.g. a personal account plus a
// separate work/school account).
func newConnectSetOAuthClientCmd(cfg *globalConfig) *cobra.Command {
	var clientSecret string
	var secretStdin bool

	cmd := &cobra.Command{
		Use:   "set-oauth-client <platform> --client-id <id>",
		Short: "Persistently store an OAuth app's client ID/secret for a platform (enables silent token refresh)",
		Long: "Stores the OAuth client_id/client_secret used to obtain and silently refresh tokens for a " +
			"platform, scoped to the active/selected profile (--profile). Without this, an OAuth connection's " +
			"access token expires (typically ~1h) and can only be renewed by re-running the full interactive " +
			"`connect`/`connect refresh` flow. client_secret may be omitted for public-client apps (e.g. a " +
			"desktop/native Azure app registration using PKCE). Prefer --client-secret-stdin: a --client-secret " +
			"value is visible to other local users in the process list. The secret is stored encrypted under " +
			"the vault envelope.",
		Example: `  monoagentcli connect set-oauth-client outlook --client-id a8c1df90-1c79-4bc0-813a-f96b6b93a256
  printf '%s' "$SECRET" | monoagentcli connect set-oauth-client google_sheets --client-id 123.apps.googleusercontent.com --client-secret-stdin`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientID, _ := cmd.Flags().GetString("client-id")
			if strings.TrimSpace(clientID) == "" {
				return errInvalidInput("--client-id is required")
			}
			if secretStdin {
				if clientSecret != "" {
					return errInvalidInput("--client-secret-stdin cannot be combined with --client-secret")
				}
				v, err := readSecretValue(cmd.InOrStdin())
				if err != nil {
					return errInvalidInput("%v", err)
				}
				clientSecret = v
			}

			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.Close()

			profileID := profileOrDefault(cfg)
			if err := connections.NewStore(db.DB).SaveOAuthClient(cmd.Context(), args[0], profileID, clientID, clientSecret); err != nil {
				return scrubSecrets(fmt.Errorf("saving OAuth client credentials: %w", err), clientSecret)
			}

			if cfg.JSONOutput {
				return writeJSONTo(cmd.OutOrStdout(), map[string]any{
					"platform": args[0], "profile_id": profileID, "client_id": clientID,
					"has_client_secret": clientSecret != "", "saved": true,
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Saved OAuth client credentials for %s (profile: %s).\n", args[0], profileID)
			return nil
		},
	}

	cmd.Flags().String("client-id", "", "OAuth client ID (required)")
	cmd.Flags().StringVar(&clientSecret, "client-secret", "", "OAuth client secret (omit for public-client apps; visible in the process list, prefer --client-secret-stdin)")
	cmd.Flags().BoolVar(&secretStdin, "client-secret-stdin", false, "Read the OAuth client secret from stdin")
	_ = cmd.MarkFlagRequired("client-id")

	return cmd
}

func runConnectPlatform(cmd *cobra.Command, cfg *globalConfig, platformID string) error {
	db, err := initDB(cfg)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	mgr, err := connections.NewManager(db.DB)
	if err != nil {
		return err
	}

	conn, err := mgr.Connect(cmd.Context(), platformID, connections.ConnectOptions{
		OAuthTimeout: 5 * time.Minute,
		ProfileID:    cfg.ProfileID,
	})
	if err != nil {
		return err
	}
	warnIfAccountProfileMismatch(db.DB, cfg, conn)
	return nil
}

// warnIfAccountProfileMismatch prints a non-blocking heads-up when the
// resolved account identity (email/username, once known) doesn't obviously
// relate to the profile's name — e.g. a profile named "halansari" whose
// connection actually resolved to a @hotmail.com/@gmail.com personal
// address. A profile name is a human label, not a guarantee of which
// account got authenticated; this exists purely to make a mismatch visible,
// never to block the connection.
func warnIfAccountProfileMismatch(db *sql.DB, cfg *globalConfig, conn *connections.Connection) {
	if conn == nil || conn.AccountID == "" || !strings.Contains(conn.AccountID, "@") {
		return
	}
	var profileName string
	_ = db.QueryRow(`SELECT name FROM profiles WHERE id = ?`, cfg.ProfileID).Scan(&profileName)
	if profileName == "" || strings.EqualFold(profileName, "default") {
		return
	}
	needle := strings.ToLower(strings.ReplaceAll(profileName, " ", ""))
	if len(needle) < 3 {
		return // too short a name to meaningfully match against
	}
	if strings.Contains(strings.ToLower(conn.AccountID), needle) {
		return
	}
	fmt.Fprintf(os.Stderr,
		"\n⚠ Heads up: profile %q connected to %s, which doesn't obviously match the profile name.\n"+
			"  If this wasn't intentional, re-run with the correct account:\n"+
			"  monoagentcli --profile %s connect %s\n\n",
		profileName, conn.AccountID, profileName, conn.Platform)
}

func newConnectListCmd(cfg *globalConfig) *cobra.Command {
	var platform string
	var jsonOut bool
	var all bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List saved connections (or all supported platforms with --all)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if all {
				return printAllPlatforms(jsonOut || cfg.JSONOutput)
			}

			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			mgr, err := connections.NewManager(db.DB)
			if err != nil {
				return err
			}

			conns, err := mgr.List(cmd.Context(), platform, cfg.ProfileID)
			if err != nil {
				return err
			}

			if jsonOut || cfg.JSONOutput {
				return writeJSONTo(cmd.OutOrStdout(), connections.RedactAll(conns))
			}

			if len(conns) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No connections saved. Run `monoagentcli connect <platform>` to add one.")
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tPLATFORM\tMETHOD\tACCOUNT\tSTATUS\tLAST TESTED")
			for _, c := range conns {
				shortID := c.ID
				if len(shortID) > 8 {
					shortID = shortID[:8] + "..."
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					shortID,
					c.Platform,
					string(c.Method),
					c.AccountID,
					c.Status,
					formatLastTested(c.LastTested),
				)
			}
			return w.Flush()
		},
	}

	cmd.Flags().StringVar(&platform, "platform", "", "Filter by platform ID")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&all, "all", false, "Show all supported platforms, not just connected ones")

	return cmd
}

// getScopedConnection returns connection id when it belongs to the active
// profile, and a not-found error (exit 2) otherwise: another profile's
// connection is indistinguishable from a missing one.
func getScopedConnection(cmd *cobra.Command, cfg *globalConfig, db *sql.DB, id string) (*connections.Connection, error) {
	conn, err := connections.NewStore(db).Get(cmd.Context(), id, profileOrDefault(cfg))
	if err != nil || conn == nil {
		return nil, errNotFound("connection %q not found", id)
	}
	return conn, nil
}

func newConnectTestCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "test <id>",
		Short: "Test a saved connection by re-validating credentials",
		Long: "Re-validates a saved connection of the active profile (an expired OAuth token is silently " +
			"refreshed first). Exit code 2 when the id is unknown, 4 when validation fails.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			mgr, err := connections.NewManager(db.DB)
			if err != nil {
				return err
			}

			// Classify failures for the exit-code contract: unknown id → 2,
			// anything the validation itself reports (auth, network) → 4.
			conn, err := getScopedConnection(cmd, cfg, db.DB, args[0])
			if err != nil {
				return err
			}
			accountID, err := mgr.Test(cmd.Context(), conn.ID)
			if err != nil {
				return errAuthConnection("%s", scrubSecrets(fmt.Errorf("connection test failed: %w", err), connectionSecrets(conn)...))
			}
			if cfg.JSONOutput {
				return writeJSONTo(cmd.OutOrStdout(), map[string]string{
					"id": conn.ID, "platform": conn.Platform, "account_id": accountID, "status": "ok",
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✓ Connection valid as %s\n", accountID)
			return nil
		},
	}
}

func newConnectRemoveCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <id>",
		Short: "Remove a saved connection",
		Long:  "Removes a saved connection of the active profile and its vault entry. Exit code 2 when the id is unknown.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			mgr, err := connections.NewManager(db.DB)
			if err != nil {
				return err
			}

			if _, err := getScopedConnection(cmd, cfg, db.DB, args[0]); err != nil {
				return err
			}
			if err := mgr.Remove(cmd.Context(), args[0], profileOrDefault(cfg)); err != nil {
				return err
			}
			if cfg.JSONOutput {
				return writeJSONTo(cmd.OutOrStdout(), map[string]any{"id": args[0], "removed": true})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Connection %s removed.\n", args[0])
			return nil
		},
	}
}

func newConnectRefreshCmd(cfg *globalConfig) *cobra.Command {
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "refresh <id>",
		Short: "Refresh OAuth tokens for a saved connection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			mgr, err := connections.NewManager(db.DB)
			if err != nil {
				return err
			}

			if _, err := getScopedConnection(cmd, cfg, db.DB, args[0]); err != nil {
				return err
			}
			if err := mgr.Refresh(cmd.Context(), args[0], timeout); err != nil {
				return err
			}
			if conn, gErr := mgr.Get(cmd.Context(), args[0]); gErr == nil {
				warnIfAccountProfileMismatch(db.DB, cfg, conn)
			}
			fmt.Printf("Connection %s refreshed.\n", args[0])
			return nil
		},
	}

	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "OAuth timeout")

	return cmd
}

// formatLastTested formats a RFC3339 timestamp for display, returning "never" for empty strings.
func formatLastTested(s string) string {
	if s == "" {
		return "never"
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.Format("2006-01-02 15:04")
}

// printAllPlatforms prints all supported platforms sorted by category then name.
func printAllPlatforms(jsonOut bool) error {
	platforms := connections.All()

	sort.Slice(platforms, func(i, j int) bool {
		if platforms[i].Category != platforms[j].Category {
			return platforms[i].Category < platforms[j].Category
		}
		return platforms[i].Name < platforms[j].Name
	})

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(platforms)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tCATEGORY\tMETHODS\tCONNECT VIA")
	for _, p := range platforms {
		methods := make([]string, len(p.Methods))
		for i, m := range p.Methods {
			methods[i] = string(m)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			p.ID,
			p.Name,
			p.Category,
			strings.Join(methods, ", "),
			p.ConnectVia,
		)
	}
	return w.Flush()
}
