// Package docs holds the Hive's documentation. Its one test keeps arch.svg
// drawn from arch.json, the diagram's source, so the two never drift apart.
package docs

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"math"
	"os"
	"testing"
)

var update = flag.Bool("update", false, "rewrite arch.svg from arch.json")

type diagram struct {
	Title string `json:"title"`
	Zones []struct {
		ID       string  `json:"id"`
		Label    string  `json:"label"`
		LabelCol float64 `json:"label_col"`
	} `json:"zones"`
	Nodes []node `json:"nodes"`
	Edges []struct {
		From   string  `json:"from"`
		To     string  `json:"to"`
		Label  string  `json:"label"`
		Offset float64 `json:"offset"`
	} `json:"edges"`
}

type node struct {
	ID    string  `json:"id"`
	Label string  `json:"label"`
	Sub   string  `json:"sub"`
	Col   float64 `json:"col"`
	Row   float64 `json:"row"`
	Zone  string  `json:"zone"`
	Kind  string  `json:"kind"`
}

const (
	colW, rowH   = 220.0, 132.0
	boxW, boxH   = 176.0, 56.0
	margin, top  = 24.0, 56.0
	zonePad      = 12.0
	zoneLabelPad = 22.0
)

var fills = map[string]string{"person": "#fff4d6", "agent": "#ffffff", "host": "#ffffff", "outside": "#f4f4f4"}

var zoneStyle = map[string][2]string{
	"sandbox": {"#edf6ed", "#4f8a4f"},
	"daemon":  {"#edf1fa", "#4d68a3"},
}

func center(n node) (float64, float64) {
	return margin + boxW/2 + n.Col*colW, top + zoneLabelPad + boxH/2 + n.Row*rowH
}

// clip is how far along (dx, dy) from a box's centre its border lies.
func clip(dx, dy float64) float64 {
	t := math.Inf(1)
	if dx != 0 {
		t = math.Min(t, boxW/2/math.Abs(dx))
	}
	if dy != 0 {
		t = math.Min(t, boxH/2/math.Abs(dy))
	}
	return t
}

