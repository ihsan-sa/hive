package release

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// testSeed is a test key, made here for the tests; the real release key is
// the owner's, and PublicKey ships empty until they pin it.
var testSeed = bytes.Repeat([]byte{7}, 32)

// pinned has this build check releases against seed's key.
func pinned(t *testing.T, seed []byte) {
	t.Helper()
	k, s, f := PublicKey, signWith, signFor
	t.Cleanup(func() { PublicKey, signWith, signFor = k, s, f })
	PublicKey = PublicOf(seed)
}

// A release signed with the pinned key installs.
func TestSignedReleaseInstalls(t *testing.T) {
	serve(t, "v9.9.9", build("new"), false)
	pinned(t, testSeed)
	signWith = testSeed
	m, exe := releaseAt(t, "v0.1.2", "me/hive")
	msg, err := m.Update("")
	if raw, _ := os.ReadFile(exe); err != nil || !strings.HasPrefix(msg, "updated to v9.9.9") || string(raw) != "new" {
		t.Fatalf("signed release: %q, %v, binary %q", msg, err, raw)
	}
}

// With a key pinned, a release with no signature is never installed, and
// Check doesn't offer it.
func TestUnsignedReleaseRefused(t *testing.T) {
	serve(t, "v9.9.9", build("new"), false)
	pinned(t, testSeed)
	m, exe := releaseAt(t, "v0.1.2", "me/hive")
	for _, tag := range []string{"", "v9.9.9"} {
		_, err := m.Update(tag)
		if raw, _ := os.ReadFile(exe); err == nil || !strings.Contains(err.Error(), "isn't signed; not installing") || string(raw) != "old" {
			t.Fatalf("unsigned release %q: %v, binary %q", tag, err, raw)
		}
	}
	if _, _, newer, err := m.Check(); newer || err == nil {
		t.Fatalf("Check offered an unsigned release: newer %v, %v", newer, err)
	}
}

// A release signed with another key, or whose signature is another tag's,
// is never installed.
func TestWronglySignedReleaseRefused(t *testing.T) {
	serve(t, "v9.9.9", build("new"), false)
	for _, c := range []struct {
		name   string
		seed   []byte
		prefix string
	}{
		{"another key", bytes.Repeat([]byte{8}, 32), ""},
		{"another tag's signature", testSeed, "v0.0.1 or "},
	} {
		pinned(t, testSeed)
		signWith, signFor = c.seed, c.prefix
		m, exe := releaseAt(t, "v0.1.2", "me/hive")
		_, err := m.Update("v9.9.9")
		if raw, _ := os.ReadFile(exe); err == nil || !strings.Contains(err.Error(), "isn't signed by this build's release key") || string(raw) != "old" {
			t.Fatalf("%s: %v, binary %q", c.name, err, raw)
		}
	}
}

// The signature covers the tag, so one tag's can't stand for another's.
func TestSignatureCoversTheTag(t *testing.T) {
	pinned(t, testSeed)
	sums := []byte("abc  hive-linux-amd64\n")
	sig, err := Sign(testSeed, "v1.0.0", sums)
	if err != nil {
		t.Fatal(err)
	}
	get := func() ([]byte, error) { return []byte(sig), nil }
	if err := verify("v1.0.0", sums, get); err != nil {
		t.Fatalf("good signature: %v", err)
	}
	if err := verify("v1.0.1", sums, get); err == nil {
		t.Fatal("v1.0.0's signature passed for v1.0.1")
	}
	if err := verify("v1.0.0", append(sums, 'x'), get); err == nil {
		t.Fatal("a signature passed over other checksums")
	}
	PublicKey = ""
	if err := verify("v1.0.1", sums, get); err != nil {
		t.Fatalf("with no key pinned: %v", err)
	}
	if _, err := Sign([]byte("short"), "v1.0.0", sums); err == nil {
		t.Fatal("a short seed signed")
	}
}

// With no key pinned an update still installs, and says plainly, in what it
// answers and in its journaled notice, that the release was unsigned; a
// release checked against a pinned key says no such thing.
func TestUnpinnedUpdateSaysUnsigned(t *testing.T) {
	for _, key := range []bool{false, true} {
		serve(t, "v9.9.9", build("new"), false)
		if key {
			pinned(t, testSeed)
			signWith = testSeed
		}
		m, exe := releaseAt(t, "v0.1.2", "me/hive")
		msg, err := m.Update("")
		if raw, _ := os.ReadFile(exe); err != nil || string(raw) != "new" {
			t.Fatalf("key %v: %q, %v, binary %q", key, msg, err, raw)
		}
		var got struct{ Unsigned bool }
		m.C.J.Replay(func(e core.Event) {
			if e.Kind == "hive.updated" {
				e.Decode(&got)
			}
		})
		if said := strings.Contains(msg, Unsigned); said == key || got.Unsigned == key {
			t.Fatalf("key %v: told unsigned %v, journaled unsigned %v: %q", key, said, got.Unsigned, msg)
		}
	}
}
