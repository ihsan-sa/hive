package improve

import (
	"maps"
	"testing"
)

// A merge records what the project's size command printed beside its score,
// and a size command that fails takes nothing from the measure.
func TestSizeRecordedWithTheScore(t *testing.T) {
	_, merge := scored(t)
	ms := merge(map[string]string{".hive/project.json": `{"test": "true", "score": {"command": "sh ./score.sh", "direction": "higher"}, "size": "echo core_lines 120; echo binary_bytes 4096; echo built in 3 s"}`})
	if want := map[string]float64{"core_lines": 120, "binary_bytes": 4096}; !maps.Equal(ms.Size, want) || !ms.Kept {
		t.Fatalf("size %v kept %v, want %v and kept", ms.Size, ms.Kept, want)
	}
	ms = merge(map[string]string{".hive/project.json": `{"test": "true", "score": {"command": "sh ./score.sh", "direction": "higher"}, "size": "echo core_lines 1; exit 1"}`})
	if ms.Size != nil || !ms.Kept {
		t.Fatalf("a failed size command: size %v kept %v, want none and kept", ms.Size, ms.Kept)
	}
}

func TestSizes(t *testing.T) {
	got := sizes("core_lines 13874\r\ngo_lines 31000\nbinary_bytes NaN\nok\nbinary_bytes 2.5e7\n")
	if want := map[string]float64{"core_lines": 13874, "go_lines": 31000, "binary_bytes": 2.5e7}; !maps.Equal(got, want) {
		t.Fatalf("sizes = %v, want %v", got, want)
	}
	if sizes("nothing here") != nil {
		t.Fatal("no size lines should be no sizes")
	}
}
