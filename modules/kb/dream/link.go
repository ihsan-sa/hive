package main

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Linking: two notes are related when one names the other ([[name]] in its
// text) or when they share enough uncommon words (tf-idf cosine), each note
// taking its few closest. Every link goes both ways, across projects, and is
// written as the note's Related list, which the pass owns and rewrites.
const (
	linkFloor = 0.2 // the least cosine that counts as related
	linkTop   = 3   // the closest notes each note takes
)

var stop = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`about above after again against also because been before being below between both could does doing down during each from further have having here into itself just more most once only other over same should some such than that their theirs them then there these they this those through under until very were what when where which while whom will with would your yours this that with from have were been they their there what when which while will would could should into onto than then them these those also only just over such each more most some very same other again about after before because being both does doing done down during further here itself once under until upon note notes text says said hive`) {
		stop[w] = true
	}
}

func words(s string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(w)) >= 4 && !stop[w] && strings.IndexFunc(w, unicode.IsLetter) >= 0 {
			out = append(out, w)
		}
	}
	return out
}

// link sets every linkable note's Related list and returns the pairs that
// are new, each as [a, b] with a < b.
func link(notes map[string]*Note, shipped map[string]bool) [][2]string {
	var ns []*Note
	byName := map[string]*Note{}
	for _, n := range notes {
		if n.topLevel() || (strings.HasPrefix(n.Path, "practice/") && !shipped[n.Path]) {
			ns = append(ns, n)
			byName[n.name()] = n
		}
	}
	sort.Slice(ns, func(i, j int) bool { return ns[i].Path < ns[j].Path })
	pairs := map[[2]string]bool{}
	add := func(a, b string) {
		if a == b {
			return
		}
		if a > b {
			a, b = b, a
		}
		pairs[[2]string{a, b}] = true
	}
	// Explicit links, from the text (the History of a living note included).
	for _, n := range ns {
		for _, m := range linkRe.FindAllStringSubmatch(n.Body, -1) {
			if byName[m[1]] != nil {
				add(n.name(), m[1])
			}
		}
	}
	// Similar notes: retired ones, summaries and the practice note keep only
	// explicit links, so the lists point at what is true now.
	var sim []*Note
	for _, n := range ns {
		if n.topLevel() && n.get("retired") == "" && !n.distilled() {
			sim = append(sim, n)
		}
	}
	vecs := make([]map[string]float64, len(sim))
	df := map[string]int{}
	for i, n := range sim {
		tf := map[string]float64{}
		for _, w := range words(n.title() + " " + n.title() + " " + n.get("tags") + " " + n.Body) {
			tf[w]++
		}
		vecs[i] = tf
		for w := range tf {
			df[w]++
		}
	}
	for i, v := range vecs {
		var norm float64
		for w, c := range v {
			v[w] = (1 + math.Log(c)) * math.Log(float64(len(sim)+1)/float64(df[w]))
			norm += v[w] * v[w]
		}
		norm = math.Sqrt(norm)
		for w := range v {
			if norm > 0 {
				v[w] /= norm
			}
		}
		vecs[i] = v
	}
	for i := range sim {
		type near struct {
			j   int
			cos float64
		}
		var best []near
		for j := range sim {
			if i == j {
				continue
			}
			var cos float64
			for w, x := range vecs[i] {
				cos += x * vecs[j][w]
			}
			if cos >= linkFloor {
				best = append(best, near{j, cos})
			}
		}
		sort.Slice(best, func(a, b int) bool {
			if best[a].cos != best[b].cos {
				return best[a].cos > best[b].cos
			}
			return best[a].j < best[b].j
		})
		for k, b := range best {
			if k == linkTop {
				break
			}
			add(sim[i].name(), sim[b.j].name())
		}
	}
	related := map[string][]string{}
	for p := range pairs {
		related[p[0]] = append(related[p[0]], p[1])
		related[p[1]] = append(related[p[1]], p[0])
	}
	had := map[[2]string]bool{}
	for _, n := range ns {
		for _, r := range n.Related {
			a, b := n.name(), r
			if a > b {
				a, b = b, a
			}
			had[[2]string{a, b}] = true
		}
	}
	for _, n := range ns {
		r := related[n.name()]
		sort.Strings(r)
		n.Related = r
	}
	var fresh [][2]string
	for p := range pairs {
		if !had[p] {
			fresh = append(fresh, p)
		}
	}
	sort.Slice(fresh, func(i, j int) bool {
		if fresh[i][0] != fresh[j][0] {
			return fresh[i][0] < fresh[j][0]
		}
		return fresh[i][1] < fresh[j][1]
	})
	return fresh
}
