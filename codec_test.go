package zstd

import (
	"bytes"
	"testing"

	kpzstd "github.com/klauspost/compress/zstd"
)

func newCodecForTest(t *testing.T, config Config) *Codec {
	t.Helper()
	codec, err := NewCodec(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(codec.Close)
	return codec
}

func compressForTest(t *testing.T, codec *Codec, plain []byte, level int32) []byte {
	t.Helper()
	dst := make([]byte, codec.config.MaxOutputBytes)
	written, status := codec.Compress(plain, dst, level)
	if status != StatusOK {
		t.Fatalf("Compress status = %v", status)
	}
	return append([]byte(nil), dst[:written]...)
}

func TestEncoderProfileIsExplicitlyCoarse(t *testing.T) {
	tests := []struct {
		level int32
		want  kpzstd.EncoderLevel
	}{
		{-5, kpzstd.SpeedFastest},
		{-1, kpzstd.SpeedFastest},
		{0, kpzstd.SpeedDefault},
		{3, kpzstd.SpeedDefault},
		{6, kpzstd.SpeedBetterCompression},
		{10, kpzstd.SpeedBestCompression},
		{22, kpzstd.SpeedBestCompression},
	}
	for _, test := range tests {
		got, status := encoderProfile(test.level)
		if status != StatusOK || got != test.want {
			t.Errorf("level %d = %v/%v, want %v/%v", test.level, got, status, test.want, StatusOK)
		}
	}
	for _, level := range []int32{-6, 23} {
		if _, status := encoderProfile(level); status != StatusInvalidArgument {
			t.Errorf("level %d status = %v", level, status)
		}
	}
}

func TestRoundTripLevelsAndEmptyInput(t *testing.T) {
	codec := newCodecForTest(t, Config{})
	inputs := [][]byte{nil, []byte("hello, bounded zstandard"), bytes.Repeat([]byte("compressible-"), 4096)}
	for _, level := range []int32{-5, 0, 3, 6, 10, 22} {
		for _, input := range inputs {
			encoded := compressForTest(t, codec, input, level)
			if len(encoded) == 0 {
				t.Fatalf("level %d encoded empty input without a frame", level)
			}
			decoded := make([]byte, len(input))
			written, status := codec.Decompress(encoded, decoded)
			if status != StatusOK || written != len(input) || !bytes.Equal(decoded, input) {
				t.Fatalf("level %d round trip = %d/%v/%q", level, written, status, decoded)
			}
		}
	}
}

func TestTransactionalFailureAndOverlap(t *testing.T) {
	codec := newCodecForTest(t, Config{})
	plain := bytes.Repeat([]byte("overlap-safe"), 128)

	short := bytes.Repeat([]byte{0xa5}, 8)
	before := append([]byte(nil), short...)
	if written, status := codec.Compress(plain, short, 0); status != StatusOutputTooSmall || written != 0 || !bytes.Equal(short, before) {
		t.Fatalf("short compress = %d/%v, changed=%v", written, status, !bytes.Equal(short, before))
	}

	encoded := compressForTest(t, codec, plain, 0)
	short = bytes.Repeat([]byte{0x5a}, len(plain)-1)
	before = append([]byte(nil), short...)
	if written, status := codec.Decompress(encoded, short); status != StatusOutputTooSmall || written != 0 || !bytes.Equal(short, before) {
		t.Fatalf("short decompress = %d/%v, changed=%v", written, status, !bytes.Equal(short, before))
	}

	compressOverlap := make([]byte, 64<<10)
	copy(compressOverlap, plain)
	written, status := codec.Compress(compressOverlap[:len(plain)], compressOverlap[1:], 0)
	if status != StatusOK {
		t.Fatalf("overlap compress status = %v", status)
	}
	compressedCopy := append([]byte(nil), compressOverlap[1:1+written]...)

	decompressOverlap := make([]byte, 64<<10)
	copy(decompressOverlap[7:], compressedCopy)
	written, status = codec.Decompress(decompressOverlap[7:7+len(compressedCopy)], decompressOverlap)
	if status != StatusOK || written != len(plain) || !bytes.Equal(decompressOverlap[:written], plain) {
		t.Fatalf("overlap decompress = %d/%v", written, status)
	}
}

func TestFramePolicyAndErrorClassification(t *testing.T) {
	codec := newCodecForTest(t, Config{})
	firstPlain, secondPlain := []byte("first"), []byte("second")
	first := compressForTest(t, codec, firstPlain, 0)
	second := compressForTest(t, codec, secondPlain, 0)
	skip := skippableFrame([]byte("ignored metadata"))
	joined := bytes.Join([][]byte{skip, first, skip, second, skip}, nil)
	out := make([]byte, 64)
	written, status := codec.Decompress(joined, out)
	if status != StatusOK || !bytes.Equal(out[:written], append(firstPlain, secondPlain...)) {
		t.Fatalf("concatenated/skippable = %d/%v/%q", written, status, out[:written])
	}

	checks := []struct {
		name   string
		input  []byte
		status Status
	}{
		{"empty compressed", nil, StatusTruncated},
		{"truncated", first[:len(first)-1], StatusTruncated},
		{"trailing", append(append([]byte{}, first...), []byte("junk")...), StatusTrailingData},
		{"invalid", []byte("not zstd"), StatusInvalidData},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			dst := bytes.Repeat([]byte{0xcc}, 64)
			before := append([]byte(nil), dst...)
			written, status := codec.Decompress(check.input, dst)
			if written != 0 || status != check.status || !bytes.Equal(dst, before) {
				t.Fatalf("result = %d/%v changed=%v, want 0/%v/false", written, status, !bytes.Equal(dst, before), check.status)
			}
		})
	}

	corrupt := append([]byte(nil), first...)
	corrupt[len(corrupt)-1] ^= 1
	if written, status := codec.Decompress(corrupt, out); written != 0 || status != StatusChecksumMismatch {
		t.Fatalf("checksum = %d/%v", written, status)
	}
}

