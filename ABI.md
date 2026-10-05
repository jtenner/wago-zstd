# Zstandard guest ABI v1

## Namespaces and signatures

Every operation returns `(status, written)`. `written` is nonzero only when
`status == 0`.

### `wago_zstd.wasm32`

```text
abi_version() -> i32
compress(src_ptr:i32, src_len:i32, dst_ptr:i32, dst_cap:i32, level:i32)
  -> (status:i32, written:i32)
decompress(src_ptr:i32, src_len:i32, dst_ptr:i32, dst_cap:i32)
  -> (status:i32, written:i32)
```

Pointers and lengths are interpreted as unsigned 32-bit values in first linear
memory. The memory must be Memory32.

### `wago_zstd.wasm64`

```text
abi_version() -> i32
compress(src_ptr:i64, src_len:i64, dst_ptr:i64, dst_cap:i64, level:i32)
  -> (status:i32, written:i32)
decompress(src_ptr:i64, src_len:i64, dst_ptr:i64, dst_cap:i64)
  -> (status:i32, written:i32)
```

Pointers and lengths are interpreted as unsigned 64-bit values in first linear
memory. The memory must be Memory64. Configuration limits keep a successful
`written` value within signed i32 range.

### `wago_zstd.gc`

```text
abi_version() -> i32
compress(src_ref:anyref, src_offset:i32, src_len:i32,
         dst_ref:anyref, dst_offset:i32, dst_cap:i32, level:i32)
  -> (status:i32, written:i32)
decompress(src_ref:anyref, src_offset:i32, src_len:i32,
           dst_ref:anyref, dst_offset:i32, dst_cap:i32)
  -> (status:i32, written:i32)
```

Offsets and lengths are unsigned byte counts. References must dynamically be
Wasm GC `array<i8>` or `array<i32>` values; `array<i32>` is viewed as its packed
raw bytes. The destination must be mutable. An immutable source is supported,
but Wago returns a detached byte view, so its complete backing array may not
exceed `max_input_bytes` even when the requested subrange is smaller.

All guest storage is borrowed only inside Wago's synchronous
`WithGuestStorage` callback. No slice, reference, pointer, or token survives the
callback.

## Status values

| Value | Name | Meaning |
|---:|---|---|
| 0 | `StatusOK` | Complete output was committed. |
| 1 | `StatusInvalidArgument` | Invalid range, storage type, mutability, level, or address width. |
| 2 | `StatusInputTooLarge` | Requested input exceeds `max_input_bytes`. |
| 3 | `StatusOutputTooLarge` | Requested/configured output, frame size, window, or decoder bound is exceeded. |
| 4 | `StatusOutputTooSmall` | Destination capacity cannot hold complete output. |
| 5 | `StatusInvalidData` | Malformed Zstandard data. |
| 6 | `StatusTruncated` | A frame, block, checksum, or recognizable next frame is incomplete. |
| 7 | `StatusChecksumMismatch` | A present frame checksum failed validation. |
| 8 | `StatusDictionaryRequired` | A frame declares an unsupported dictionary ID. |
| 9 | `StatusTrailingData` | Complete frames are followed by unknown bytes. |
| 10 | `StatusBusy` | Every configured codec context is active. The call does not wait. |
| 11 | `StatusUnsupported` | The active Wago callback cannot expose the requested storage API. |
| 12 | `StatusInternalError` | Plugin state is closed or codec setup failed. |

## Transaction and overlap rules

Compression and decompression write into a finite private staging buffer. Only
after the codec reports complete success does the plugin copy the result into
guest storage. Therefore:

- partial output is never committed;
- failure always returns `written = 0`;
- the destination range is unchanged on failure; and
- exact or partial input/output overlap is supported.

Success may overwrite at most `written` bytes starting at the destination; the
remainder of the declared destination capacity is unchanged.
