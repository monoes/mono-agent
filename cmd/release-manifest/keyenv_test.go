package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newKeyEnv(t *testing.T, raw []byte) {
	t.Helper()
	t.Setenv("TEST_RELEASE_KEY", base64.StdEncoding.EncodeToString(raw))
}

func manifestFixture(t *testing.T, version string) string {
	t.Helper()
	dir := fixture(t)
	if err := run([]string{"manifest", "-dir", dir, "-version", version}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSignKeyEnvRoundTrip(t *testing.T) {
	for name, mk := range map[string]func(ed25519.PrivateKey) []byte{
		"full key": func(p ed25519.PrivateKey) []byte { return p },
		"seed":     func(p ed25519.PrivateKey) []byte { return p.Seed() },
	} {
		t.Run(name, func(t *testing.T) {
			pub, priv, _ := ed25519.GenerateKey(rand.Reader)
			newKeyEnv(t, mk(priv))
			dir := manifestFixture(t, "v1.2.3")
			mpath := filepath.Join(dir, "manifest.json")
			pubOut := filepath.Join(t.TempDir(), "pub.txt")
			err := run([]string{"sign", "-manifest", mpath, "-key-env", "TEST_RELEASE_KEY",
				"-assets-dir", dir, "-expect-version", "v1.2.3", "-min-version", "v1.2.2", "-pubkey-out", pubOut})
			if err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(mpath)
			sig, _ := os.ReadFile(mpath + ".sig")
			if err := verifySignature(data, string(sig), []ed25519.PublicKey{pub}); err != nil {
				t.Fatal(err)
			}
			line, _ := os.ReadFile(pubOut)
			if want := keyID(pub) + " " + base64.StdEncoding.EncodeToString(pub) + "\n"; string(line) != want {
				t.Errorf("pubkey-out %q, want %q", line, want)
			}
			if strings.Contains(string(line), base64.StdEncoding.EncodeToString(priv)) {
				t.Error("private key leaked into pubkey-out")
			}
		})
	}
}

func TestSignKeyEnvRefusals(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	dir := manifestFixture(t, "v1.2.3")
	mpath := filepath.Join(dir, "manifest.json")
	base := []string{"sign", "-manifest", mpath, "-key-env", "TEST_RELEASE_KEY"}
	with := func(extra ...string) []string { return append(append([]string{}, base...), extra...) }

	t.Setenv("TEST_RELEASE_KEY", "")
	if err := run(with("-assets-dir", dir, "-expect-version", "v1.2.3")); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("empty variable accepted: %v", err)
	}
	newKeyEnv(t, priv)
	if err := run(base); err == nil {
		t.Error("-key-env without -assets-dir/-expect-version accepted")
	}
	if err := run(with("-assets-dir", dir, "-expect-version", "v1.2.3", "-keyring")); err == nil {
		t.Error("two key sources accepted")
	}
	// An error must never echo the key.
	t.Setenv("TEST_RELEASE_KEY", "not-base64-!!!secretsecret")
	if err := run(with("-assets-dir", dir, "-expect-version", "v1.2.3")); err == nil || strings.Contains(err.Error(), "secretsecret") {
		t.Errorf("bad key error missing or leaks: %v", err)
	}
	if _, err := os.Stat(mpath + ".sig"); err == nil {
		t.Error("a signature was written despite refusals")
	}
}

func TestSignRefusesBadManifest(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	sign := func(dir, ver, min string) error {
		newKeyEnv(t, priv) // sign unsets the variable once it has read it
		args := []string{"sign", "-manifest", filepath.Join(dir, "manifest.json"), "-key-env", "TEST_RELEASE_KEY",
			"-assets-dir", dir, "-expect-version", ver}
		if min != "" {
			args = append(args, "-min-version", min)
		}
		return run(args)
	}
	t.Run("checksum mismatch", func(t *testing.T) {
		dir := manifestFixture(t, "v1.2.3")
		os.WriteFile(filepath.Join(dir, "monoagentcli-linux-amd64"), []byte("tampered after manifest"), 0o644)
		if err := sign(dir, "v1.2.3", ""); err == nil || !strings.Contains(err.Error(), "sha256") {
			t.Errorf("tampered asset signed: %v", err)
		}
	})
	t.Run("size mismatch", func(t *testing.T) {
		dir := manifestFixture(t, "v1.2.3")
		mpath := filepath.Join(dir, "manifest.json")
		raw, _ := os.ReadFile(mpath)
		var m Manifest
		json.Unmarshal(raw, &m)
		m.Assets[0].Size++
		out, _ := json.Marshal(m)
		os.WriteFile(mpath, out, 0o644)
		if err := sign(dir, "v1.2.3", ""); err == nil || !strings.Contains(err.Error(), "size") {
			t.Errorf("wrong size signed: %v", err)
		}
	})
	t.Run("missing asset file", func(t *testing.T) {
		dir := manifestFixture(t, "v1.2.3")
		os.Remove(filepath.Join(dir, "MonoAgent-linux-amd64.tar.gz"))
		if err := sign(dir, "v1.2.3", ""); err == nil {
			t.Error("manifest with a missing asset signed")
		}
	})
	t.Run("built file not in manifest", func(t *testing.T) {
		dir := manifestFixture(t, "v1.2.3")
		os.WriteFile(filepath.Join(dir, "MonoAgent-darwin-amd64.zip"), []byte("x"), 0o644)
		if err := sign(dir, "v1.2.3", ""); err == nil || !strings.Contains(err.Error(), "missing from the manifest") {
			t.Errorf("unlisted platform file signed: %v", err)
		}
	})
	t.Run("wrong version", func(t *testing.T) {
		dir := manifestFixture(t, "v1.2.3")
		if err := sign(dir, "v1.2.4", ""); err == nil || !strings.Contains(err.Error(), "being released") {
			t.Errorf("version not equal to the release signed: %v", err)
		}
	})
	t.Run("lower than latest published", func(t *testing.T) {
		dir := manifestFixture(t, "v1.2.3")
		for _, min := range []string{"v1.2.4", "v1.3.0", "v2.0.0", "v1.10.0"} {
			if err := sign(dir, "v1.2.3", min); err == nil || !strings.Contains(err.Error(), "lower") {
				t.Errorf("v1.2.3 signed although %s is published: %v", min, err)
			}
		}
		for _, min := range []string{"v1.2.3", "v1.2.2", "v0.9.9", "v1.1.99"} {
			if err := sign(dir, "v1.2.3", min); err != nil {
				t.Errorf("v1.2.3 refused with latest %s: %v", min, err)
			}
		}
	})
	t.Run("foreign url", func(t *testing.T) {
		dir := manifestFixture(t, "v1.2.3")
		mpath := filepath.Join(dir, "manifest.json")
		raw, _ := os.ReadFile(mpath)
		os.WriteFile(mpath, []byte(strings.Replace(string(raw), "/releases/download/v1.2.3/monoagentcli-linux-amd64", "/evil/monoagentcli-linux-amd64", 1)), 0o644)
		if err := sign(dir, "v1.2.3", ""); err == nil {
			t.Error("manifest with an off-release url signed")
		}
	})
}
