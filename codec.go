package zstd

import (
	"errors"
	"io"
	"sync"

	kpzstd "github.com/klauspost/compress/zstd"
)

type codecWorker struct {
	decoder      *kpzstd.Decoder
	encoder      *kpzstd.Encoder
	encoderLevel kpzstd.EncoderLevel
	scratch      []byte
}

func (w *codecWorker) close() {
	if w.decoder != nil {
		w.decoder.Close()
		w.decoder = nil
	}
	if w.encoder != nil {
		_ = w.encoder.Close()
		w.encoder = nil
	}
	w.scratch = nil
}

func (w *codecWorker) scratchBuffer(size int) []byte {
	if cap(w.scratch) < size {
		w.scratch = make([]byte, 0, size)
	}
	return w.scratch[:0:size]
}

func (w *codecWorker) trimScratch() {
	if cap(w.scratch) > maxRetainedScratchBytes {
		w.scratch = nil
		return
	}
	w.scratch = w.scratch[:0]
}

// Codec is a bounded pool of reusable Zstandard encoder/decoder contexts.
// Calls never block waiting for a context; excess simultaneous calls return
// StatusBusy. Codec is safe for concurrent use.
type Codec struct {
	config  Config
	mu      sync.Mutex
	workers chan *codecWorker
	closed  bool
}

func NewCodec(config Config) (*Codec, error) {
	config, err := config.normalized()
	if err != nil {
		return nil, err
	}
	workers := make(chan *codecWorker, config.MaxConcurrent)
	for range config.MaxConcurrent {
		decoder, err := kpzstd.NewReader(nil,
			kpzstd.WithDecoderConcurrency(1),
			kpzstd.WithDecoderLowmem(true),
			kpzstd.WithDecoderMaxMemory(config.MaxDecoderMemoryBytes),
			kpzstd.WithDecoderMaxWindow(config.MaxWindowBytes),
			kpzstd.WithDecodeAllCapLimit(true),
		)
		if err != nil {
			for len(workers) != 0 {
				(<-workers).close()
			}
			return nil, err
		}
		workers <- &codecWorker{decoder: decoder}
	}
	return &Codec{config: config, workers: workers}, nil
}

func (c *Codec) Config() Config { return c.config }

func (c *Codec) acquire() (*codecWorker, Status) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, StatusInternalError
	}
	select {
	case worker := <-c.workers:
		return worker, StatusOK
	default:
		return nil, StatusBusy
	}
}

func (c *Codec) release(worker *codecWorker) {
	worker.trimScratch()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		worker.close()
		return
	}
	c.workers <- worker
}

// Close releases idle codec state immediately. A context in an active call is
// released when that call returns. Close is idempotent.
func (c *Codec) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	for len(c.workers) != 0 {
		(<-c.workers).close()
	}
}

func encoderProfile(level int32) (kpzstd.EncoderLevel, Status) {
	if level < -5 || level > 22 {
		return 0, StatusInvalidArgument
	}
	if level == 0 {
		return kpzstd.SpeedDefault, StatusOK
	}
	return kpzstd.EncoderLevelFromZstd(int(level)), StatusOK
}

func (c *Codec) encoder(worker *codecWorker, level kpzstd.EncoderLevel) (*kpzstd.Encoder, error) {
	if worker.encoder != nil && worker.encoderLevel == level {
		return worker.encoder, nil
	}
	if worker.encoder != nil {
		_ = worker.encoder.Close()
		worker.encoder = nil
	}
	encoder, err := kpzstd.NewWriter(nil,
		kpzstd.WithEncoderConcurrency(1),
		kpzstd.WithConcurrentBlocks(false),
		kpzstd.WithEncoderLevel(level),
		kpzstd.WithWindowSize(int(c.config.MaxWindowBytes)),
		kpzstd.WithLowerEncoderMem(true),
		kpzstd.WithEncoderCRC(true),
		kpzstd.WithZeroFrames(true),
	)
	if err != nil {
		return nil, err
	}
	worker.encoder = encoder
	worker.encoderLevel = level
	return encoder, nil
}

