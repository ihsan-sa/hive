package agentcli

import "testing"

// A switch may stand bare before the next --key, but a key that takes a value
// keeps it even when the value itself starts with --.
func TestSwitchesAndValues(t *testing.T) {
	got, err := parseArgs([]string{"--self", "--text", "--help", "--name", "hive", "--isolated"})
	if err != nil {
		t.Fatal(err)
	}
	if got["self"] != true || got["text"] != "--help" || got["name"] != "hive" || got["isolated"] != true {
		t.Fatalf("parsed %v", got)
	}
}
