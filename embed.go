package protoconf_terraform

import (
	"embed"
)

//go:embed src
var InitTemplate embed.FS
