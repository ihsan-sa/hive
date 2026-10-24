package release

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// PublicKey is the public half of the key every release is signed with,
// base64 of its 32 bytes. The owner makes the key and pins it here, or at
// build with -ldflags "-X .../release.PublicKey=...", never in a config, env
// or file the Hive could edit; until then it is "" and an update checks the
// checksums only, and tells the person (Unsigned). Once
// it is set, a release whose checksums.txt.sig is missing or doesn't verify
// against it is never installed.
var PublicKey = ""

// SigName is the asset holding the signature over a release's checksums.
const SigName = "checksums.txt.sig"

// signed is what a release's signature covers: its tag and its checksums, so
// an older release's signed checksums can't be served under a newer tag.
func signed(tag string, sums []byte) []byte {
	return append([]byte("hive release "+tag+"\n"), sums...)
}

// Sign is the signature, base64, over tag's checksums with the private key
// whose 32-byte seed is seed.
func Sign(seed []byte, tag string, sums []byte) (string, error) {
	if len(seed) != ed25519.SeedSize {
		return "", fmt.Errorf("a release key is a %d-byte seed, not %d bytes", ed25519.SeedSize, len(seed))
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.NewKeyFromSeed(seed), signed(tag, sums))), nil
}

// PublicOf is the base64 public key of seed, the form PublicKey pins.
func PublicOf(seed []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey))
}

// Unsigned is what the person is told when an update installed with no key
// pinned: the checksums matched, but nothing showed who made the release.
const Unsigned = "This release was unsigned: this build has no release key pinned, so the update checked only that the download matched its checksums, not who made the release."

// errUnsigned is a release with no signature where one is required.
var errUnsigned = errors.New("isn't signed")

// verify errs unless sig is tag's signature over sums by PublicKey. With no
// key pinned there is nothing to check against, and it passes; Update then
// tells the person the release was unsigned.
func verify(tag string, sums []byte, sig func() ([]byte, error)) error {
	if PublicKey == "" {
		return nil
	}
	pub, err := base64.StdEncoding.DecodeString(PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("this build's release key is malformed")
	}
	raw, err := sig()
	if err != nil {
		return errUnsigned
	}
	got, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(raw)))
	if err != nil || !ed25519.Verify(pub, signed(tag, sums), got) {
		return errors.New("isn't signed by this build's release key")
	}
	return nil
}

// sums fetches a release's checksums, checks their signature and reads them.
func sums(repo, tag string) (map[string]string, error) {
	raw, err := fetch(repo, tag, "checksums.txt")
	if err != nil {
		return nil, fmt.Errorf("no release %s in %s (%v)", tag, repo, err)
	}
	if err := verify(tag, raw, func() ([]byte, error) { return fetch(repo, tag, SigName) }); err != nil {
		return nil, fmt.Errorf("release %s in %s %w; not installing", tag, repo, err)
	}
	return parseSums(raw)
}

func fetch(repo, tag, name string) ([]byte, error) {
	resp, err := get(repo + "/releases/download/" + tag + "/" + name)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
