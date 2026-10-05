(module
  (import "wago_zstd.wasm64" "abi_version"
    (func $abi_version (result i32)))
  (import "wago_zstd.wasm64" "compress"
    (func $compress (param i64 i64 i64 i64 i32) (result i32 i32)))
  (import "wago_zstd.wasm64" "decompress"
    (func $decompress (param i64 i64 i64 i64) (result i32 i32)))

  (memory (export "memory") i64 2 2)
  (data (i64.const 0) "Wago Zstandard memory64 integration")
  (data (i64.const 64) "junk")

  (func (export "compress_proxy") (param i64 i64 i64 i64 i32) (result i32 i32)
    local.get 0
    local.get 1
    local.get 2
    local.get 3
    local.get 4
    call $compress)

  (func (export "decompress_proxy") (param i64 i64 i64 i64) (result i32 i32)
    local.get 0
    local.get 1
    local.get 2
    local.get 3
    call $decompress)

  (func (export "run") (result i32)
    (local $status i32)
    (local $compressed i32)
    (local $written i32)

    call $abi_version
    i32.const 1
    i32.ne
    if
      i32.const -100
      return
    end

    i64.const 0
    i64.const 35
    i64.const 1024
    i64.const 8192
    i32.const 0
    call $compress
    local.set $compressed
    local.set $status
    local.get $status
    if
      local.get $status
      i32.const -1000
      i32.sub
      return
    end

    i64.const 1024
    local.get $compressed
    i64.extend_i32_u
    i64.const 16384
    i64.const 8192
    call $decompress
    local.set $written
    local.set $status
    local.get $status
    if
      local.get $status
      i32.const -2000
      i32.sub
      return
    end
    local.get $written))
