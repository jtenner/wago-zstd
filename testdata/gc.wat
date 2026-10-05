(module
  (type $bytes (array (mut i32)))
  (type $immutable-bytes (array i32))
  (import "wago_zstd.gc" "abi_version"
    (func $abi_version (result i32)))
  (import "wago_zstd.gc" "compress"
    (func $compress (param (ref $bytes) i32 i32 (ref $bytes) i32 i32 i32) (result i32 i32)))
  (import "wago_zstd.gc" "decompress"
    (func $decompress (param (ref $bytes) i32 i32 (ref $bytes) i32 i32) (result i32 i32)))
  (import "wago_zstd.gc" "compress"
    (func $compress-immutable (param (ref $immutable-bytes) i32 i32 (ref $bytes) i32 i32 i32) (result i32 i32)))

  (func (export "run") (result i32)
    (local $source (ref null $bytes))
    (local $compressed-buffer (ref null $bytes))
    (local $output (ref null $bytes))
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

    i32.const 16
    array.new_default $bytes
    local.set $source
    local.get $source
    ref.as_non_null
    i32.const 0
    i32.const 65
    i32.const 16
    array.fill $bytes
    i32.const 256
    array.new_default $bytes
    local.set $compressed-buffer
    i32.const 32
    array.new_default $bytes
    local.set $output

    local.get $source
    ref.as_non_null
    i32.const 0
    i32.const 64
    local.get $compressed-buffer
    ref.as_non_null
    i32.const 0
    i32.const 1024
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

    local.get $compressed-buffer
    ref.as_non_null
    i32.const 0
    local.get $compressed
    local.get $output
    ref.as_non_null
    i32.const 0
    i32.const 128
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

    local.get $written
    i32.const 64
    i32.ne
    if
      i32.const -3000
      return
    end
    local.get $output
    ref.as_non_null
    i32.const 0
    array.get $bytes
    i32.const 65
    i32.eq)

  (func (export "short_output") (result i32)
    (local $source (ref null $bytes))
    (local $compressed-buffer (ref null $bytes))
    (local $output (ref null $bytes))
    (local $status i32)
    (local $compressed i32)
    (local $written i32)

    i32.const 16
    array.new_default $bytes
    local.set $source
    local.get $source
    ref.as_non_null
    i32.const 0
    i32.const 65
    i32.const 16
    array.fill $bytes
    i32.const 256
    array.new_default $bytes
    local.set $compressed-buffer
    i32.const 8
    array.new_default $bytes
    local.set $output
    local.get $output
    ref.as_non_null
    i32.const 0
    i32.const 1515870810
    array.set $bytes

    local.get $source
    ref.as_non_null
    i32.const 0
    i32.const 64
    local.get $compressed-buffer
    ref.as_non_null
    i32.const 0
    i32.const 1024
    i32.const 0
    call $compress
    local.set $compressed
    local.set $status
    local.get $status
    if
      i32.const 0
      return
    end

    local.get $compressed-buffer
    ref.as_non_null
    i32.const 0
    local.get $compressed
    local.get $output
    ref.as_non_null
    i32.const 0
    i32.const 32
    call $decompress
    local.set $written
    local.set $status

    local.get $status
    i32.const 4
    i32.eq
    local.get $written
    i32.eqz
    i32.and
    local.get $output
    ref.as_non_null
    i32.const 0
    array.get $bytes
    i32.const 1515870810
    i32.eq
    i32.and)

  (func (export "bounds") (result i32)
    (local $source (ref null $bytes))
    (local $output (ref null $bytes))
    (local $status i32)
    (local $written i32)
    i32.const 16
    array.new_default $bytes
    local.set $source
    i32.const 256
    array.new_default $bytes
    local.set $output
    local.get $source
    ref.as_non_null
    i32.const 0
    i32.const 65
    local.get $output
    ref.as_non_null
    i32.const 0
    i32.const 1024
    i32.const 0
    call $compress
    local.set $written
    local.set $status
    local.get $status
    i32.const 1
    i32.eq
    local.get $written
    i32.eqz
    i32.and)

  (func (export "invalid") (result i32)
    (local $source (ref null $bytes))
    (local $output (ref null $bytes))
    (local $status i32)
    (local $written i32)
    i32.const 1
    array.new_default $bytes
    local.set $source
    local.get $source
    ref.as_non_null
    i32.const 0
    i32.const 1802399082
    array.set $bytes
    i32.const 16
    array.new_default $bytes
    local.set $output
    local.get $source
    ref.as_non_null
    i32.const 0
    i32.const 4
    local.get $output
    ref.as_non_null
    i32.const 0
    i32.const 64
    call $decompress
    local.set $written
    local.set $status
    local.get $status
    i32.const 5
    i32.eq
    local.get $written
    i32.eqz
    i32.and)

  (func (export "immutable_backing_limit") (result i32)
    (local $source (ref null $immutable-bytes))
    (local $output (ref null $bytes))
    (local $status i32)
    (local $written i32)
    ;; The requested range is one byte, but Wago must detach an immutable
    ;; array's complete 1 MiB + 4 byte backing payload. Admission must count
    ;; four bytes per i32 element and reject before making that copy.
    i32.const 262145
    array.new_default $immutable-bytes
    local.set $source
    i32.const 64
    array.new_default $bytes
    local.set $output
    local.get $source
    ref.as_non_null
    i32.const 0
    i32.const 1
    local.get $output
    ref.as_non_null
    i32.const 0
    i32.const 256
    i32.const 0
    call $compress-immutable
    local.set $written
    local.set $status
    local.get $status
    i32.const 2
    i32.eq
    local.get $written
    i32.eqz
    i32.and)

  (func (export "overlap") (result i32)
    (local $buffer (ref null $bytes))
    (local $status i32)
    (local $compressed i32)
    (local $written i32)
    i32.const 300
    array.new_default $bytes
    local.set $buffer
    local.get $buffer
    ref.as_non_null
    i32.const 0
    i32.const 65
    i32.const 16
    array.fill $bytes

    local.get $buffer
    ref.as_non_null
    i32.const 0
    i32.const 64
    local.get $buffer
    ref.as_non_null
    i32.const 4
    i32.const 1024
    i32.const 0
    call $compress
    local.set $compressed
    local.set $status
    local.get $status
    if
      i32.const 0
      return
    end

    local.get $buffer
    ref.as_non_null
    i32.const 4
    local.get $compressed
    local.get $buffer
    ref.as_non_null
    i32.const 0
    i32.const 1024
    call $decompress
    local.set $written
    local.set $status
    local.get $status
    i32.eqz
    local.get $written
    i32.const 64
    i32.eq
    i32.and
    local.get $buffer
    ref.as_non_null
    i32.const 0
    array.get $bytes
    i32.const 65
    i32.eq
    i32.and))
