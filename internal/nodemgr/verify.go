package nodemgr

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"sync"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
)

// releaseKeys are the Node.js releasers' public keys, pinned from
// github.com/nodejs/release-keys (see the header of the file for the
// commit). A release's SHASUMS256.txt.asc must be signed by one of them, so
// the checksums, and through them the archive, are authentic and not only
// uncorrupted: a mirror or a hijacked nodejs.org cannot hand out a Node of
// its own.
//
//go:embed keys/release-keys.asc
var releaseKeys []byte

var (
	keyringOnce sync.Once
	keyring     openpgp.EntityList
	keyringErr  error
)

// ReleaseKeyring parses the embedded release keys once.
func ReleaseKeyring() (openpgp.EntityList, error) {
	keyringOnce.Do(func() { keyring, keyringErr = parseKeyBlocks(releaseKeys) })
	return keyring, keyringErr
}

// parseKeyBlocks reads every armored public key block in data (the file is
// the concatenation of one block per key, which a single armor read would
// stop after the first of).
func parseKeyBlocks(data []byte) (openpgp.EntityList, error) {
	const begin = "-----BEGIN PGP PUBLIC KEY BLOCK-----"
	var out openpgp.EntityList
	for {
		i := bytes.Index(data, []byte(begin))
		if i < 0 {
			break
		}
		data = data[i:]
		next := bytes.Index(data[len(begin):], []byte(begin))
		block := data
		if next >= 0 {
			block, data = data[:len(begin)+next], data[len(begin)+next:]
		} else {
			data = nil
		}
		list, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(block))
		if err != nil {
			return nil, fmt.Errorf("reading a pinned Node release key: %w", err)
		}
		out = append(out, list...)
	}
	if len(out) == 0 {
		return nil, errors.New("no pinned Node release keys")
	}
	return out, nil
}

// verifySums checks a clearsigned SHASUMS256.txt.asc against keys and
// returns the signed text: only what the signature covers is trusted, not
// anything around it.
func verifySums(asc []byte, keys openpgp.EntityList) ([]byte, error) {
	block, _ := clearsign.Decode(asc)
	if block == nil {
		return nil, errors.New("SHASUMS256.txt.asc is not a clearsigned message")
	}
	if _, err := block.VerifySignature(keys, nil); err != nil {
		return nil, fmt.Errorf("SHASUMS256.txt.asc is not signed by a Node.js release key: %w", err)
	}
	return block.Bytes, nil
}
