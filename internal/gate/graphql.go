package gate

import (
	"fmt"
	"strings"
	"unicode"
)

// A GraphQL document, as far as the broker needs to read one: operations and
// fragments, their fields, arguments and nesting. It reads what gh sends; it
// is not a full GraphQL parser, and what it can't read it refuses.

type gqlValue struct {
	Var  string              // $name, when the value is a variable
	Str  *string             // a string literal
	List []gqlValue          // a list
	Obj  map[string]gqlValue // an input object
	// numbers, booleans, null and enums carry nothing the broker checks
}

type gqlSel struct {
	Field    string // "" for a fragment spread or inline fragment
	Args     map[string]gqlValue
	Spread   string   // ...Name
	Inline   bool     // ... on T { } or ... { }
	Children []gqlSel // the selection set, nil for a scalar
	Has      bool     // has a selection set
}

type gqlOp struct {
	Kind string // query, mutation, subscription
	Sels []gqlSel
}

type gqlDoc struct {
	Ops   []gqlOp
	Frags map[string][]gqlSel
}

type gqlParser struct {
	toks []string
	i    int
}

func parseGraphQL(src string) (*gqlDoc, error) {
	toks, err := gqlLex(src)
	if err != nil {
		return nil, err
	}
	p := &gqlParser{toks: toks}
	doc := &gqlDoc{Frags: map[string][]gqlSel{}}
	for p.i < len(p.toks) {
		switch t := p.peek(); t {
		case "{":
			sels, err := p.selections(0)
			if err != nil {
				return nil, err
			}
			doc.Ops = append(doc.Ops, gqlOp{Kind: "query", Sels: sels})
		case "query", "mutation", "subscription":
			p.i++
			if isName(p.peek()) {
				p.i++
			}
			if p.peek() == "(" {
				if err := p.skipBalanced("(", ")"); err != nil {
					return nil, err
				}
			}
			if err := p.directives(); err != nil {
				return nil, err
			}
			sels, err := p.selections(0)
			if err != nil {
				return nil, err
			}
			doc.Ops = append(doc.Ops, gqlOp{Kind: t, Sels: sels})
		case "fragment":
			p.i++
			name := p.next()
			if !isName(name) || p.next() != "on" || !isName(p.next()) {
				return nil, fmt.Errorf("graphql: a fragment I can't read")
			}
			if err := p.directives(); err != nil {
				return nil, err
			}
			sels, err := p.selections(0)
			if err != nil {
				return nil, err
			}
			doc.Frags[name] = sels
		default:
			return nil, fmt.Errorf("graphql: unexpected %q", t)
		}
	}
	return doc, nil
}

func (p *gqlParser) peek() string {
	if p.i < len(p.toks) {
		return p.toks[p.i]
	}
	return ""
}

func (p *gqlParser) next() string { t := p.peek(); p.i++; return t }

func (p *gqlParser) skipBalanced(open, close string) error {
	depth := 0
	for p.i < len(p.toks) {
		switch p.next() {
		case open:
			depth++
		case close:
			if depth--; depth == 0 {
				return nil
			}
		}
	}
	return fmt.Errorf("graphql: unbalanced %s", open)
}