func render(d diagram) ([]byte, error) {
	byID := map[string]node{}
	maxC, maxR := 0.0, 0.0
	for _, n := range d.Nodes {
		if _, dup := byID[n.ID]; dup {
			return nil, fmt.Errorf("node %q twice", n.ID)
		}
		if _, ok := fills[n.Kind]; !ok {
			return nil, fmt.Errorf("node %q: unknown kind %q", n.ID, n.Kind)
		}
		byID[n.ID] = n
		maxC, maxR = math.Max(maxC, n.Col), math.Max(maxR, n.Row)
	}
	w := 2*margin + boxW + maxC*colW
	h := top + zoneLabelPad + boxH + maxR*rowH + margin
	var b bytes.Buffer
	esc := html.EscapeString
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%.0f" height="%.0f" viewBox="0 0 %.0f %.0f" font-family="Helvetica, Arial, sans-serif">`+"\n", w, h, w, h)
	b.WriteString(`<defs><marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0,0 L10,5 L0,10 z" fill="#444"/></marker></defs>` + "\n")
	fmt.Fprintf(&b, `<rect width="100%%" height="100%%" fill="#ffffff"/>`+"\n")
	fmt.Fprintf(&b, `<text x="%.0f" y="32" font-size="17" font-weight="bold" fill="#222">%s</text>`+"\n", margin, esc(d.Title))

	for _, z := range d.Zones {
		x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
		for _, n := range d.Nodes {
			if n.Zone != z.ID {
				continue
			}
			cx, cy := center(n)
			x0, y0 = math.Min(x0, cx-boxW/2), math.Min(y0, cy-boxH/2)
			x1, y1 = math.Max(x1, cx+boxW/2), math.Max(y1, cy+boxH/2)
		}
		if math.IsInf(x0, 1) {
			return nil, fmt.Errorf("zone %q has no nodes", z.ID)
		}
		st, ok := zoneStyle[z.ID]
		if !ok {
			st = [2]string{"#f6f6f6", "#888888"}
		}
		fmt.Fprintf(&b, `<rect x="%.0f" y="%.0f" width="%.0f" height="%.0f" rx="10" fill="%s" stroke="%s" stroke-dasharray="6 4"/>`+"\n",
			x0-zonePad, y0-zoneLabelPad, x1-x0+2*zonePad, y1-y0+zoneLabelPad+zonePad, st[0], st[1])
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" font-size="12" font-weight="bold" fill="%s">%s</text>`+"\n", x0-zonePad+10+z.LabelCol*colW, y0-zoneLabelPad+15, st[1], esc(z.Label))
	}
	for _, n := range d.Nodes {
		if n.Zone != "" && !hasZone(d, n.Zone) {
			return nil, fmt.Errorf("node %q: unknown zone %q", n.ID, n.Zone)
		}
		cx, cy := center(n)
		fmt.Fprintf(&b, `<rect x="%.0f" y="%.0f" width="%.0f" height="%.0f" rx="6" fill="%s" stroke="#333"/>`+"\n", cx-boxW/2, cy-boxH/2, boxW, boxH, fills[n.Kind])
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" font-size="14" font-weight="bold" text-anchor="middle" fill="#222">%s</text>`+"\n", cx, cy-4, esc(n.Label))
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" font-size="11" text-anchor="middle" fill="#555">%s</text>`+"\n", cx, cy+14, esc(n.Sub))
	}
	var labels bytes.Buffer
	for _, e := range d.Edges {
		a, okA := byID[e.From]
		c, okC := byID[e.To]
		if !okA || !okC {
			return nil, fmt.Errorf("edge %s -> %s: unknown node", e.From, e.To)
		}
		ax, ay := center(a)
		cx, cy := center(c)
		dx, dy := cx-ax, cy-ay
		l := math.Hypot(dx, dy)
		ux, uy := dx/l, dy/l
		// Offset moves the edge sideways, so a pair running both ways shows as two lines.
		px, py := -uy*e.Offset, ux*e.Offset
		ax, ay, cx, cy = ax+px, ay+py, cx+px, cy+py
		s, t := clip(ux, uy)+3, clip(ux, uy)+5
		x1, y1, x2, y2 := ax+ux*s, ay+uy*s, cx-ux*t, cy-uy*t
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#444" stroke-width="1.4" marker-end="url(#arrow)"/>`+"\n", x1, y1, x2, y2)
		if e.Label != "" {
			mx, my := (x1+x2)/2+px*1.6, (y1+y2)/2+py*1.6
			lw := 6.2*float64(len(e.Label)) + 10
			fmt.Fprintf(&labels, `<rect x="%.1f" y="%.1f" width="%.1f" height="17" rx="3" fill="#ffffff" fill-opacity="0.92"/>`+"\n", mx-lw/2, my-9, lw)
			fmt.Fprintf(&labels, `<text x="%.1f" y="%.1f" font-size="11" text-anchor="middle" fill="#333">%s</text>`+"\n", mx, my+4, esc(e.Label))
		}
	}
	b.Write(labels.Bytes())
	b.WriteString("</svg>\n")
	return b.Bytes(), nil
}

func hasZone(d diagram, id string) bool {
	for _, z := range d.Zones {
		if z.ID == id {
			return true
		}
	}
	return false
}

// TestArch fails when arch.svg isn't what arch.json draws; -update redraws it.
func TestArch(t *testing.T) {
	src, err := os.ReadFile("arch.json")
	if err != nil {
		t.Fatal(err)
	}
	var d diagram
	dec := json.NewDecoder(bytes.NewReader(src))
	if err := dec.Decode(&d); err != nil {
		t.Fatalf("arch.json: %v", err)
	}
	got, err := render(d)
	if err != nil {
		t.Fatalf("arch.json: %v", err)
	}
	if *update {
		if err := os.WriteFile("arch.svg", got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile("arch.svg")
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("arch.svg is not what arch.json draws; run: go test ./docs -run TestArch -update")
	}
}