// Compress encodes one complete Zstandard frame. On failure written is zero
// and dst is unchanged. src and dst may overlap.
func (c *Codec) Compress(src, dst []byte, level int32) (written int, status Status) {
	if c == nil {
		return 0, StatusInternalError
	}
	if uint64(len(src)) > c.config.MaxInputBytes {
		return 0, StatusInputTooLarge
	}
	if uint64(len(dst)) > c.config.MaxOutputBytes {
		return 0, StatusOutputTooLarge
	}
	profile, status := encoderProfile(level)
	if status != StatusOK {
		return 0, status
	}
	worker, status := c.acquire()
	if status != StatusOK {
		return 0, status
	}
	defer c.release(worker)
	return c.compressWithWorker(worker, src, dst, profile)
}

func (c *Codec) compressWithWorker(worker *codecWorker, src, dst []byte, profile kpzstd.EncoderLevel) (written int, status Status) {
	encoder, err := c.encoder(worker, profile)
	if err != nil {
		return 0, StatusInternalError
	}
	bound := encoder.MaxEncodedSize(len(src))
	if bound < 0 || uint64(bound) > c.config.MaxOutputBytes {
		return 0, StatusOutputTooLarge
	}
	encoded := encoder.EncodeAll(src, worker.scratchBuffer(bound))
	if uint64(len(encoded)) > c.config.MaxOutputBytes {
		return 0, StatusOutputTooLarge
	}
	if len(encoded) > len(dst) {
		return 0, StatusOutputTooSmall
	}
	copy(dst, encoded)
	return len(encoded), StatusOK
}

// Decompress decodes a complete sequence of Zstandard and skippable frames.
// On failure written is zero and dst is unchanged. src and dst may overlap.
func (c *Codec) Decompress(src, dst []byte) (written int, status Status) {
	if c == nil {
		return 0, StatusInternalError
	}
	if uint64(len(src)) > c.config.MaxInputBytes {
		return 0, StatusInputTooLarge
	}
	if uint64(len(dst)) > c.config.MaxOutputBytes {
		return 0, StatusOutputTooLarge
	}
	worker, status := c.acquire()
	if status != StatusOK {
		return 0, status
	}
	defer c.release(worker)
	return c.decompressWithWorker(worker, src, dst)
}

func (c *Codec) decompressWithWorker(worker *codecWorker, src, dst []byte) (written int, status Status) {
	scan, status := scanFrames(src)
	if status != StatusOK {
		return 0, status
	}
	if scan.knownSize {
		if scan.decodedSize > c.config.MaxOutputBytes {
			return 0, StatusOutputTooLarge
		}
		if scan.decodedSize > uint64(len(dst)) {
			return 0, StatusOutputTooSmall
		}
	}

	// Unknown-size frames decode against the configured output ceiling rather
	// than the guest destination. This preserves a reliable distinction between
	// OUTPUT_TOO_SMALL and OUTPUT_TOO_LARGE without allowing DecodeAll to grow
	// past the explicit cap.
	capacity := int(c.config.MaxOutputBytes)
	if scan.knownSize {
		capacity = int(scan.decodedSize)
	}
	decoded, err := worker.decoder.DecodeAll(src, worker.scratchBuffer(capacity))
	if err != nil {
		return 0, c.decodeStatus(err, capacity)
	}
	if len(decoded) > len(dst) {
		return 0, StatusOutputTooSmall
	}
	copy(dst, decoded)
	return len(decoded), StatusOK
}

func (c *Codec) decodeStatus(err error, capacity int) Status {
	switch {
	case errors.Is(err, kpzstd.ErrCRCMismatch):
		return StatusChecksumMismatch
	case errors.Is(err, kpzstd.ErrUnknownDictionary):
		return StatusDictionaryRequired
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return StatusTruncated
	case errors.Is(err, kpzstd.ErrDecoderSizeExceeded):
		if uint64(capacity) < c.config.MaxOutputBytes {
			return StatusOutputTooSmall
		}
		return StatusOutputTooLarge
	case errors.Is(err, kpzstd.ErrWindowSizeExceeded):
		return StatusOutputTooLarge
	default:
		return StatusInvalidData
	}
}
