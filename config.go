package zstd

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	DefaultMaxInputBytes         uint64 = 8 << 20
	DefaultMaxOutputBytes        uint64 = 32 << 20
	DefaultMaxWindowBytes        uint64 = 8 << 20
	DefaultMaxDecoderMemoryBytes uint64 = 32 << 20
	DefaultMaxConcurrent         uint32 = 2

	HardMaxInputBytes         uint64 = 64 << 20
	HardMaxOutputBytes        uint64 = 64 << 20
	HardMaxWindowBytes        uint64 = 64 << 20
	HardMaxDecoderMemoryBytes uint64 = 64 << 20
	HardMaxConcurrent         uint32 = 8

	// HardProviderAdmissionBytes caps the conservative configured envelope for
	// all simultaneously active calls. The envelope includes a possible copied
	// GC input, transactional output, decoder memory, and an eight-window codec
	// allowance. It is an admission bound, not a Go heap reservation or promise.
	HardProviderAdmissionBytes uint64 = 1 << 30

	maxRetainedScratchBytes         = 1 << 20
	codecWindowAllowanceMultiplier  = 8
	fixedEncoderStateAllowanceBytes = 40 << 20
)

//go:embed config.schema.json
var configSchema []byte

// Config defines finite resource limits for one activated plugin.
// Zero fields select conservative defaults.
type Config struct {
	MaxInputBytes         uint64 `json:"max_input_bytes,omitempty"`
	MaxOutputBytes        uint64 `json:"max_output_bytes,omitempty"`
	MaxWindowBytes        uint64 `json:"max_window_bytes,omitempty"`
	MaxDecoderMemoryBytes uint64 `json:"max_decoder_memory_bytes,omitempty"`
	MaxConcurrent         uint32 `json:"max_concurrent,omitempty"`
}

func (c Config) normalized() (Config, error) {
	if c.MaxInputBytes == 0 {
		c.MaxInputBytes = DefaultMaxInputBytes
	}
	if c.MaxOutputBytes == 0 {
		c.MaxOutputBytes = DefaultMaxOutputBytes
	}
	if c.MaxWindowBytes == 0 {
		c.MaxWindowBytes = DefaultMaxWindowBytes
	}
	if c.MaxDecoderMemoryBytes == 0 {
		c.MaxDecoderMemoryBytes = DefaultMaxDecoderMemoryBytes
	}
	if c.MaxConcurrent == 0 {
		c.MaxConcurrent = DefaultMaxConcurrent
	}

	if c.MaxInputBytes > HardMaxInputBytes {
		return Config{}, fmt.Errorf("max_input_bytes exceeds %d", HardMaxInputBytes)
	}
	if c.MaxOutputBytes > HardMaxOutputBytes {
		return Config{}, fmt.Errorf("max_output_bytes exceeds %d", HardMaxOutputBytes)
	}
	if c.MaxWindowBytes < 1<<10 || c.MaxWindowBytes > HardMaxWindowBytes || c.MaxWindowBytes&(c.MaxWindowBytes-1) != 0 {
		return Config{}, fmt.Errorf("max_window_bytes must be a power of two from 1024 through %d", HardMaxWindowBytes)
	}
	if c.MaxDecoderMemoryBytes < 1<<10 || c.MaxDecoderMemoryBytes > HardMaxDecoderMemoryBytes {
		return Config{}, fmt.Errorf("max_decoder_memory_bytes must be from 1024 through %d", HardMaxDecoderMemoryBytes)
	}
	if c.MaxDecoderMemoryBytes < c.MaxOutputBytes {
		return Config{}, errors.New("max_decoder_memory_bytes must be at least max_output_bytes")
	}
	if c.MaxDecoderMemoryBytes < c.MaxWindowBytes {
		return Config{}, errors.New("max_decoder_memory_bytes must be at least max_window_bytes")
	}
	if c.MaxConcurrent > HardMaxConcurrent {
		return Config{}, fmt.Errorf("max_concurrent exceeds %d", HardMaxConcurrent)
	}

	perCall := configuredPerCallEnvelope(c)
	if perCall > HardProviderAdmissionBytes/uint64(c.MaxConcurrent) {
		return Config{}, fmt.Errorf("configured concurrent resource envelope %d exceeds provider admission bound %d", perCall*uint64(c.MaxConcurrent), HardProviderAdmissionBytes)
	}
	return c, nil
}

func configuredPerCallEnvelope(c Config) uint64 {
	return c.MaxInputBytes + c.MaxOutputBytes + c.MaxDecoderMemoryBytes +
		codecWindowAllowanceMultiplier*c.MaxWindowBytes + fixedEncoderStateAllowanceBytes
}

func decodeConfig(raw json.RawMessage) (Config, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var config Config
	decoder := json.NewDecoder(bytesReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Config{}, errors.New("trailing JSON value")
		}
		return Config{}, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return Config{}, err
	}
	if fields == nil {
		return Config{}, errors.New("config must be a JSON object")
	}
	for _, field := range []string{"max_input_bytes", "max_output_bytes", "max_window_bytes", "max_decoder_memory_bytes", "max_concurrent"} {
		value, present := fields[field]
		if !present {
			continue
		}
		var number uint64
		if err := json.Unmarshal(value, &number); err != nil {
			return Config{}, fmt.Errorf("%s: %w", field, err)
		}
		if number == 0 {
			return Config{}, fmt.Errorf("%s must be at least 1 when provided", field)
		}
	}
	return config.normalized()
}

// bytesReader is kept small so config validation does not pull a second JSON
// representation into memory.
func bytesReader(raw []byte) *sliceReader { return &sliceReader{remaining: raw} }

type sliceReader struct{ remaining []byte }

func (r *sliceReader) Read(p []byte) (int, error) {
	if len(r.remaining) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.remaining)
	r.remaining = r.remaining[n:]
	return n, nil
}
