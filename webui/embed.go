// Package webui embeds the static management interface.
package webui

import (
	"embed"
)

// Assets contains the Field Manual management interface served by the admin listener.
//
//go:embed index.html
var Assets embed.FS
