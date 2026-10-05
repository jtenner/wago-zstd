package zstd

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	goruntime "runtime"
	"testing"

	wago "github.com/wago-org/wago"
)

var (
	//go:embed testdata/wasm32.wasm
	wasm32Guest []byte
	//go:embed testdata/wasm64.wasm
	wasm64Guest []byte
	//go:embed testdata/gc.wasm
	gcGuest []byte
	//go:embed testdata/tinygo_guest.wasm
	tinyGoGuest []byte
)

func TestProviderDefinitionAndConfig(t *testing.T) {
	provider := Provider()
	if provider.New == nil || provider.ValidateConfig == nil {
		t.Fatal("provider is incomplete")
	}
	if _, err := wago.DefinitionDigest(provider.Definition); err != nil {
		t.Fatalf("definition: %v", err)
	}
	if got := provider.Definition.Provenance.License; got != "Apache-2.0" {
		t.Fatalf("license metadata = %q", got)
	}
	if got := provider.Definition.Version; got != "0.0.1" {
		t.Fatalf("version metadata = %q", got)
	}
	if got := provider.Definition.Compatibility.Platforms; len(got) != 1 || got[0] != "linux/amd64" {
		t.Fatalf("platform metadata = %v", got)
	}
	if err := provider.ValidateConfig(json.RawMessage(`{"max_concurrent":1,"max_window_bytes":1048576}`)); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	if err := provider.ValidateConfig(json.RawMessage(`{"max_concurrent":9}`)); err == nil {
		t.Fatal("invalid config accepted")
	}
	if err := provider.ValidateConfig(json.RawMessage(`{"unknown":1}`)); err == nil {
		t.Fatal("unknown config accepted")
	}
	if err := provider.ValidateConfig(json.RawMessage(`{"max_input_bytes":0}`)); err == nil {
		t.Fatal("explicit zero config accepted")
	}
}

func TestPluginSetUsesExactAuthorityAndNormalizedConfig(t *testing.T) {
	set, err := PluginSet(Config{MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Providers) != 1 || len(set.Selections) != 1 {
		t.Fatalf("set size = %d/%d", len(set.Providers), len(set.Selections))
	}
	selection := set.Selections[0]
	if len(selection.Grants) != 1 || selection.Grants[0].Name != wago.AuthorityHostImportDefine {
		t.Fatalf("grants = %+v", selection.Grants)
	}
	if !bytes.Contains(selection.Config, []byte(`"max_concurrent":1`)) {
		t.Fatalf("config = %s", selection.Config)
	}
	if _, err := PluginSet(Config{}, Config{}); err == nil {
		t.Fatal("multiple configs accepted")
	}
}

func TestGCArrayByteLengthUsesBackingWidth(t *testing.T) {
	tests := []struct {
		storage wago.GuestGCArrayStorage
		length  uint32
		want    uint64
		ok      bool
	}{
		{wago.GuestGCArrayI8, 7, 7, true},
		{wago.GuestGCArrayI32, 7, 28, true},
		{wago.GuestGCArrayI64, 7, 0, false},
	}
	for _, test := range tests {
		got, ok := gcArrayByteLength(wago.GuestGCArrayInfo{Storage: test.storage, Length: test.length})
		if got != test.want || ok != test.ok {
			t.Errorf("storage %v length %d = %d/%v, want %d/%v", test.storage, test.length, got, ok, test.want, test.ok)
		}
	}
}

func TestPackResultLayoutAndFailureNormalization(t *testing.T) {
	tests := []struct {
		name        string
		status      Status
		written     int
		wantStatus  Status
		wantWritten uint32
	}{
		{"success", StatusOK, 1234, StatusOK, 1234},
		{"failure", StatusInvalidData, 1234, StatusInvalidData, 0},
		{"negative success", StatusOK, -1, StatusInternalError, 0},
		{"oversize success", StatusOK, int(HardMaxOutputBytes) + 1, StatusInternalError, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			packed := packResult(test.status, test.written)
			if got := Status(uint32(packed)); got != test.wantStatus {
				t.Fatalf("status = %v, want %v", got, test.wantStatus)
			}
			if got := uint32(packed >> 32); got != test.wantWritten {
				t.Fatalf("written = %d, want %d", got, test.wantWritten)
			}
		})
	}
}

func TestWagoGuestABIs(t *testing.T) {
	if goruntime.GOARCH != "amd64" && goruntime.GOARCH != "arm64" {
		t.Skip("Wago native execution integration runs on amd64 and arm64")
	}
	testWagoGuestABIs(t)
}

func TestTinyGoGuestPackedABI(t *testing.T) {
	if goruntime.GOARCH != "amd64" && goruntime.GOARCH != "arm64" {
		t.Skip("Wago native execution integration runs on amd64 and arm64")
	}
	testTinyGoGuestPackedABI(t)
}

