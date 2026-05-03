package cacheutil

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
)

var (
	zstdGobEncoder *zstd.Encoder
	zstdGobDecoder *zstd.Decoder
)

func init() {
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		panic(fmt.Sprintf("init cache zstd encoder: %v", err))
	}
	zstdGobEncoder = enc
	dec, err := zstd.NewReader(nil)
	if err != nil {
		panic(fmt.Sprintf("init cache zstd decoder: %v", err))
	}
	zstdGobDecoder = dec
}

// EncodeZstdGob returns zstd(gob(v)) using the shared cache compression
// level. It is intended for whole-entry cache payloads whose final size is
// needed before writing LRU metadata.
func EncodeZstdGob(v any) ([]byte, error) {
	var raw bytes.Buffer
	if err := gob.NewEncoder(&raw).Encode(v); err != nil {
		return nil, err
	}
	return zstdGobEncoder.EncodeAll(raw.Bytes(), nil), nil
}

// DecodeZstdGob decodes a zstd(gob(v)) cache payload from r.
func DecodeZstdGob(r io.Reader, v any) error {
	compressed, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	raw, err := zstdGobDecoder.DecodeAll(compressed, nil)
	if err != nil {
		return err
	}
	return gob.NewDecoder(bytes.NewReader(raw)).Decode(v)
}

// IsZstdFrame reports whether b starts with the RFC 8878 zstd frame magic.
// Tests use this as a regression guard against silently writing raw gob.
func IsZstdFrame(b []byte) bool {
	return len(b) >= 4 && b[0] == 0x28 && b[1] == 0xb5 && b[2] == 0x2f && b[3] == 0xfd
}