func (p *gqlParser) directives() error {
	for p.peek() == "@" {
		p.i++
		if !isName(p.next()) {
			return fmt.Errorf("graphql: a directive I can't read")
		}
		if p.peek() == "(" {
			if _, err := p.args(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *gqlParser) selections(depth int) ([]gqlSel, error) {
	if depth > 40 {
		return nil, fmt.Errorf("graphql: nested too deep")
	}
	if p.next() != "{" {
		return nil, fmt.Errorf("graphql: expected {")
	}
	var sels []gqlSel
	for {
		t := p.peek()
		switch {
		case t == "}":
			p.i++
			return sels, nil
		case t == "":
			return nil, fmt.Errorf("graphql: unclosed {")
		case t == "...":
			p.i++
			if isName(p.peek()) && p.peek() != "on" {
				s := gqlSel{Spread: p.next()}
				if err := p.directives(); err != nil {
					return nil, err
				}
				sels = append(sels, s)
				continue
			}
			if p.peek() == "on" {
				p.i++
				if !isName(p.next()) {
					return nil, fmt.Errorf("graphql: an inline fragment I can't read")
				}
			}
			if err := p.directives(); err != nil {
				return nil, err
			}
			kids, err := p.selections(depth + 1)
			if err != nil {
				return nil, err
			}
			sels = append(sels, gqlSel{Inline: true, Children: kids, Has: true})
		case isName(t):
			p.i++
			s := gqlSel{Field: t}
			if p.peek() == ":" { // alias: the field's name follows
				p.i++
				if s.Field = p.next(); !isName(s.Field) {
					return nil, fmt.Errorf("graphql: an alias I can't read")
				}
			}
			if p.peek() == "(" {
				args, err := p.args()
				if err != nil {
					return nil, err
				}
				s.Args = args
			}
			if err := p.directives(); err != nil {
				return nil, err
			}
			if p.peek() == "{" {
				kids, err := p.selections(depth + 1)
				if err != nil {
					return nil, err
				}
				s.Children, s.Has = kids, true
			}
			sels = append(sels, s)
		default:
			return nil, fmt.Errorf("graphql: unexpected %q", t)
		}
	}
}

func (p *gqlParser) args() (map[string]gqlValue, error) {
	p.i++ // (
	args := map[string]gqlValue{}
	for p.peek() != ")" {
		name := p.next()
		if !isName(name) || p.next() != ":" {
			return nil, fmt.Errorf("graphql: an argument I can't read")
		}
		v, err := p.value(0)
		if err != nil {
			return nil, err
		}
		args[name] = v
	}
	p.i++
	return args, nil
}

func (p *gqlParser) value(depth int) (gqlValue, error) {
	if depth > 20 {
		return gqlValue{}, fmt.Errorf("graphql: a value nested too deep")
	}
	t := p.next()
	switch {
	case t == "":
		return gqlValue{}, fmt.Errorf("graphql: a value missing")
	case t == "$":
		n := p.next()
		if !isName(n) {
			return gqlValue{}, fmt.Errorf("graphql: a variable I can't read")
		}
		return gqlValue{Var: n}, nil
	case strings.HasPrefix(t, `"`):
		s := t[1:]
		return gqlValue{Str: &s}, nil
	case t == "[":
		var v gqlValue
		v.List = []gqlValue{}
		for p.peek() != "]" {
			e, err := p.value(depth + 1)
			if err != nil {
				return gqlValue{}, err
			}
			v.List = append(v.List, e)
		}
		p.i++
		return v, nil
	case t == "{":
		v := gqlValue{Obj: map[string]gqlValue{}}
		for p.peek() != "}" {
			k := p.next()
			if !isName(k) || p.next() != ":" {
				return gqlValue{}, fmt.Errorf("graphql: an input object I can't read")
			}
			e, err := p.value(depth + 1)
			if err != nil {
				return gqlValue{}, err
			}
			v.Obj[k] = e
		}
		p.i++
		return v, nil
	case isName(t) || t[0] == '-' || unicode.IsDigit(rune(t[0])):
		return gqlValue{}, nil // a number, boolean, null or enum
	}
	return gqlValue{}, fmt.Errorf("graphql: unexpected %q", t)
}

func isName(t string) bool {
	if t == "" || !(t[0] == '_' || unicode.IsLetter(rune(t[0]))) {
		return false
	}
	for _, r := range t {
		if !(r == '_' || r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r))) {
			return false
		}
	}
	return true
}

// gqlLex splits a document into tokens. A string token is `"` followed by
// its decoded text; commas and comments are dropped, as GraphQL ignores them.
func gqlLex(src string) ([]string, error) {
	var toks []string
	r := []rune(src)
	for i := 0; i < len(r); {
		c := r[i]
		switch {
		case c == ',' || unicode.IsSpace(c) || c == '\uFEFF':
			i++
		case c == '#':
			for i < len(r) && r[i] != '\n' && r[i] != '\r' {
				i++
			}
		case c == '.':
			if i+2 < len(r) && r[i+1] == '.' && r[i+2] == '.' {
				toks = append(toks, "...")
				i += 3
				continue
			}
			return nil, fmt.Errorf("graphql: a lone .")
		case strings.ContainsRune("{}()[]:!=@$|&", c):
			toks = append(toks, string(c))
			i++
		case c == '"':
			if i+2 < len(r) && r[i+1] == '"' && r[i+2] == '"' { // block string
				end := strings.Index(string(r[i+3:]), `"""`)
				if end < 0 {
					return nil, fmt.Errorf("graphql: an unclosed block string")
				}
				body := string(r[i+3:])[:end]
				toks = append(toks, `"`+body)
				i += 3 + len([]rune(body)) + 3
				continue
			}
			var b strings.Builder
			j := i + 1
			for ; j < len(r) && r[j] != '"'; j++ {
				if r[j] == '\n' {
					return nil, fmt.Errorf("graphql: a string across lines")
				}
				if r[j] == '\\' && j+1 < len(r) {
					j++
					switch r[j] {
					case 'n':
						b.WriteRune('\n')
					case 't':
						b.WriteRune('\t')
					case 'u':
						if j+4 >= len(r) {
							return nil, fmt.Errorf("graphql: a bad escape")
						}
						var n rune
						for _, h := range r[j+1 : j+5] {
							d := strings.IndexRune("0123456789abcdef", unicode.ToLower(h))
							if d < 0 {
								return nil, fmt.Errorf("graphql: a bad escape")
							}
							n = n*16 + rune(d)
						}
						b.WriteRune(n)
						j += 4
					default:
						b.WriteRune(r[j])
					}
					continue
				}
				b.WriteRune(r[j])
			}
			if j >= len(r) {
				return nil, fmt.Errorf("graphql: an unclosed string")
			}
			toks = append(toks, `"`+b.String())
			i = j + 1
		case c == '_' || c == '-' || c == '+' || c < 128 && (unicode.IsLetter(c) || unicode.IsDigit(c)):
			num := c == '-' || c == '+' || unicode.IsDigit(c)
			j := i + 1
			for j < len(r) && (r[j] == '_' || num && (r[j] == '.' || r[j] == '+' || r[j] == '-') || r[j] < 128 && (unicode.IsLetter(r[j]) || unicode.IsDigit(r[j]))) {
				j++
			}
			toks = append(toks, string(r[i:j]))
			i = j
		default:
			return nil, fmt.Errorf("graphql: unexpected %q", c)
		}
	}
	return toks, nil
}
