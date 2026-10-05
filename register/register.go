// Package register exposes the Zstandard provider catalog without init-time
// side effects.
package register

import (
	zstd "github.com/jtenner/wago-zstd"
	wago "github.com/wago-org/wago"
)

// Providers returns a fresh provider catalog entry.
func Providers() []wago.PluginProvider { return []wago.PluginProvider{zstd.Provider()} }
