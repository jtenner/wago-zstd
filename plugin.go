// Package zstd provides a bounded Zstandard compression plugin for Wago.
// Compression and decompression are delegated to klauspost/compress/zstd.
package zstd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	wago "github.com/wago-org/wago"
)

const (
	PluginID = "github.com/jtenner/wago-zstd"

	ModuleWasm32 = "wago_zstd.wasm32"
	ModuleWasm64 = "wago_zstd.wasm64"
	ModuleGC     = "wago_zstd.gc"

	ABIVersion int32 = 1

	CapZstd wago.Capability = "compression.zstd"
)

// Plugin owns the bounded codec pool used by one Wago activation.
type Plugin struct {
	codec *Codec
}

// Definition returns fresh immutable provider metadata.
func Definition() wago.PluginDefinition {
	return wago.PluginDefinition{
		ID:          PluginID,
		Name:        "Zstandard",
		Version:     "0.0.1",
		Description: "Bounded whole-buffer Zstandard compression and decompression.",
		Stability:   wago.Experimental,
		Compatibility: wago.Compatibility{
			Engines:   map[string]string{"go": ">=1.25", "wago": ">=0.1.0"},
			Platforms: []string{"linux/amd64"},
		},
		Provenance: wago.PluginProvenance{
			Repository: "https://github.com/jtenner/wago-zstd",
			License:    "Apache-2.0",
			Authors:    []string{"jtenner"},
		},
		Authorities: []wago.AuthorityRequest{{
			Name:   wago.AuthorityHostImportDefine,
			Mode:   wago.AuthorityRequired,
			Reason: "define the bounded Zstandard guest APIs",
			Scope:  wago.AuthorityScope{Modules: []string{ModuleGC, ModuleWasm32, ModuleWasm64}},
		}},
		ConfigSchema: append(json.RawMessage(nil), configSchema...),
	}
}

// Provider returns the side-effect-free provider catalog entry.
func Provider() wago.PluginProvider {
	return wago.PluginProvider{
		Definition: Definition(),
		New:        func() wago.Plugin { return &Plugin{} },
		ValidateConfig: func(raw json.RawMessage) error {
			_, err := decodeConfig(raw)
			return err
		},
	}
}

// PluginSet returns a directly selected provider with exact authority grants.
// At most one Config may be supplied; no config selects finite defaults.
func PluginSet(configs ...Config) (wago.PluginSet, error) {
	if len(configs) > 1 {
		return wago.PluginSet{}, errors.New("zstd.PluginSet accepts at most one Config")
	}
	var config Config
	if len(configs) == 1 {
		config = configs[0]
	}
	normalized, err := config.normalized()
	if err != nil {
		return wago.PluginSet{}, err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return wago.PluginSet{}, err
	}
	provider := Provider()
	digest, err := wago.DefinitionDigest(provider.Definition)
	if err != nil {
		return wago.PluginSet{}, err
	}
	request := provider.Definition.Authorities[0]
	return wago.PluginSet{
		Providers: []wago.PluginProvider{provider},
		Selections: []wago.PluginSelection{{
			ID:               provider.Definition.ID,
			DefinitionDigest: digest,
			Direct:           true,
			Dependencies:     map[string]string{},
			Grants:           []wago.AuthorityGrant{{Name: request.Name, Scope: request.Scope}},
			Config:           raw,
		}},
	}, nil
}

func (p *Plugin) Register(reg *wago.Registrar) (registerErr error) {
	var config Config
	if err := reg.Config(&config); err != nil {
		return err
	}
	codec, err := NewCodec(config)
	if err != nil {
		return fmt.Errorf("zstd: configure codec: %w", err)
	}
	p.codec = codec
	defer func() {
		if registerErr != nil {
			codec.Close()
			p.codec = nil
		}
	}()

	if err := reg.GuestCapability(CapZstd, wago.CapabilityDocs("compress and decompress bounded Zstandard frame sequences")); err != nil {
		return err
	}
	imports, err := reg.HostImports()
	if err != nil {
		return err
	}
	p.registerWasm32(imports)
	p.registerWasm64(imports)
	p.registerGC(imports)
	return reg.Lifecycle(wago.PluginLifecycle{Stop: func(context.Context) error {
		codec.Close()
		return nil
	}})
}

