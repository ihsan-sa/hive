package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeProc writes a /proc with this much available memory and pressure.
func fakeProc(t *testing.T, memKB, avg10 string) string {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "pressure"), 0o755)
	os.WriteFile(filepath.Join(dir, "meminfo"), []byte("MemTotal:       16000000 kB\nMemFree:          100000 kB\nMemAvailable:   "+memKB+" kB\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "pressure", "memory"), []byte("some avg10="+avg10+" avg60=0.00 avg300=0.00 total=0\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n"), 0o644)
	return dir
}

func TestStrained(t *testing.T) {
	for _, c := range []struct {
		name, memKB, avg10 string
		floor              int
		max                float64
		want               string
	}{
		{"healthy", "8000000", "1.50", 1024, 40, ""},
		{"low memory", "500000", "1.50", 1024, 40, "low on memory (488 MB free"},
		{"pressure", "8000000", "55.20", 1024, 40, "memory pressure (55.20%"},
		{"checks off", "500000", "55.20", 0, 0, ""},
	} {
		got := strained(fakeProc(t, c.memKB, c.avg10), c.floor, c.max)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if got := strained(t.TempDir(), 1024, 40); got != "" {
		t.Errorf("no /proc files: %q, want no hold", got)
	}
}
