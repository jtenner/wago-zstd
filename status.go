package zstd

import "fmt"

// Status is the stable ABI result code returned by compress and decompress.
// A successful call reports StatusOK and a non-negative written count.
type Status int32

const (
	StatusOK Status = iota
	StatusInvalidArgument
	StatusInputTooLarge
	StatusOutputTooLarge
	StatusOutputTooSmall
	StatusInvalidData
	StatusTruncated
	StatusChecksumMismatch
	StatusDictionaryRequired
	StatusTrailingData
	StatusBusy
	StatusUnsupported
	StatusInternalError
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusInvalidArgument:
		return "invalid argument"
	case StatusInputTooLarge:
		return "input too large"
	case StatusOutputTooLarge:
		return "output too large"
	case StatusOutputTooSmall:
		return "output too small"
	case StatusInvalidData:
		return "invalid data"
	case StatusTruncated:
		return "truncated"
	case StatusChecksumMismatch:
		return "checksum mismatch"
	case StatusDictionaryRequired:
		return "dictionary required"
	case StatusTrailingData:
		return "trailing data"
	case StatusBusy:
		return "busy"
	case StatusUnsupported:
		return "unsupported"
	case StatusInternalError:
		return "internal error"
	default:
		return fmt.Sprintf("status(%d)", int32(s))
	}
}
