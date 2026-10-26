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

// A switch given true or false, as --apart true or --apart=false, reads as a
// yes or no, while another key's "true" stays text.
func TestSwitchValues(t *testing.T) {
	got, err := parseArgs([]string{"--apart", "true", "--self=false", "--text", "true"})
	if err != nil {
		t.Fatal(err)
	}
	if got["apart"] != true || got["self"] != false || got["text"] != "true" {
		t.Fatalf("parsed %v", got)
	}
}
