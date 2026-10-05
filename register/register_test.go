package register

import (
	"bytes"
	"os"
	"testing"

	zstd "github.com/jtenner/wago-zstd"
	wago "github.com/wago-org/wago"
)

func TestProviderCatalog(t *testing.T) {
	providers := Providers()
	if len(providers) != 1 || providers[0].Definition.ID != zstd.PluginID || providers[0].New == nil || providers[0].New() == nil {
		t.Fatalf("providers = %+v", providers)
	}
	want, err := wago.EncodeProviderCatalog("github.com/jtenner/wago-zstd/register", providers)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../wago.providers.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("wago.providers.json is stale; run: wago plugin catalog")
	}
}
