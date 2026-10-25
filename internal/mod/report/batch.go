package report

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// DefaultBundleURL takes `hive share`'s bundles and the everything level's
// batches, unless share_bundle_url names another.
const DefaultBundleURL = "https://hive-api.ihsan.cc/v1/bundle"

// batchCap bounds one batch, the zip as it goes; the question says it in MB.
const batchCap = 8 << 20

// everythingSet is what the everything level sends beside the reports, as
// globs under .hive, with the words the question names each by. A tail file
// is only appended to, so each batch sends what was added since the last;
// another is sent whole each time it changes. Nothing else under .hive goes:
// not the logins, the private folder, the config, the binaries or a run's
// home and checkout.
var everythingSet = []struct {
	glob, says string
	tail       bool
}{
	{"runs/*/ro/brief.md", "the brief", false},
	{"runs/*/box/notes.md", "notes", false},
	{"runs/*/host/turn-*.jsonl", "turn logs of each agent run", true},
	{"chats/*.jsonl", "the chats", true},
	{"events.jsonl", "the Hive journal", true},
	{"daemon.log", "the daemon log", true},
}

// BundleURL is where batches go.
func BundleURL(cfg core.Config) string {
	if cfg.ShareBundleURL != "" {
		return cfg.ShareBundleURL
	}
	return DefaultBundleURL
}

// batchFile is how far a file has been sent: its size and time when last
// seen, and the bytes of it sent (a tail file's offset).
type batchFile struct {
	Size int64     `json:"size"`
	Mod  time.Time `json:"mod"`
	Sent int64     `json:"sent"`
}

// batches is where the batches have got to, for the everything level chosen
// at Since.
type batches struct {
	Since time.Time            `json:"since"`
	Files map[string]batchFile `json:"files"`
}

// Part is one file's piece in a batch, as its manifest lists it.
type Part struct {
	Name  string `json:"name"` // slash path under .hive
	From  int64  `json:"from"` // where in the file it starts
	Bytes int    `json:"bytes"`
}

// Batch is a batch's manifest, batch.json in its zip.
type Batch struct {
	Install string `json:"install"`
	Version string `json:"version"`
	Type    string `json:"type"` // batch
	OS      string `json:"os"`
	At      string `json:"at"`
	Parts   []Part `json:"parts"`
}

func batchState(ws core.Workspace) string { return filepath.Join(Dir(ws), "batches.json") }

func loadBatches(ws core.Workspace) batches {
	var b batches
	if raw, err := os.ReadFile(batchState(ws)); err == nil {
		json.Unmarshal(raw, &b)
	}
	if b.Files == nil {
		b.Files = map[string]batchFile{}
	}
	return b
}

// candidates are the files of everythingSet as they are now.
func candidates(ws core.Workspace) map[string]os.FileInfo {
	out := map[string]os.FileInfo{}
	for _, s := range everythingSet {
		files, _ := filepath.Glob(filepath.Join(ws.Hive(), filepath.FromSlash(s.glob)))
		for _, f := range files {
			if fi, err := os.Lstat(f); err == nil && fi.Mode().IsRegular() {
				rel, _ := filepath.Rel(ws.Hive(), f)
				out[filepath.ToSlash(rel)] = fi
			}
		}
	}
	return out
}

// baseline marks every file as sent as it stands, so the batches for the
// everything level chosen at since hold only what is written from then.
func baseline(ws core.Workspace, since time.Time) {
	b := batches{Since: since, Files: map[string]batchFile{}}
	for rel, fi := range candidates(ws) {
		b.Files[rel] = batchFile{Size: fi.Size(), Mod: fi.ModTime(), Sent: fi.Size()}
	}
	raw, _ := json.Marshal(b)
	core.WriteFileAtomic(batchState(ws), raw, 0o600)
}

func tailFile(rel string) bool {
	for _, s := range everythingSet {
		if ok, _ := filepath.Match(s.glob, rel); ok {
			return s.tail
		}
	}
	return false
}

