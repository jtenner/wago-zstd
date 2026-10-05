# Wago Zstandard

`wago-zstd` is an experimental, bounded whole-buffer Zstandard plugin for the
[Wago](https://github.com/wago-org/wago) WebAssembly runtime. It delegates the
codec to maintained [`klauspost/compress/zstd`](https://github.com/klauspost/compress/tree/v1.20.1/zstd)
rather than implementing Zstandard in this repository.

The ABI is intentionally small: `abi_version`, legacy multi-value `compress`
and `decompress`, and additive `compress_packed` and `decompress_packed`
operations. It supports Wago's Wasm32, Memory64, and Wasm GC storage paths.
There are no dictionaries, dictionary training, streaming handles, or retained
guest pointers.

The project is licensed under Apache-2.0. The BSD-3-Clause terms of the
`klauspost/compress` dependency remain unchanged; see
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

The initial qualified host platform is Linux/amd64. Wasm32, Memory64, and Wasm
GC refer to guest storage transports, not additional native host-platform
qualification.

## Use from a Wago host

```go
set, err := zstd.PluginSet(zstd.Config{
    MaxInputBytes:         8 << 20,
    MaxOutputBytes:        32 << 20,
    MaxWindowBytes:        8 << 20,
    MaxDecoderMemoryBytes: 32 << 20,
    MaxConcurrent:         2,
})
if err != nil {
    return err
}

runtime := wago.NewRuntime()
defer runtime.Close()
if err := runtime.LoadPlugins(context.Background(), set); err != nil {
    return err
}
```

Generated hosts can add the side-effect-free catalog from
`github.com/jtenner/wago-zstd/register`. Importing it does not mutate global
state.

Native Go callers can use `NewCodec` directly with the same limits. `Codec` is
safe for concurrent use and returns `StatusBusy` instead of waiting when every
configured context is active.

## Guest ABI

All namespaces expose:

- `abi_version() -> i32`
- `compress(source, destination, level) -> (status: i32, written: i32)`
- `decompress(source, destination) -> (status: i32, written: i32)`
- `compress_packed(source, destination, level) -> i64`
- `decompress_packed(source, destination) -> i64`

Packed results place status in the low 32 bits and written in the high 32 bits;
failures always have a zero written half. They use the same parameters and
transaction semantics as the legacy calls. The concrete signatures and status
table are in [ABI.md](ABI.md).

Compression accepts requested Zstandard levels from `-5` through `22`, with
`0` meaning the plugin default. These are mapped to klauspost's four coarse
encoder presets:

| Requested level | klauspost preset |
|---:|---|
| `-5..2`, except `0` | fastest |
| `0`, `3..5` | default |
| `6..9` | better |
| `10..22` | best |

The mapping is deliberately coarse. It does not claim exact libzstd level,
ratio, or performance equivalence.

## Finite defaults and lifecycle

| Resource | Default | Per-field hard maximum |
|---|---:|---:|
| input | 8 MiB | 64 MiB |
| output | 32 MiB | 64 MiB |
| frame/encoder window | 8 MiB | 64 MiB |
| decoder memory | 32 MiB | 64 MiB |
| simultaneous calls | 2 | 8 |

The decoder is always created with `WithDecodeAllCapLimit(true)`,
`WithDecoderMaxMemory`, `WithDecoderMaxWindow`, low-memory mode, and internal
concurrency 1. Encoders use the configured window, lower-memory mode, checksum
frames, and internal concurrency 1. The plugin holds at most one decoder and
one most-recently-used encoder per operation slot. Transaction scratch above
1 MiB is dropped after each call.

Configuration also has a 1 GiB conservative provider admission envelope across
all simultaneous calls. A concurrency slot is acquired before guest storage is
borrowed, an immutable GC source is copied, or compressed input is scanned.
The calculation includes the entire backing byte size of a possibly copied
immutable GC input (`array<i32>` is four bytes per element), transactional
output, decoder memory, an eight-window codec allowance, and a fixed 40 MiB
allowance for the largest klauspost encoder tables plus headroom. The fixed
allowance accounts for the roughly 34 MiB short/long match tables used by the
best-compression preset in klauspost/compress v1.20.1. This is a configuration
admission bound, not a byte-exact Go heap limit or a security guarantee;
upstream and Go runtime bookkeeping are not reported as exact allocations.

Closing the Wago runtime closes every decoder, drops encoder references and
scratch buffers, and causes in-flight contexts to clean themselves up when they
return.

## Frame policy

- `compress` emits one complete frame. Non-empty frames include a checksum;
  klauspost's canonical empty frame does not add a checksum.
- `decompress` accepts concatenated standard frames and Zstandard skippable
  frames, including a skippable-only sequence with zero output.
- Unknown trailing bytes are rejected. An incomplete recognizable next-frame
  prefix is reported as truncated input.
- Frame checksums are validated before output is committed.
- Frames declaring a dictionary ID are rejected with
  `StatusDictionaryRequired`. No dictionary is registered implicitly.
- An empty compressed input is truncated input. An empty uncompressed input
  compresses to a real empty frame and round-trips successfully.
- Source and destination may overlap because output is staged privately.
- Every failure reports `written = 0` and leaves the requested destination
  range byte-for-byte unchanged.

The frame-envelope scanner only identifies complete frame boundaries and
policy metadata. klauspost remains responsible for Zstandard block decoding,
checksum validation, and codec correctness.

## Development

```sh
go test ./...
go test -race ./...
go vet ./...
```

### TinyGo guest and host qualification

The dedicated Linux/amd64 CI job first builds
[`testdata/tinygo_guest/main.go`](testdata/tinygo_guest/main.go) as a genuine
TinyGo Wasm32 guest. It executes that guest through the real packed imports in
both the ordinary Go Wago host and a TinyGo-built Wago host. The guest checks
round trips, empty input, overlap, short-output atomicity, configured bounds,
checksum failure, truncation, malformed input, invalid levels, zero failure
counts, and unchanged sentinel destinations.

The same job also executes WAT-authored Wasm32, Memory64, and WasmGC fixtures.
Those fixtures check legacy/packed parity and cover storage features TinyGo's
`wasm-unknown` guest target does not emit.

The pinned toolchain and flags are:

```sh
# TinyGo 0.42.0, Go 1.27.1, Linux/amd64
tinygo build -target=wasm-unknown -no-debug -opt=z \
  -o testdata/tinygo_guest.wasm ./testdata/tinygo_guest
go test -count=1 -v -run '^TestTinyGoGuestPackedABI$' .
tinygo test -v -scheduler=tasks -gc=conservative -opt=z -no-debug -p=2 \
  -tags=noasm -count=1 -run '^TestTinyGoHostIntegration$' .
```

TinyGo 0.42.0 and Go 1.27.1 are pinned for the guest build and host execution.
CI applies the exact upstream task-exit fix used by the pinned Wago dependency
before compiling with `-scheduler=tasks`. The `noasm` tag is required only when
compiling the host: it selects
klauspost's maintained pure-Go codec path because TinyGo cannot link the
dependency's Go-assembly entry points. This does not replace the codec or fork
its algorithm.

#### TinyGo guest output limits

The TinyGo target is `wasm-unknown`, so the guest uses the Wasm32 namespace and
unsigned 32-bit pointers. Its imported packed operations return a single `i64`;
the guest does not depend on multi-value import lowering. The checked-in test
guest intentionally has a 2 KiB compressed buffer and a 512-byte output buffer.
Those fixture capacities are separate from the host plugin configuration used
by the test (1 MiB input/output) and from the plugin's 64 MiB hard output cap.
A production guest must supply a destination buffer large enough for its data,
subject to both its Wasm32 linear-memory limits and the host's configured
`max_output_bytes`. This qualification does not claim TinyGo-generated
Memory64 or WasmGC guests.

The checked-in integration fixtures can be regenerated with `wasm-tools`:

```sh
wasm-tools parse testdata/wasm32.wat -o testdata/wasm32.wasm
wasm-tools parse testdata/wasm64.wat -o testdata/wasm64.wasm
wasm-tools parse testdata/gc.wat -o testdata/gc.wasm
```

Benchmarks are short local smoke measurements, not claims that this plugin is
the fastest implementation or appropriate for every workload.
