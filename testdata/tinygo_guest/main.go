//go:build tinygo

package main

import "unsafe"

const (
	statusOK               = uint32(0)
	statusInvalidArgument  = uint32(1)
	statusInputTooLarge    = uint32(2)
	statusOutputTooSmall   = uint32(4)
	statusInvalidData      = uint32(5)
	statusTruncated        = uint32(6)
	statusChecksumMismatch = uint32(7)
	testMaxInputBytes      = uint32(1 << 20)
	compressedBufferBytes  = 2048
	outputBufferBytes      = 512
	failureSentinel        = byte(0xa5)
)

var (
	source     [outputBufferBytes]byte
	compressed [compressedBufferBytes]byte
	output     [outputBufferBytes]byte
	overlap    [compressedBufferBytes]byte
)

// TinyGo lowers these uint32 parameters to WebAssembly i32 and the uint64
// result to i64. The packed result avoids a multi-value import, which keeps the
// guest declaration usable across TinyGo's wasm-unknown target.
//
//go:wasmimport wago_zstd.wasm32 abi_version
func abiVersion() int32

//go:wasmimport wago_zstd.wasm32 compress_packed
func compressPacked(srcPtr, srcLen, dstPtr, dstCap uint32, level int32) uint64

//go:wasmimport wago_zstd.wasm32 decompress_packed
func decompressPacked(srcPtr, srcLen, dstPtr, dstCap uint32) uint64

func pointer(buffer *byte) uint32 {
	return uint32(uintptr(unsafe.Pointer(buffer)))
}

func resultStatus(result uint64) uint32  { return uint32(result) }
func resultWritten(result uint64) uint32 { return uint32(result >> 32) }

func fill(buffer []byte, value byte) {
	for index := range buffer {
		buffer[index] = value
	}
}

func allEqual(buffer []byte, value byte) bool {
	for _, got := range buffer {
		if got != value {
			return false
		}
	}
	return true
}

func bytesEqual(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func copyString(destination []byte, value string) int {
	for index := 0; index < len(value); index++ {
		destination[index] = value[index]
	}
	return len(value)
}

func expectFailure(result uint64, status uint32, destination []byte) bool {
	return resultStatus(result) == status && resultWritten(result) == 0 && allEqual(destination, failureSentinel)
}

func compressFixture(messageLength int) (uint32, bool) {
	fill(compressed[:], 0)
	result := compressPacked(
		pointer(&source[0]), uint32(messageLength),
		pointer(&compressed[0]), uint32(len(compressed)), 0,
	)
	written := resultWritten(result)
	return written, resultStatus(result) == statusOK && written != 0 && written <= uint32(len(compressed))
}

//go:wasmexport run
func run() int32 {
	if abiVersion() != 1 {
		return -1
	}

	const message = "TinyGo guest to Wago Zstandard packed ABI"
	fill(source[:], 0)
	messageLength := copyString(source[:], message)
	compressedLength, ok := compressFixture(messageLength)
	if !ok {
		return -2
	}

	// Ordinary packed round-trip through the real codec.
	fill(output[:], failureSentinel)
	result := decompressPacked(
		pointer(&compressed[0]), compressedLength,
		pointer(&output[0]), uint32(len(output)),
	)
	if resultStatus(result) != statusOK || resultWritten(result) != uint32(messageLength) ||
		!bytesEqual(output[:messageLength], source[:messageLength]) {
		return -3
	}

	// Empty input still emits a real frame and round-trips to zero bytes.
	emptyResult := compressPacked(
		pointer(&source[0]), 0,
		pointer(&compressed[0]), uint32(len(compressed)), 0,
	)
	if resultStatus(emptyResult) != statusOK || resultWritten(emptyResult) == 0 {
		return -4
	}
	fill(output[:], failureSentinel)
	result = decompressPacked(
		pointer(&compressed[0]), resultWritten(emptyResult),
		pointer(&output[0]), uint32(len(output)),
	)
	if resultStatus(result) != statusOK || resultWritten(result) != 0 || !allEqual(output[:], failureSentinel) {
		return -5
	}

	// Exact source/destination overlap is safe because host output is staged.
	fill(overlap[:], 0)
	copyString(overlap[:], message)
	result = compressPacked(
		pointer(&overlap[0]), uint32(messageLength),
		pointer(&overlap[1]), uint32(len(overlap)-1), 0,
	)
	if resultStatus(result) != statusOK || resultWritten(result) == 0 {
		return -6
	}
	overlapCompressed := resultWritten(result)
	result = decompressPacked(
		pointer(&overlap[1]), overlapCompressed,
		pointer(&overlap[0]), uint32(messageLength),
	)
	if resultStatus(result) != statusOK || resultWritten(result) != uint32(messageLength) ||
		!bytesEqual(overlap[:messageLength], source[:messageLength]) {
		return -7
	}

	compressedLength, ok = compressFixture(messageLength)
	if !ok {
		return -8
	}

	// Short output, bounds, checksum, truncation, invalid data, and level errors
	// must all preserve the destination sentinel and return written == 0.
	fill(output[:], failureSentinel)
	result = decompressPacked(
		pointer(&compressed[0]), compressedLength,
		pointer(&output[0]), uint32(messageLength-1),
	)
	if !expectFailure(result, statusOutputTooSmall, output[:]) {
		return -9
	}

	fill(output[:], failureSentinel)
	result = compressPacked(
		pointer(&source[0]), testMaxInputBytes+1,
		pointer(&output[0]), uint32(len(output)), 0,
	)
	if !expectFailure(result, statusInputTooLarge, output[:]) {
		return -10
	}

	compressed[compressedLength-1] ^= 1
	fill(output[:], failureSentinel)
	result = decompressPacked(
		pointer(&compressed[0]), compressedLength,
		pointer(&output[0]), uint32(len(output)),
	)
	if !expectFailure(result, statusChecksumMismatch, output[:]) {
		return -11
	}
	compressed[compressedLength-1] ^= 1

	fill(output[:], failureSentinel)
	result = decompressPacked(
		pointer(&compressed[0]), compressedLength-1,
		pointer(&output[0]), uint32(len(output)),
	)
	if !expectFailure(result, statusTruncated, output[:]) {
		return -12
	}

	fill(output[:], failureSentinel)
	output[0] = 'j'
	output[1] = 'u'
	output[2] = 'n'
	output[3] = 'k'
	fill(compressed[:], failureSentinel)
	result = decompressPacked(
		pointer(&output[0]), 4,
		pointer(&compressed[0]), 16,
	)
	if !expectFailure(result, statusInvalidData, compressed[:]) {
		return -13
	}

	fill(output[:], failureSentinel)
	result = compressPacked(
		pointer(&source[0]), uint32(messageLength),
		pointer(&output[0]), uint32(len(output)), 23,
	)
	if !expectFailure(result, statusInvalidArgument, output[:]) {
		return -14
	}

	return 0
}

func main() {}
