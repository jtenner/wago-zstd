//go:build tinygo

package zstd

import (
	"runtime"
	"testing"
)

// TestTinyGoHostIntegration is the non-skipping TinyGo host qualification.
// It executes the Wasm32, Memory64, and WasmGC fixtures through the plugin;
// it is not a test of a guest produced by TinyGo.
func TestTinyGoHostIntegration(t *testing.T) {
	if runtime.Compiler != "tinygo" {
		t.Fatalf("compiler = %q, want tinygo", runtime.Compiler)
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Fatalf("qualification host = %s/%s, want linux/amd64", runtime.GOOS, runtime.GOARCH)
	}
	testWagoGuestABIs(t)
}
