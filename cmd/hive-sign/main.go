// Command hive-sign signs a release's checksums in the release workflow:
//
//	HIVE_RELEASE_KEY=<base64 seed> go run ./cmd/hive-sign <tag> <checksums.txt>
//
// writes <checksums.txt>.sig, which every install whose build pins the key's
// public half (release.PublicKey) checks before it installs the release. With
// no key in the environment it signs nothing, and fails when this source pins
// a key, since every install would then refuse the release. A key whose
// public half isn't the pinned one fails too.
//
// The key is the owner's. `go run ./cmd/hive-sign -new` prints a fresh one:
// the seed goes in the HIVE_RELEASE_KEY secret of the repository's protected
// release-signing environment (not a repository secret, which every job
// sees), and the public half in release.PublicKey.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/mod/release"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "hive-sign:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 1 && args[0] == "-new" {
		seed := make([]byte, ed25519.SeedSize)
		if _, err := rand.Read(seed); err != nil {
			return err
		}
		fmt.Printf("HIVE_RELEASE_KEY (the secret): %s\nrelease.PublicKey (pinned in the source): %s\n", base64.StdEncoding.EncodeToString(seed), release.PublicOf(seed))
		return nil
	}
	if len(args) != 2 {
		return fmt.Errorf("usage: hive-sign <tag> <checksums.txt>, or hive-sign -new")
	}
	tag, path := args[0], args[1]
	env := strings.TrimSpace(os.Getenv("HIVE_RELEASE_KEY"))
	if env == "" {
		if release.PublicKey != "" {
			return fmt.Errorf("no HIVE_RELEASE_KEY, and this source pins a release key, so every install would refuse %s unsigned", tag)
		}
		fmt.Println("::warning::no HIVE_RELEASE_KEY and no release key pinned: " + tag + " goes out unsigned")
		return nil
	}
	seed, err := base64.StdEncoding.DecodeString(env)
	if err != nil {
		return fmt.Errorf("HIVE_RELEASE_KEY isn't base64: %v", err)
	}
	if release.PublicKey != "" && release.PublicOf(seed) != release.PublicKey {
		return fmt.Errorf("HIVE_RELEASE_KEY isn't the key this source pins")
	}
	sums, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sig, err := release.Sign(seed, tag, sums)
	if err != nil {
		return err
	}
	if release.PublicKey == "" {
		fmt.Println("::warning::" + tag + " is signed, but no release key is pinned, so installs don't check it yet")
	}
	return os.WriteFile(path+".sig", []byte(sig+"\n"), 0o644)
}
