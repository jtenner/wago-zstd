package zstd

import (
	"encoding/binary"
	"errors"
	"io"
	"math"

	kpzstd "github.com/klauspost/compress/zstd"
)

const maxZstdBlockSize = 128 << 10

type frameScan struct {
	decodedSize uint64
	knownSize   bool
	standard    int
	skippable   int
}

func scanFrames(input []byte) (frameScan, Status) {
	result := frameScan{knownSize: true}
	if len(input) == 0 {
		return result, StatusTruncated
	}

	for offset := 0; offset < len(input); {
		remaining := input[offset:]
		if len(remaining) < 4 {
			if result.standard+result.skippable > 0 && !magicPrefix(remaining) {
				return result, StatusTrailingData
			}
			return result, StatusTruncated
		}

		var header kpzstd.Header
		if err := header.Decode(remaining); err != nil {
			switch {
			case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
				return result, StatusTruncated
			case errors.Is(err, kpzstd.ErrMagicMismatch) && result.standard+result.skippable > 0:
				return result, StatusTrailingData
			default:
				return result, StatusInvalidData
			}
		}

		if header.Skippable {
			frameBytes := uint64(header.HeaderSize) + uint64(header.SkippableSize)
			if frameBytes > uint64(len(remaining)) {
				return result, StatusTruncated
			}
			offset += int(frameBytes)
			result.skippable++
			continue
		}
		if header.DictionaryID != 0 {
			return result, StatusDictionaryRequired
		}
		if header.HasFCS {
			if header.FrameContentSize > math.MaxUint64-result.decodedSize {
				return result, StatusOutputTooLarge
			}
			result.decodedSize += header.FrameContentSize
		} else {
			result.knownSize = false
		}

		cursor := header.HeaderSize
		for {
			if len(remaining)-cursor < 3 {
				return result, StatusTruncated
			}
			block := uint32(remaining[cursor]) | uint32(remaining[cursor+1])<<8 | uint32(remaining[cursor+2])<<16
			cursor += 3
			last := block&1 != 0
			blockType := (block >> 1) & 3
			blockSize := uint64(block >> 3)
			if blockType == 3 || blockSize > maxZstdBlockSize {
				return result, StatusInvalidData
			}
			payloadSize := blockSize
			if blockType == 1 { // RLE carries one encoded byte.
				payloadSize = 1
			}
			if payloadSize > uint64(len(remaining)-cursor) {
				return result, StatusTruncated
			}
			cursor += int(payloadSize)
			if last {
				break
			}
		}
		if header.HasCheckSum {
			if len(remaining)-cursor < 4 {
				return result, StatusTruncated
			}
			cursor += 4
		}
		offset += cursor
		result.standard++
	}
	return result, StatusOK
}

func magicPrefix(input []byte) bool {
	standard := []byte{0x28, 0xb5, 0x2f, 0xfd}
	if len(input) <= len(standard) && string(input) == string(standard[:len(input)]) {
		return true
	}
	if len(input) == 0 {
		return true
	}
	// All skippable magic values are 0x184d2a50 through 0x184d2a5f in
	// little-endian byte order.
	skippable := []byte{0x50, 0x2a, 0x4d, 0x18}
	if input[0]&0xf0 != 0x50 {
		return false
	}
	if len(input) == 1 {
		return true
	}
	return len(input) <= len(skippable) && string(input[1:]) == string(skippable[1:len(input)])
}

func skippableFrame(payload []byte) []byte {
	out := make([]byte, 8+len(payload))
	binary.LittleEndian.PutUint32(out, 0x184d2a50)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(payload)))
	copy(out[8:], payload)
	return out
}
