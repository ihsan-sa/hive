// Package hive is the Hive's own source, carried in the binary so every
// install can read the code and design it runs. The knowledge base writes it
// out at start (internal/mod/kb). A folder is one word to add to the list.
package hive

import "embed"

// Source is the Hive's repository at the version this binary was built from.
//
//go:embed cmd internal modules docs install tests .github go.mod go.sum README.md LEARNINGS.md CLAUDE.md
var Source embed.FS