// buildBatch zips what changed since the last batch, scrubbed, oldest
// first, up to batchCap, and returns the zip, its manifest and where the
// batches will have got to once it is sent.
func (m *Module) buildBatch(cfg core.Config) ([]byte, Batch, batches, error) {
	ws := m.C.WS
	since, _ := everythingSince(cfg)
	st := loadBatches(ws)
	if !st.Since.Equal(since) {
		// The level was chosen some way Changed didn't see (the config
		// edited by hand): nothing written before now is known to be after
		// the choice, so the batches start from now.
		baseline(ws, since)
		st = loadBatches(ws)
	}
	type due struct {
		rel string
		fi  os.FileInfo
	}
	var list []due
	for rel, fi := range candidates(ws) {
		old, seen := st.Files[rel]
		if seen && old.Size == fi.Size() && old.Mod.Equal(fi.ModTime()) || !seen && fi.ModTime().Before(since) {
			continue
		}
		list = append(list, due{rel, fi})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].fi.ModTime().Before(list[j].fi.ModTime()) })
	man := Batch{Install: cfg.InstallID, Version: Version(), Type: "batch", OS: runtime.GOOS + "/" + runtime.GOARCH, At: time.Now().UTC().Format(time.RFC3339)}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	room := batchCap - 64<<10 // the manifest and the zip's own headers
	for _, d := range list {
		if room < 4<<10 {
			break
		}
		old := st.Files[d.rel]
		from, tail := int64(0), tailFile(d.rel)
		if tail && old.Sent <= d.fi.Size() {
			from = old.Sent // a tail file shorter than was sent was replaced, and is sent again
		}
		raw, err := core.SafeRead(filepath.Join(ws.Hive(), filepath.FromSlash(d.rel)), from+int64(room))
		if err != nil || int64(len(raw)) < from {
			continue
		}
		piece := raw[from:]
		if tail {
			piece = piece[:bytes.LastIndexByte(piece, '\n')+1] // a line still being written waits
		}
		if bytes.IndexByte(piece, 0) >= 0 {
			st.Files[d.rel] = batchFile{Size: d.fi.Size(), Mod: d.fi.ModTime(), Sent: d.fi.Size()} // not text: never sent
			continue
		}
		text := m.private(string(piece))
		for len(text) > room && len(piece) > 0 {
			// Scrubbing can lengthen a piece a little; a tail piece is cut
			// at a line, another clipped.
			cut := len(piece) * room / len(text) * 9 / 10
			if tail {
				cut = bytes.LastIndexByte(piece[:cut], '\n') + 1
			}
			piece = piece[:cut]
			text = m.private(string(piece))
		}
		if len(piece) == 0 {
			continue
		}
		name := ".hive/" + d.rel
		if from > 0 {
			name += fmt.Sprintf("@%d", from)
		}
		f, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: d.fi.ModTime()})
		if err != nil {
			return nil, man, st, err
		}
		f.Write([]byte(text))
		room -= len(text)
		man.Parts = append(man.Parts, Part{Name: d.rel, From: from, Bytes: len(text)})
		sent := from + int64(len(piece))
		if !tail {
			sent = d.fi.Size() // sent whole, or as much as fit
		}
		st.Files[d.rel] = batchFile{Size: d.fi.Size(), Mod: d.fi.ModTime(), Sent: sent}
		if tail && sent < d.fi.Size() {
			// More of it waits: seen as changed next time.
			st.Files[d.rel] = batchFile{Size: sent, Mod: d.fi.ModTime(), Sent: sent}
		}
	}
	if len(man.Parts) == 0 {
		zw.Close()
		return nil, man, st, nil
	}
	raw, _ := json.Marshal(man)
	f, err := zw.Create("batch.json")
	if err != nil {
		return nil, man, st, err
	}
	f.Write(raw)
	err = zw.Close()
	return buf.Bytes(), man, st, err
}

// sendBatch sends one batch, keeping its manifest in reports/batches as the
// record of what went.
// Where the batches got to moves only once the endpoint took it.
func (m *Module) sendBatch(cfg core.Config) string {
	ws := m.C.WS
	body, man, st, err := m.buildBatch(cfg)
	if err != nil {
		return "no batch: " + err.Error()
	}
	if body == nil {
		raw, _ := json.Marshal(st)
		core.WriteFileAtomic(batchState(ws), raw, 0o600)
		return "no batch: nothing new"
	}
	req, err := http.NewRequest("POST", BundleURL(cfg), bytes.NewReader(body))
	if err != nil {
		return "no batch: share_bundle_url: " + err.Error()
	}
	req.Header.Set("Content-Type", "application/zip")
	req.Header.Set("User-Agent", "hive/"+core.Version)
	req.Header.Set("X-Hive-Install", cfg.InstallID)
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return "batch waits: couldn't reach " + BundleURL(cfg)
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Sprintf("batch waits: %s answered %d", BundleURL(cfg), resp.StatusCode)
	}
	raw, _ := json.MarshalIndent(man, "", " ")
	core.WriteFileAtomic(filepath.Join(Dir(ws), "batches", time.Now().UTC().Format("060102-150405.000000000")+".json"), raw, 0o600)
	raw, _ = json.Marshal(st)
	core.WriteFileAtomic(batchState(ws), raw, 0o600)
	var n int
	for _, p := range man.Parts {
		n += p.Bytes
	}
	return fmt.Sprintf("batch of %d files, %d KB", len(man.Parts), (n+1023)>>10)
}