func registerCommon(imports *wago.HostImportRegistrar, module string, compress, decompress, compressPacked, decompressPacked any, compressParams, decompressParams []wago.ValType) {
	imports.HostFunc(module, "abi_version", func(call wago.HostCall) {
		call.SetI32(0, ABIVersion)
	}).Results(wago.ValI32).Capability(CapZstd).
		Docs("return the Zstandard ABI version")
	imports.HostFunc(module, "compress", compress).
		Params(compressParams...).Results(wago.ValI32, wago.ValI32).Capability(CapZstd).
		Docs("compress one complete Zstandard frame and return status, written")
	imports.HostFunc(module, "decompress", decompress).
		Params(decompressParams...).Results(wago.ValI32, wago.ValI32).Capability(CapZstd).
		Docs("decompress a complete bounded frame sequence and return status, written")
	imports.HostFunc(module, "compress_packed", compressPacked).
		Params(compressParams...).Results(wago.ValI64).Capability(CapZstd).
		Docs("compress one complete Zstandard frame and return packed status/written")
	imports.HostFunc(module, "decompress_packed", decompressPacked).
		Params(decompressParams...).Results(wago.ValI64).Capability(CapZstd).
		Docs("decompress a complete bounded frame sequence and return packed status/written")
}

func (p *Plugin) registerWasm32(imports *wago.HostImportRegistrar) {
	registerCommon(imports, ModuleWasm32, p.compressWasm32, p.decompressWasm32, p.compressPackedWasm32, p.decompressPackedWasm32,
		[]wago.ValType{wago.ValI32, wago.ValI32, wago.ValI32, wago.ValI32, wago.ValI32},
		[]wago.ValType{wago.ValI32, wago.ValI32, wago.ValI32, wago.ValI32})
}

func (p *Plugin) registerWasm64(imports *wago.HostImportRegistrar) {
	registerCommon(imports, ModuleWasm64, p.compressWasm64, p.decompressWasm64, p.compressPackedWasm64, p.decompressPackedWasm64,
		[]wago.ValType{wago.ValI64, wago.ValI64, wago.ValI64, wago.ValI64, wago.ValI32},
		[]wago.ValType{wago.ValI64, wago.ValI64, wago.ValI64, wago.ValI64})
}

func (p *Plugin) registerGC(imports *wago.HostImportRegistrar) {
	registerCommon(imports, ModuleGC, p.compressGC, p.decompressGC, p.compressPackedGC, p.decompressPackedGC,
		[]wago.ValType{wago.ValAnyRef, wago.ValI32, wago.ValI32, wago.ValAnyRef, wago.ValI32, wago.ValI32, wago.ValI32},
		[]wago.ValType{wago.ValAnyRef, wago.ValI32, wago.ValI32, wago.ValAnyRef, wago.ValI32, wago.ValI32})
}

func setResult(call wago.HostCall, status Status, written int) {
	status, written = normalizedResult(status, written)
	call.SetI32(0, int32(status))
	call.SetI32(1, int32(written))
}

func setPackedResult(call wago.HostCall, status Status, written int) {
	call.SetI64(0, int64(packResult(status, written)))
}

// packResult encodes status in the low 32 bits and written in the high 32
// bits. Configuration limits cap successful output at 64 MiB, so written is
// always representable as uint32. Failures are normalized to written == 0.
func packResult(status Status, written int) uint64 {
	status, written = normalizedResult(status, written)
	return uint64(uint32(status)) | uint64(uint32(written))<<32
}

func normalizedResult(status Status, written int) (Status, int) {
	if status != StatusOK || written < 0 || uint64(written) > HardMaxOutputBytes {
		if status == StatusOK {
			status = StatusInternalError
		}
		return status, 0
	}
	return status, written
}

func (p *Plugin) compressWasm32(caller wago.Caller, call wago.HostCall) {
	status, written := p.linearResult(caller, wago.GuestMemory32, true,
		uint64(uint32(call.I32(0))), uint64(uint32(call.I32(1))),
		uint64(uint32(call.I32(2))), uint64(uint32(call.I32(3))), call.I32(4))
	setResult(call, status, written)
}

func (p *Plugin) decompressWasm32(caller wago.Caller, call wago.HostCall) {
	status, written := p.linearResult(caller, wago.GuestMemory32, false,
		uint64(uint32(call.I32(0))), uint64(uint32(call.I32(1))),
		uint64(uint32(call.I32(2))), uint64(uint32(call.I32(3))), 0)
	setResult(call, status, written)
}

func (p *Plugin) compressPackedWasm32(caller wago.Caller, call wago.HostCall) {
	status, written := p.linearResult(caller, wago.GuestMemory32, true,
		uint64(uint32(call.I32(0))), uint64(uint32(call.I32(1))),
		uint64(uint32(call.I32(2))), uint64(uint32(call.I32(3))), call.I32(4))
	setPackedResult(call, status, written)
}

