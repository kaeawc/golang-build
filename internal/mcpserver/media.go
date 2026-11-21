package mcpserver

import (
	"bytes"
	"encoding/binary"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"math"
)

// identiconPNG renders a deterministic 5x5 mirrored identicon for seed,
// scaled to 8px cells. It gives the image-content and blob-resource demos
// real, valid PNG bytes without shipping binary fixtures.
func identiconPNG(seed string) []byte {
	h := fnv.New64a()
	h.Write([]byte(seed))
	sum := h.Sum64()
	var rgb [8]byte
	binary.BigEndian.PutUint64(rgb[:], sum)

	const cells, scale = 5, 8
	fg := color.RGBA{R: rgb[0], G: rgb[1], B: rgb[2], A: 0xff}
	bg := color.RGBA{R: 0xf4, G: 0xf4, B: 0xf4, A: 0xff}
	img := image.NewRGBA(image.Rect(0, 0, cells*scale, cells*scale))
	for y := range cells {
		for x := range 3 { // left half + centre column, mirrored
			on := sum>>uint(y*3+x)&1 == 1
			c := bg
			if on {
				c = fg
			}
			fill(img, x, y, scale, c)
			fill(img, cells-1-x, y, scale, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		// png.Encode only fails on writer errors; bytes.Buffer never errors.
		panic(err)
	}
	return buf.Bytes()
}

func fill(img *image.RGBA, cx, cy, scale int, c color.RGBA) {
	for y := cy * scale; y < (cy+1)*scale; y++ {
		for x := cx * scale; x < (cx+1)*scale; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

// chimeWAV synthesises a short two-note 8 kHz mono 16-bit PCM WAV. The
// pitch is derived from seed so different users get different chimes.
func chimeWAV(seed string) []byte {
	h := fnv.New32a()
	h.Write([]byte(seed))
	base := 440 + float64(h.Sum32()%220)

	const rate, noteSamples = 8000, 1200 // 0.15s per note
	notes := [...]float64{base, base * 1.5}
	pcm := make([]int16, 0, len(notes)*noteSamples)
	for _, freq := range notes {
		for i := range noteSamples {
			env := 1 - float64(i)/noteSamples // linear decay avoids clicks
			v := math.Sin(2*math.Pi*freq*float64(i)/rate) * env * 0.4
			pcm = append(pcm, int16(v*math.MaxInt16))
		}
	}

	var buf bytes.Buffer
	const dataLen = uint32(len(notes) * noteSamples * 2) // array len is a constant
	write := func(v any) { _ = binary.Write(&buf, binary.LittleEndian, v) }
	buf.WriteString("RIFF")
	write(36 + dataLen)
	buf.WriteString("WAVEfmt ")
	write(uint32(16))       // fmt chunk size
	write(uint16(1))        // PCM
	write(uint16(1))        // mono
	write(uint32(rate))     // sample rate
	write(uint32(rate * 2)) // byte rate
	write(uint16(2))        // block align
	write(uint16(16))       // bits per sample
	buf.WriteString("data")
	write(dataLen)
	write(pcm)
	return buf.Bytes()
}
