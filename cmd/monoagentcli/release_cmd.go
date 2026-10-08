package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"

	"github.com/monoes/mono-agent/internal/release"
	"github.com/monoes/mono-agent/internal/secrets"
	"github.com/spf13/cobra"
)

// Maintainer tools for the signed release manifest. They are open commands
// (no account needed, like update): they only touch a local keychain entry
// and files the maintainer names.

func newReleaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Maintainer tools for signed releases (keygen, verify)",
	}
	cmd.AddCommand(newReleaseKeygenCmd(), newReleaseVerifyCmd())
	return cmd
}

func newReleaseKeygenCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "keygen",
		Short: "Generate the release signing key; the private key goes to the OS keychain",
		Long: "Generates an Ed25519 release signing key. The private key is stored in the OS keychain (service monoagent-release-signing, " +
			"account ed25519-v1) and never printed. The public key line is printed: paste it into pinnedReleaseKeys in " +
			"internal/release/keys.go and rebuild; clients trust only the keys pinned there. Run it in your own terminal.\n\n" +
			"An existing key is never replaced unless --force (releases signed with the old key stop verifying in builds that pin only the new one).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := secrets.ReleaseSigningKey(); err == nil && !force {
				return errInvalidInput("a release signing key already exists in the keychain; --force replaces it")
			} else if err != nil && !errors.Is(err, secrets.ErrNoReleaseKey) {
				return fmt.Errorf("read the keychain: %w", err)
			}
			pub, priv, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				return err
			}
			if err := secrets.StoreReleaseSigningKey(base64.StdEncoding.EncodeToString(priv)); err != nil {
				return fmt.Errorf("store the private key in the OS keychain: %w", err)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "Private key stored in the OS keychain (monoagent-release-signing / ed25519-v1).")
			fmt.Fprintln(out, "Paste this line into pinnedReleaseKeys in internal/release/keys.go:")
			fmt.Fprintf(out, "\t%q,\n", release.KeyLine(pub))
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Replace an existing key")
	return cmd
}

func newReleaseVerifyCmd() *cobra.Command {
	var pubkey string
	cmd := &cobra.Command{
		Use:   "verify <manifest> <sig>",
		Short: "Verify a manifest.json against its manifest.json.sig",
		Long: "Checks the signature file (\"<key-id> <base64 signature>\") over the exact bytes of the manifest against the keys pinned " +
			"in this binary, or against --pubkey \"<key-id> <base64 public key>\" (for trying a key before it is pinned). Exit 3 when it does not verify.",
		Example: "  monoagentcli release verify manifest.json manifest.json.sig",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			manifest, err := os.ReadFile(args[0])
			if err != nil {
				return errNotFound("%v", err)
			}
			sig, err := os.ReadFile(args[1])
			if err != nil {
				return errNotFound("%v", err)
			}
			keys := release.PinnedKeys()
			if pubkey != "" {
				k, err := release.ParseKey(pubkey)
				if err != nil {
					return errInvalidInput("%v", err)
				}
				keys = []release.Key{k}
			}
			if len(keys) == 0 {
				return errInvalidInput("this build pins no release key; pass --pubkey")
			}
			m, err := release.VerifyWith(keys, manifest, sig)
			if err != nil {
				return errInvalidInput("%v", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "OK: %s, %d assets, signature valid\n", m.Version, len(m.Assets))
			return nil
		},
	}
	cmd.Flags().StringVar(&pubkey, "pubkey", "", "Verify against this \"<key-id> <base64 public key>\" instead of the pinned keys")
	return cmd
}
