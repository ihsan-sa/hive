// Package modules holds the default modules a fresh install ships with. They
// are written to .modules/ on first run and are ordinary modules after that:
// editable, replaceable, and improved through the same review-and-merge path.
package modules

import "embed"

// FS is the default modules' files.
//
//go:embed agents chat kb improve system workers
var FS embed.FS