func (p *Plugin) decompressPackedWasm32(caller wago.Caller, call wago.HostCall) {
	status, written := p.linearResult(caller, wago.GuestMemory32, false,
		uint64(uint32(call.I32(0))), uint64(uint32(call.I32(1))),
		uint64(uint32(call.I32(2))), uint64(uint32(call.I32(3))), 0)
	setPackedResult(call, status, written)
}

func (p *Plugin) compressWasm64(caller wago.Caller, call wago.HostCall) {
	status, written := p.linearResult(caller, wago.GuestMemory64, true,
		uint64(call.I64(0)), uint64(call.I64(1)), uint64(call.I64(2)), uint64(call.I64(3)), call.I32(4))
	setResult(call, status, written)
}

func (p *Plugin) decompressWasm64(caller wago.Caller, call wago.HostCall) {
	status, written := p.linearResult(caller, wago.GuestMemory64, false,
		uint64(call.I64(0)), uint64(call.I64(1)), uint64(call.I64(2)), uint64(call.I64(3)), 0)
	setResult(call, status, written)
}

func (p *Plugin) compressPackedWasm64(caller wago.Caller, call wago.HostCall) {
	status, written := p.linearResult(caller, wago.GuestMemory64, true,
		uint64(call.I64(0)), uint64(call.I64(1)), uint64(call.I64(2)), uint64(call.I64(3)), call.I32(4))
	setPackedResult(call, status, written)
}

func (p *Plugin) decompressPackedWasm64(caller wago.Caller, call wago.HostCall) {
	status, written := p.linearResult(caller, wago.GuestMemory64, false,
		uint64(call.I64(0)), uint64(call.I64(1)), uint64(call.I64(2)), uint64(call.I64(3)), 0)
	setPackedResult(call, status, written)
}

func (p *Plugin) linearResult(caller wago.Caller, addressType wago.GuestMemoryAddressType, compress bool, srcOffset, srcLength, dstOffset, dstCapacity uint64, level int32) (Status, int) {
	codec := p.activeCodec()
	if codec == nil {
		return StatusInternalError, 0
	}
	if status := validateLengths(codec, srcLength, dstCapacity); status != StatusOK {
		return status, 0
	}
	profile, status := encoderProfile(level)
	if compress && status != StatusOK {
		return status, 0
	}
	worker, status := codec.acquire()
	if status != StatusOK {
		return status, 0
	}
	defer codec.release(worker)
	host, ok := any(caller).(wago.GuestStorageHostModule)
	if !ok {
		return StatusUnsupported, 0
	}
	status, written := StatusInvalidArgument, 0
	err := host.WithGuestStorage(func(storage wago.GuestStorage) error {
		info, err := storage.MemoryInfo(0)
		if err != nil {
			return err
		}
		if info.AddressType != addressType {
			return fmt.Errorf("expected Memory%d", addressType)
		}
		src, err := storage.MemoryRange(0, srcOffset, srcLength, wago.GuestStorageRead)
		if err != nil {
			return err
		}
		dst, err := storage.MemoryRange(0, dstOffset, dstCapacity, wago.GuestStorageWrite)
		if err != nil {
			return err
		}
		if compress {
			written, status = codec.compressWithWorker(worker, src, dst, profile)
		} else {
			written, status = codec.decompressWithWorker(worker, src, dst)
		}
		return nil
	})
	if err != nil {
		status, written = StatusInvalidArgument, 0
	}
	return normalizedResult(status, written)
}

func (p *Plugin) compressGC(caller wago.Caller, call wago.HostCall) {
	status, written := p.gcResult(caller, true,
		call.ParamSlots()[0], uint64(uint32(call.I32(1))), uint64(uint32(call.I32(2))),
		call.ParamSlots()[3], uint64(uint32(call.I32(4))), uint64(uint32(call.I32(5))), call.I32(6))
	setResult(call, status, written)
}

func (p *Plugin) decompressGC(caller wago.Caller, call wago.HostCall) {
	status, written := p.gcResult(caller, false,
		call.ParamSlots()[0], uint64(uint32(call.I32(1))), uint64(uint32(call.I32(2))),
		call.ParamSlots()[3], uint64(uint32(call.I32(4))), uint64(uint32(call.I32(5))), 0)
	setResult(call, status, written)
}

func (p *Plugin) compressPackedGC(caller wago.Caller, call wago.HostCall) {
	status, written := p.gcResult(caller, true,
		call.ParamSlots()[0], uint64(uint32(call.I32(1))), uint64(uint32(call.I32(2))),
		call.ParamSlots()[3], uint64(uint32(call.I32(4))), uint64(uint32(call.I32(5))), call.I32(6))
	setPackedResult(call, status, written)
}

