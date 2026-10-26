package core

import (
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// A shipped module has no module-reviewer read, so the program its manifest
// runs as the person is covered by the security list instead: every module.json
// and every file an exec names under its module's folder (a file, or each file
// of a folder) is on it. A new shipped program can't skip that read unnoticed.
func TestShippedModuleProgramsNeedASecurityRead(t *testing.T) {
	root := filepath.Join("..", "..")
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(SecurityList)))
	if err != nil {
		t.Fatal(err)
	}
	list := ParseList(string(raw), "paths")
	manifests, _ := filepath.Glob(filepath.Join(root, "modules", "*", "module.json"))
	if len(manifests) == 0 {
		t.Fatal("no shipped modules found")
	}
	for _, mf := range manifests {
		var m struct{ Exec []string }
		raw, err := os.ReadFile(mf)
		if err != nil || json.Unmarshal(raw, &m) != nil {
			t.Fatalf("%s: %v", mf, err)
		}
		dir := "modules/" + filepath.Base(filepath.Dir(mf))
		need := []string{dir + "/module.json"}
		for _, arg := range m.Exec {
			p := path.Clean(path.Join(dir, filepath.ToSlash(arg)))
			if !strings.HasPrefix(p, dir+"/") {
				continue
			}
			filepath.WalkDir(filepath.Join(root, filepath.FromSlash(p)), func(f string, d fs.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					rel, _ := filepath.Rel(root, f)
					need = append(need, filepath.ToSlash(rel))
				}
				return nil
			})
		}
		if len(m.Exec) > 0 && len(need) == 1 {
			t.Errorf("%s runs %v, but no file it names is under %s", mf, m.Exec, dir)
		}
		for _, f := range need {
			if !matchAny(f, list) {
				t.Errorf("%s runs %s, which is not on %s", dir, f, SecurityList)
			}
		}
	}
}