func testTinyGoGuestPackedABI(t *testing.T) {
	t.Helper()
	set, err := PluginSet(Config{
		MaxInputBytes:         1 << 20,
		MaxOutputBytes:        1 << 20,
		MaxWindowBytes:        1 << 20,
		MaxDecoderMemoryBytes: 1 << 20,
		MaxConcurrent:         1,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := wago.NewRuntime()
	defer runtime.Close()
	if err := runtime.LoadPlugins(context.Background(), set); err != nil {
		t.Fatal(err)
	}
	compiled, err := runtime.Compile(tinyGoGuest)
	if err != nil {
		t.Fatalf("compile TinyGo guest: %v", err)
	}
	defer compiled.Close()
	instance, err := runtime.Instantiate(context.Background(), compiled)
	if err != nil {
		t.Fatalf("instantiate TinyGo guest: %v", err)
	}
	defer instance.Close()
	if _, err := instance.Invoke("_initialize"); err != nil {
		t.Fatalf("initialize TinyGo guest: %v", err)
	}
	results, err := instance.Invoke("run")
	if err != nil {
		t.Fatalf("run TinyGo guest: %v", err)
	}
	if len(results) != 1 || wago.AsI32(results[0]) != 0 {
		t.Fatalf("TinyGo guest result = %v, want 0", results)
	}
}

func testWagoGuestABIs(t *testing.T) {
	set, err := PluginSet(Config{
		MaxInputBytes:         1 << 20,
		MaxOutputBytes:        1 << 20,
		MaxWindowBytes:        1 << 20,
		MaxDecoderMemoryBytes: 1 << 20,
		MaxConcurrent:         1,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := wago.NewRuntime()
	defer runtime.Close()
	if err := runtime.LoadPlugins(context.Background(), set); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		guest      []byte
		wantResult int32
		wantMemory string
	}{
		{"wasm32", wasm32Guest, int32(len("Wago Zstandard memory32 integration")), "Wago Zstandard memory32 integration"},
		{"wasm64", wasm64Guest, int32(len("Wago Zstandard memory64 integration")), "Wago Zstandard memory64 integration"},
		{"gc", gcGuest, 1, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if goruntime.GOOS == "windows" && test.name != "wasm32" {
				t.Skip("Wago v0.1.0-beta.11 disables Memory64 and Wasm GC execution on Windows")
			}
			compiled, err := runtime.Compile(test.guest)
			if err != nil {
				t.Fatal(err)
			}
			defer compiled.Close()
			instance, err := runtime.Instantiate(context.Background(), compiled)
			if err != nil {
				t.Fatal(err)
			}
			defer instance.Close()
			results, err := instance.InvokeContext(context.Background(), "run")
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || wago.AsI32(results[0]) != test.wantResult {
				t.Fatalf("result = %v, want %d", results, test.wantResult)
			}
			if test.wantMemory != "" {
				memory := instance.Memory()
				if memory == nil || !bytes.Equal(memory.UnsafeBytes()[16384:16384+len(test.wantMemory)], []byte(test.wantMemory)) {
					t.Fatalf("decompressed memory does not contain %q", test.wantMemory)
				}
				testLinearABI(t, instance, test.name == "wasm64", []byte(test.wantMemory))
				return
			}
			for _, export := range []string{"short_output", "bounds", "invalid", "checksum", "overlap", "immutable_backing_limit", "packed_parity"} {
				results, err := instance.Invoke(export)
				if err != nil {
					t.Fatalf("%s: %v", export, err)
				}
				if len(results) != 1 || wago.AsI32(results[0]) != 1 {
					t.Fatalf("%s result = %v, want 1", export, results)
				}
			}
		})
	}
}

func testLinearABI(t *testing.T, instance *wago.Instance, memory64 bool, plain []byte) {
	t.Helper()
	memory := instance.Memory().UnsafeBytes()

	invokePair := func(name string, arguments ...uint64) (Status, int) {
		t.Helper()
		results, err := instance.Invoke(name, arguments...)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(results) != 2 {
			t.Fatalf("%s results = %v", name, results)
		}
		return Status(wago.AsI32(results[0])), int(wago.AsI32(results[1]))
	}
	invokePacked := func(name string, arguments ...uint64) (Status, int) {
		t.Helper()
		results, err := instance.Invoke(name, arguments...)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(results) != 1 {
			t.Fatalf("%s results = %v", name, results)
		}
		return Status(uint32(results[0])), int(uint32(results[0] >> 32))
	}

	// The WAT-authored proxies prove the packed and legacy imports have the
	// same parameters and behavior for both success and failure.
	copy(memory[28000:], plain)
	legacyStatus, legacyCompressed := invokePair("compress_proxy", 28000, uint64(len(plain)), 30000, 1024, 0)
	packedStatus, packedCompressed := invokePacked("compress_packed_proxy", 28000, uint64(len(plain)), 32000, 1024, 0)
	if packedStatus != legacyStatus || packedCompressed != legacyCompressed || !bytes.Equal(memory[30000:30000+legacyCompressed], memory[32000:32000+packedCompressed]) {
		t.Fatalf("packed compress parity = %v/%d, legacy = %v/%d", packedStatus, packedCompressed, legacyStatus, legacyCompressed)
	}
	legacyStatus, legacyWritten := invokePair("decompress_proxy", 30000, uint64(legacyCompressed), 34000, 1024)
	packedStatus, packedWritten := invokePacked("decompress_packed_proxy", 32000, uint64(packedCompressed), 36000, 1024)
	if packedStatus != legacyStatus || packedWritten != legacyWritten || !bytes.Equal(memory[34000:34000+legacyWritten], memory[36000:36000+packedWritten]) {
		t.Fatalf("packed decompress parity = %v/%d, legacy = %v/%d", packedStatus, packedWritten, legacyStatus, legacyWritten)
	}
	for index := 38000; index < 38016; index++ {
		memory[index] = 0x4d
	}
	legacyStatus, legacyWritten = invokePair("compress_proxy", 28000, uint64(len(plain)), 38000, 16, 23)
	packedStatus, packedWritten = invokePacked("compress_packed_proxy", 28000, uint64(len(plain)), 38000, 16, 23)
	if packedStatus != legacyStatus || packedWritten != 0 || legacyWritten != 0 || !bytes.Equal(memory[38000:38016], bytes.Repeat([]byte{0x4d}, 16)) {
		t.Fatalf("packed failure parity = %v/%d, legacy = %v/%d", packedStatus, packedWritten, legacyStatus, legacyWritten)
	}

	// Both calls intentionally overlap input and output by one byte. Complete
	// success proves the plugin does not write before it has finished reading.
	copy(memory[4096:], plain)
	status, compressed := invokePair("compress_proxy", 4096, uint64(len(plain)), 4097, 8192, 0)
	if status != StatusOK || compressed == 0 {
		t.Fatalf("overlap compress = %d/%v", compressed, status)
	}
	status, written := invokePair("decompress_proxy", 4097, uint64(compressed), 4096, 8192)
	if status != StatusOK || written != len(plain) || !bytes.Equal(memory[4096:4096+written], plain) {
		t.Fatalf("overlap decompress = %d/%v", written, status)
	}

	// Recreate a non-overlapping compressed frame for failure-path checks.
	copy(memory[4096:], plain)
	status, compressed = invokePair("compress_proxy", 4096, uint64(len(plain)), 8192, 8192, 0)
	if status != StatusOK {
		t.Fatalf("setup compress = %d/%v", compressed, status)
	}
	for index := 24000; index < 24016; index++ {
		memory[index] = 0x7c
	}
	memory[8192+compressed-1] ^= 1
	status, written = invokePair("decompress_proxy", 8192, uint64(compressed), 24000, uint64(len(plain)))
	if status != StatusChecksumMismatch || written != 0 || !bytes.Equal(memory[24000:24016], bytes.Repeat([]byte{0x7c}, 16)) {
		t.Fatalf("checksum failure = %d/%v, destination changed=%v", written, status, !bytes.Equal(memory[24000:24016], bytes.Repeat([]byte{0x7c}, 16)))
	}
	memory[8192+compressed-1] ^= 1
	for index := 20000; index < 20016; index++ {
		memory[index] = 0x5a
	}
	status, written = invokePair("decompress_proxy", 8192, uint64(compressed), 20000, 1)
	if status != StatusOutputTooSmall || written != 0 || !bytes.Equal(memory[20000:20016], bytes.Repeat([]byte{0x5a}, 16)) {
		t.Fatalf("short output = %d/%v, destination changed=%v", written, status, !bytes.Equal(memory[20000:20016], bytes.Repeat([]byte{0x5a}, 16)))
	}

	copy(memory[64:], "junk")
	status, written = invokePair("decompress_proxy", 64, 4, 20000, 16)
	if status != StatusInvalidData || written != 0 {
		t.Fatalf("invalid data = %d/%v", written, status)
	}

	for index := 22000; index < 22016; index++ {
		memory[index] = 0x6b
	}
	if memory64 {
		status, written = invokePair("compress_proxy", 1<<32, 1, 22000, 16, 0)
	} else {
		status, written = invokePair("compress_proxy", uint64(len(memory)-2), 4, 22000, 16, 0)
	}
	if status != StatusInvalidArgument || written != 0 || !bytes.Equal(memory[22000:22016], bytes.Repeat([]byte{0x6b}, 16)) {
		t.Fatalf("range failure = %d/%v, destination changed=%v", written, status, !bytes.Equal(memory[22000:22016], bytes.Repeat([]byte{0x6b}, 16)))
	}
}