func (p *Plugin) decompressPackedGC(caller wago.Caller, call wago.HostCall) {
	status, written := p.gcResult(caller, false,
		call.ParamSlots()[0], uint64(uint32(call.I32(1))), uint64(uint32(call.I32(2))),
		call.ParamSlots()[3], uint64(uint32(call.I32(4))), uint64(uint32(call.I32(5))), 0)
	setPackedResult(call, status, written)
}

func (p *Plugin) gcResult(caller wago.Caller, compress bool, srcToken, srcOffset, srcLength, dstToken, dstOffset, dstCapacity uint64, level int32) (Status, int) {
	codec := p.activeCodec()
	if codec == nil {
		return StatusInternalError, 0
	}
	if status := validateLengths(codec, srcLength, dstCapacity); status != StatusOK {
		return status, 0
	}
	profile, status := encoderProfile(level)
	if compress && status != StatusOK {
		return status, 0
	}
	worker, status := codec.acquire()
	if status != StatusOK {
		return status, 0
	}
	defer codec.release(worker)
	host, ok := any(caller).(wago.GuestStorageHostModule)
	if !ok {
		return StatusUnsupported, 0
	}
	status, written := StatusInvalidArgument, 0
	err := host.WithGuestStorage(func(storage wago.GuestStorage) error {
		srcRef, err := storage.GCRef(srcToken)
		if err != nil {
			return err
		}
		srcInfo, err := storage.GCArrayInfo(srcRef)
		if err != nil {
			return err
		}
		if !supportedGCStorage(srcInfo.Storage) {
			return errors.New("source must be an array<i8> or array<i32>")
		}
		// Immutable GC arrays are detached by Wago before returning a byte view.
		// Bound that implicit copy by the configured input limit.
		backingBytes, ok := gcArrayByteLength(srcInfo)
		if !ok {
			return errors.New("source array byte length overflow")
		}
		if !srcInfo.Mutable && backingBytes > codec.config.MaxInputBytes {
			status = StatusInputTooLarge
			return nil
		}
		srcBytes, _, err := storage.GCArrayBytes(srcRef, wago.GuestStorageRead)
		if err != nil {
			return err
		}
		src, ok := byteRange(srcBytes, srcOffset, srcLength)
		if !ok {
			return errors.New("source range is outside array")
		}

		dstRef, err := storage.GCRef(dstToken)
		if err != nil {
			return err
		}
		dstBytes, dstInfo, err := storage.GCArrayBytes(dstRef, wago.GuestStorageWrite)
		if err != nil {
			return err
		}
		if !supportedGCStorage(dstInfo.Storage) || !dstInfo.Mutable {
			return errors.New("destination must be a mutable array<i8> or array<i32>")
		}
		dst, ok := byteRange(dstBytes, dstOffset, dstCapacity)
		if !ok {
			return errors.New("destination range is outside array")
		}
		if compress {
			written, status = codec.compressWithWorker(worker, src, dst, profile)
		} else {
			written, status = codec.decompressWithWorker(worker, src, dst)
		}
		return nil
	})
	if err != nil {
		status, written = StatusInvalidArgument, 0
	}
	return normalizedResult(status, written)
}

func (p *Plugin) activeCodec() *Codec {
	if p == nil {
		return nil
	}
	return p.codec
}

func validateLengths(codec *Codec, srcLength, dstCapacity uint64) Status {
	if srcLength > codec.config.MaxInputBytes {
		return StatusInputTooLarge
	}
	if dstCapacity > codec.config.MaxOutputBytes {
		return StatusOutputTooLarge
	}
	return StatusOK
}

func byteRange(buffer []byte, offset, length uint64) ([]byte, bool) {
	if offset > uint64(len(buffer)) || length > uint64(len(buffer))-offset {
		return nil, false
	}
	end := offset + length
	return buffer[int(offset):int(end):int(end)], true
}

func supportedGCStorage(storage wago.GuestGCArrayStorage) bool {
	return storage == wago.GuestGCArrayI8 || storage == wago.GuestGCArrayI32
}

func gcArrayByteLength(info wago.GuestGCArrayInfo) (uint64, bool) {
	var width uint64
	switch info.Storage {
	case wago.GuestGCArrayI8:
		width = 1
	case wago.GuestGCArrayI32:
		width = 4
	default:
		return 0, false
	}
	return uint64(info.Length) * width, true
}
