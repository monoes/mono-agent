package account

import (
	"crypto/ed25519"
	"testing"
)

// A caller changes what TrustedKeys returns, and must not change what the process
// trusts: every key in it is a copy, the pinned ones and the ones a build adds
// (the development key of a devaccount build) alike. A default build adds none,
// so the combination is tested on its own.
func TestTheMergedKeysAreCopiesOfBothSets(t *testing.T) {
	pinned := []Key{{KID: "pinned", Public: ed25519.PublicKey{1, 2, 3}}}
	extra := []Key{{KID: "dev", Public: ed25519.PublicKey{4, 5, 6}}}
	got := mergeKeys(pinned, extra)
	if len(got) != 2 || got[0].KID != "pinned" || got[1].KID != "dev" {
		t.Fatalf("mergeKeys = %v, want the pinned key and then the extra one", got)
	}
	for i := range got {
		got[i].KID = "changed"
		got[i].Public[0] = 9
	}
	if pinned[0].KID != "pinned" || pinned[0].Public[0] != 1 {
		t.Fatalf("a change to the result reached the pinned set: %+v", pinned[0])
	}
	if extra[0].KID != "dev" || extra[0].Public[0] != 4 {
		t.Fatalf("a change to the result reached the extra set (the development key): %+v", extra[0])
	}
}

func TestMergingWhenOneSetOrBothAreEmpty(t *testing.T) {
	key := []Key{{KID: "k", Public: ed25519.PublicKey{1}}}
	for _, c := range []struct {
		name          string
		pinned, extra []Key
		want          int
	}{
		{"nothing", nil, nil, 0},
		{"only pinned", key, nil, 1},
		{"only extra", nil, key, 1},
	} {
		got := mergeKeys(c.pinned, c.extra)
		if len(got) != c.want {
			t.Errorf("%s: %d keys, want %d", c.name, len(got), c.want)
		}
		for i := range got {
			got[i].Public[0] = 9
		}
		if key[0].Public[0] != 1 {
			t.Fatalf("%s: a change to the result reached the input", c.name)
		}
	}
}
