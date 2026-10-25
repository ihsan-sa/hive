package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// githubAPI is where the contribute issues are read, as anyone can read a
// public repo's; tests replace it.
var githubAPI = "https://api.github.com"

// ReadKey is where the owner keeps the key reports_read_url takes, under
// .hive/logins, which no run sees.
func ReadKey(ws core.Workspace) string {
	return filepath.Join(ws.Hive(), "logins", "report", "read.key")
}

// h0 says this Hive is the developer's: its own project is the upstream.
func (m *Module) h0(cfg core.Config) bool {
	for _, p := range m.C.Projects.List() {
		if p.Self && strings.EqualFold(p.Repo, Upstream(cfg)) {
			return true
		}
	}
	return false
}

// Received is the improve sweep's section on what installs sent the
// developer: the reports the endpoint stored, read through reports_read_url
// with the owner's key, and the contribute issues upstream, each kind of
// failure with how many installs and how many times. It is empty on any
// Hive but the developer's and when nothing was received.
func (m *Module) Received() string {
	cfg := m.C.WS.LoadConfig()
	if !m.h0(cfg) && cfg.ReportsReadURL == "" {
		return ""
	}
	var b strings.Builder
	if cfg.ReportsReadURL != "" {
		rs, err := m.readStore(cfg)
		if err != nil {
			fmt.Fprintf(&b, "The reports endpoint's store couldn't be read: %s\n\n", core.Clip(err.Error(), 300))
		} else {
			b.WriteString(storeDigest(rs))
		}
	}
	if m.h0(cfg) {
		b.WriteString(issuesDigest(Upstream(cfg)))
	}
	if strings.TrimSpace(b.String()) == "" {
		return ""
	}
	return "## What installs sent (docs/report.md)\n\nFile a fix for each real one, saying which installs and how many: the fingerprint or issue, the installs and the count below.\n\n" + b.String()
}

// readStore reads the stored reports: a JSON array of them, or one a line.
func (m *Module) readStore(cfg core.Config) ([]Report, error) {
	req, err := http.NewRequest("GET", cfg.ReportsReadURL, nil)
	if err != nil {
		return nil, err
	}
	if key, err := os.ReadFile(ReadKey(m.C.WS)); err == nil {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(key)))
	}
	req.Header.Set("User-Agent", "hive/"+core.Version)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%s answered %d", cfg.ReportsReadURL, resp.StatusCode)
	}
	var rs []Report
	if json.Unmarshal(raw, &rs) == nil {
		return rs, nil
	}
	for _, line := range bytes.Split(raw, []byte("\n")) {
		var r Report
		if json.Unmarshal(line, &r) == nil && r.Type != "" {
			rs = append(rs, r)
		}
	}
	return rs, nil
}

// storeDigest groups the stored failures by fingerprint, most installs
// first, and sums the usage.
func storeDigest(rs []Report) string {
	type group struct {
		r                  Report
		count              int
		installs, versions map[string]bool
	}
	groups := map[string]*group{}
	installs := map[string]bool{}
	use := map[string]float64{}
	for _, r := range rs {
		installs[r.Install] = true
		if r.Type == "usage" {
			for k, v := range r.Usage {
				use[k] += v
			}
			continue
		}
		fp := Fingerprint(r)
		g := groups[fp]
		if g == nil {
			g = &group{r: r, installs: map[string]bool{}, versions: map[string]bool{}}
			groups[fp] = g
		}
		g.count++
		g.installs[r.Install], g.versions[r.Version] = true, true
	}
	if len(rs) == 0 {
		return "The reports endpoint's store holds no reports.\n\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Reports the endpoint stored: %d from %d installs; usage across them: %d runs, %d turns, %d merges, %d reverts, $%.2f.\n\n",
		len(rs), len(installs), int(use["runs"]), int(use["turns"]), int(use["merges"]), int(use["reverts"]), use["cost_usd"])
	fps := make([]string, 0, len(groups))
	for fp := range groups {
		fps = append(fps, fp)
	}
	sort.Slice(fps, func(i, j int) bool {
		a, c := groups[fps[i]], groups[fps[j]]
		if len(a.installs) != len(c.installs) {
			return len(a.installs) > len(c.installs)
		}
		return a.count > c.count || a.count == c.count && fps[i] < fps[j]
	})
	for i, fp := range fps {
		if i >= 30 {
			fmt.Fprintf(&b, "- and %d more kinds\n", len(fps)-i)
			break
		}
		g := groups[fp]
		ids := slices.Sorted(func(yield func(string) bool) {
			for id := range g.installs {
				if !yield(id[:min(8, len(id))]) { // enough to tell installs apart
					return
				}
			}
		})
		vs := slices.Sorted(func(yield func(string) bool) {
			for v := range g.versions {
				if !yield(v) {
					return
				}
			}
		})
		fmt.Fprintf(&b, "- %s `%s`: %d reports from %d installs (%s), versions %s: %s\n", fp, g.r.Kind, g.count, len(g.installs),
			strings.Join(ids[:min(len(ids), 6)], ", "), strings.Join(vs, ", "), core.Clip(g.r.Message, 300))
	}
	return b.String() + "\n"
}

var (
	reportMark  = regexp.MustCompile(`<!-- ` + issueMarker + `([0-9a-f]+) -->`)
	installMark = regexp.MustCompile(`<!-- ` + installMarker + `([0-9a-f]+) -->`)
	seenTimes   = regexp.MustCompile(`Seen on install [0-9a-f]+ (once|(\d+) times)`)
)

// issuesDigest lists the open contribute issues upstream with the installs
// that commented on each and how many times they saw it.
func issuesDigest(repo string) string {
	var issues []struct {
		Number   int    `json:"number"`
		Title    string `json:"title"`
		Body     string `json:"body"`
		Comments int    `json:"comments"`
	}
	if err := getJSON(fmt.Sprintf("%s/repos/%s/issues?state=open&per_page=100", githubAPI, repo), &issues); err != nil {
		return fmt.Sprintf("The contribute issues on %s couldn't be read: %s\n\n", repo, core.Clip(err.Error(), 300))
	}
	var lines []string
	for _, is := range issues {
		if !reportMark.MatchString(is.Body) || len(lines) >= 20 {
			continue
		}
		var comments []struct {
			Body string `json:"body"`
		}
		installs, times := map[string]bool{}, 0
		if getJSON(fmt.Sprintf("%s/repos/%s/issues/%d/comments?per_page=100", githubAPI, repo, is.Number), &comments) == nil {
			for _, c := range comments {
				if m := installMark.FindStringSubmatch(c.Body); m != nil && !installs[m[1]] {
					installs[m[1]] = true
					if t := seenTimes.FindStringSubmatch(c.Body); t != nil && t[2] != "" {
						n := 0
						fmt.Sscan(t[2], &n)
						times += n
					} else {
						times++
					}
				}
			}
		}
		lines = append(lines, fmt.Sprintf("- %s#%d (%s) %s: %d installs, %d times", repo, is.Number, reportMark.FindStringSubmatch(is.Body)[1], core.Clip(is.Title, 160), len(installs), times))
	}
	if len(lines) == 0 {
		return ""
	}
	return "Contribute issues open on " + repo + ":\n" + strings.Join(lines, "\n") + "\n\n"
}

func getJSON(url string, v any) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "hive/"+core.Version)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s answered %d", url, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v)
}
