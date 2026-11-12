// Package games holds the example games that are built into the binary. On first run they are copied into the
// games directory beside it, where they can be edited or used as a starting point.
package games

import "embed"

//go:embed */game.yaml */tests.yaml
var FS embed.FS
