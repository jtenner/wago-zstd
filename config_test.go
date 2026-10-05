package zstd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConfigDefaultsAndBounds(t *testing.T) {
	got, err := (Config{}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		MaxInputBytes:         DefaultMaxInputBytes,
		MaxOutputBytes:        DefaultMaxOutputBytes,
		MaxWindowBytes:        DefaultMaxWindowBytes,
		MaxDecoderMemoryBytes: DefaultMaxDecoderMemoryBytes,
		MaxConcurrent:         DefaultMaxConcurrent,
	}
	if got != want {
		t.Fatalf("defaults = %+v, want %+v", got, want)
	}
	wantEnvelope := got.MaxInputBytes + got.MaxOutputBytes + got.MaxDecoderMemoryBytes +
		codecWindowAllowanceMultiplier*got.MaxWindowBytes + fixedEncoderStateAllowanceBytes
	if envelope := configuredPerCallEnvelope(got); envelope != wantEnvelope {
		t.Fatalf("per-call envelope = %d, want %d", envelope, wantEnvelope)
	}

	tests := []struct {
		name   string
		config Config
	}{
		{"input", Config{MaxInputBytes: HardMaxInputBytes + 1}},
		{"output", Config{MaxOutputBytes: HardMaxOutputBytes + 1, MaxDecoderMemoryBytes: HardMaxOutputBytes + 1}},
		{"window minimum", Config{MaxWindowBytes: 512}},
		{"window power", Config{MaxWindowBytes: 3 << 20}},
		{"decoder memory output", Config{MaxOutputBytes: 4 << 20, MaxDecoderMemoryBytes: 2 << 20}},
		{"decoder memory window", Config{MaxOutputBytes: 1 << 20, MaxWindowBytes: 4 << 20, MaxDecoderMemoryBytes: 2 << 20}},
		{"concurrency", Config{MaxConcurrent: HardMaxConcurrent + 1}},
		{"aggregate envelope", Config{MaxInputBytes: 64 << 20, MaxOutputBytes: 64 << 20, MaxWindowBytes: 64 << 20, MaxDecoderMemoryBytes: 64 << 20, MaxConcurrent: 2}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.config.normalized(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestDecodeConfigStrict(t *testing.T) {
	for _, raw := range []string{
		`{"unknown":1}`,
		`{"max_input_bytes":1024} {}`,
		`{"max_window_bytes":1536}`,
		`{"max_input_bytes":0}`,
		`{"max_concurrent":null}`,
		`null`,
	} {
		if _, err := decodeConfig(json.RawMessage(raw)); err == nil {
			t.Fatalf("decodeConfig(%q) succeeded", raw)
		}
	}
	got, err := decodeConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxInputBytes != DefaultMaxInputBytes {
		t.Fatalf("default input = %d", got.MaxInputBytes)
	}
}

func TestConfigSchemaMentionsEveryField(t *testing.T) {
	for _, field := range []string{"max_input_bytes", "max_output_bytes", "max_window_bytes", "max_decoder_memory_bytes", "max_concurrent"} {
		if !strings.Contains(string(configSchema), `"`+field+`"`) {
			t.Errorf("schema does not mention %s", field)
		}
	}
}