func TestDictionaryAndWindowPolicy(t *testing.T) {
	codec := newCodecForTest(t, Config{})
	dictionary := []byte("dictionary material used by the encoder")
	dictFrame := encodedForTest(t, []byte("dictionary-backed payload"), kpzstd.WithEncoderDictRaw(42, dictionary))
	if written, status := codec.Decompress(dictFrame, make([]byte, 1024)); written != 0 || status != StatusDictionaryRequired {
		t.Fatalf("dictionary = %d/%v", written, status)
	}

	// One valid empty frame that explicitly requests a 16 MiB window:
	// magic, frame descriptor, window descriptor, final empty raw block.
	windowFrame := []byte{0x28, 0xb5, 0x2f, 0xfd, 0x00, 0x70, 0x01, 0x00, 0x00}
	if written, status := codec.Decompress(windowFrame, make([]byte, 64<<10)); written != 0 || status != StatusOutputTooLarge {
		t.Fatalf("window = %d/%v", written, status)
	}
}

func TestUnknownContentSizeStillHonorsDestinationCap(t *testing.T) {
	codec := newCodecForTest(t, Config{})
	plain := bytes.Repeat([]byte("streamed"), 1024)
	var compressed bytes.Buffer
	encoder, err := kpzstd.NewWriter(&compressed, kpzstd.WithEncoderConcurrency(1), kpzstd.WithEncoderCRC(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := encoder.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	dst := bytes.Repeat([]byte{0xee}, len(plain)-1)
	before := append([]byte(nil), dst...)
	written, status := codec.Decompress(compressed.Bytes(), dst)
	if written != 0 || status != StatusOutputTooSmall || !bytes.Equal(dst, before) {
		t.Fatalf("unknown-size short output = %d/%v changed=%v", written, status, !bytes.Equal(dst, before))
	}
}

func TestLimitsBusyRetainedScratchAndClose(t *testing.T) {
	codec := newCodecForTest(t, Config{
		MaxInputBytes:         4 << 20,
		MaxOutputBytes:        4 << 20,
		MaxWindowBytes:        1 << 20,
		MaxDecoderMemoryBytes: 4 << 20,
		MaxConcurrent:         1,
	})
	if written, status := codec.Compress(make([]byte, 4<<20+1), make([]byte, 4<<20), 0); written != 0 || status != StatusInputTooLarge {
		t.Fatalf("input limit = %d/%v", written, status)
	}
	if written, status := codec.Compress(nil, make([]byte, 4<<20+1), 0); written != 0 || status != StatusOutputTooLarge {
		t.Fatalf("output request limit = %d/%v", written, status)
	}

	held, status := codec.acquire()
	if status != StatusOK {
		t.Fatal(status)
	}
	if written, status := codec.Compress([]byte("busy"), make([]byte, 128), 0); written != 0 || status != StatusBusy {
		t.Fatalf("busy = %d/%v", written, status)
	}
	if written, status := codec.Decompress([]byte("invalid but bounded"), make([]byte, 128)); written != 0 || status != StatusBusy {
		t.Fatalf("busy decompress = %d/%v", written, status)
	}
	codec.release(held)

	large := bytes.Repeat([]byte("scratch"), 300<<10)
	_ = compressForTest(t, codec, large, 0)
	worker, status := codec.acquire()
	if status != StatusOK {
		t.Fatal(status)
	}
	if cap(worker.scratch) > maxRetainedScratchBytes {
		t.Fatalf("retained scratch = %d", cap(worker.scratch))
	}
	codec.release(worker)

	codec.Close()
	if written, status := codec.Compress([]byte("closed"), make([]byte, 128), 0); written != 0 || status != StatusInternalError {
		t.Fatalf("closed = %d/%v", written, status)
	}
}

func TestCloseLetsAcquiredWorkerFinishAndCleansItOnRelease(t *testing.T) {
	codec := newCodecForTest(t, Config{MaxConcurrent: 1})
	worker, status := codec.acquire()
	if status != StatusOK {
		t.Fatal(status)
	}
	codec.Close()

	profile, status := encoderProfile(0)
	if status != StatusOK {
		t.Fatal(status)
	}
	dst := make([]byte, 1024)
	written, status := codec.compressWithWorker(worker, []byte("already admitted"), dst, profile)
	if status != StatusOK || written == 0 {
		t.Fatalf("active call after close = %d/%v", written, status)
	}
	codec.release(worker)
	if worker.decoder != nil || worker.encoder != nil || worker.scratch != nil {
		t.Fatalf("released worker retained state: decoder=%p encoder=%p scratch=%d", worker.decoder, worker.encoder, cap(worker.scratch))
	}
	if written, status := codec.Compress(nil, dst, 0); written != 0 || status != StatusInternalError {
		t.Fatalf("new call after close = %d/%v", written, status)
	}
}

func BenchmarkCodecRoundTrip64KiB(b *testing.B) {
	codec, err := NewCodec(Config{MaxConcurrent: 1})
	if err != nil {
		b.Fatal(err)
	}
	defer codec.Close()
	plain := bytes.Repeat([]byte("benchmark-zstd-"), (64<<10)/15)
	compressed := make([]byte, DefaultMaxOutputBytes)
	encoded, status := codec.Compress(plain, compressed, 0)
	if status != StatusOK {
		b.Fatal(status)
	}
	output := make([]byte, len(plain))
	b.ReportAllocs()
	b.SetBytes(int64(len(plain)))
	b.ResetTimer()
	for range b.N {
		if _, status := codec.Compress(plain, compressed, 0); status != StatusOK {
			b.Fatal(status)
		}
		if _, status := codec.Decompress(compressed[:encoded], output); status != StatusOK {
			b.Fatal(status)
		}
	}
}
