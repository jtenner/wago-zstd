package zstd

import (
	"bytes"
	"testing"

	kpzstd "github.com/klauspost/compress/zstd"
)

func encodedForTest(t *testing.T, input []byte, options ...kpzstd.EOption) []byte {
	t.Helper()
	options = append([]kpzstd.EOption{
		kpzstd.WithEncoderConcurrency(1),
		kpzstd.WithEncoderCRC(true),
		kpzstd.WithZeroFrames(true),
	}, options...)
	encoder, err := kpzstd.NewWriter(nil, options...)
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	return encoder.EncodeAll(input, nil)
}

func TestScanFramesPolicies(t *testing.T) {
	first := encodedForTest(t, []byte("first"))
	second := encodedForTest(t, []byte("second"))
	skip := skippableFrame([]byte("metadata"))

	tests := []struct {
		name   string
		input  []byte
		status Status
		size   uint64
		known  bool
	}{
		{"single", first, StatusOK, 0, false},
		{"concatenated", append(append([]byte{}, first...), second...), StatusOK, 0, false},
		{"skippable only", skip, StatusOK, 0, true},
		{"skippable around frames", bytes.Join([][]byte{skip, first, skip, second, skip}, nil), StatusOK, 0, false},
		{"empty", nil, StatusTruncated, 0, true},
		{"truncated frame", first[:len(first)-1], StatusTruncated, 0, false},
		{"truncated next magic", append(append([]byte{}, first...), 0x28, 0xb5), StatusTruncated, 0, false},
		{"trailing byte", append(append([]byte{}, first...), 0x01), StatusTrailingData, 0, false},
		{"trailing word", append(append([]byte{}, first...), []byte("junk")...), StatusTrailingData, 0, false},
		{"wrong first magic", []byte("junk"), StatusInvalidData, 0, true},
		{"truncated skippable", skip[:len(skip)-1], StatusTruncated, 0, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scan, status := scanFrames(test.input)
			if status != test.status {
				t.Fatalf("status = %v, want %v", status, test.status)
			}
			if scan.decodedSize != test.size {
				t.Fatalf("decoded size = %d, want %d", scan.decodedSize, test.size)
			}
			if scan.knownSize != test.known {
				t.Fatalf("known size = %v, want %v", scan.knownSize, test.known)
			}
		})
	}
}

func TestScanFramesRejectsDictionary(t *testing.T) {
	encoded := encodedForTest(t, []byte("dictionary-backed payload"),
		kpzstd.WithEncoderDictRaw(42, []byte("dictionary material used by the encoder")))
	if _, status := scanFrames(encoded); status != StatusDictionaryRequired {
		t.Fatalf("status = %v, want %v", status, StatusDictionaryRequired)
	}
}
