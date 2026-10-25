package runs

import (
	"bytes"
	"encoding/json"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// Step outputs larger than compressAbove bytes are stored as a zstd frame.
// A frame starts with zstd's magic number, which no JSON text starts with,
// so a stored output reads either way.
const compressAbove = 256

var (
	zstdMagic = []byte{0x28, 0xb5, 0x2f, 0xfd}
	encoder   = sync.OnceValue(func() *zstd.Encoder {
		e, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
		if err != nil {
			panic(err)
		}
		return e
	})
	decoder = sync.OnceValue(func() *zstd.Decoder {
		// Outputs are bounded at a few MiB; a larger frame is not one of ours.
		d, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(0), zstd.WithDecoderMaxMemory(64<<20))
		if err != nil {
			panic(err)
		}
		return d
	})
)

// pack returns output as it is stored.
func pack(output json.RawMessage) []byte {
	if len(output) <= compressAbove {
		return output
	}
	return encoder().EncodeAll(output, make([]byte, 0, len(output)/4))
}

// unpack returns a stored output as JSON.
func unpack(stored []byte) (json.RawMessage, error) {
	if !bytes.HasPrefix(stored, zstdMagic) {
		return stored, nil
	}
	return decoder().DecodeAll(stored, nil)
}
