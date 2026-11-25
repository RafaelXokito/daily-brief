package picture

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrBadWebP means a WebP file is animated, malformed, or has a frame
// whose size differs from its canvas.
var ErrBadWebP = errors.New("picture: unsupported WebP")

const (
	riffHeader   = 12 // "RIFF", length, "WEBP"
	chunkHeader  = 8  // FourCC, length
	vp8xLen      = 10
	vp8HeaderLen = 10
	vp8lLen      = 5
	vp8lMagic    = 0x2f
	webpAnimBit  = 1 << 1
	frameSideCap = 0x3fff // VP8 and VP8L store each side in 14 bits
)

// checkWebP walks a WebP file's chunks and checks every frame header, not
// just the size DecodeConfig reports: the decoder allocates for the frame's
// own size, which an extended (VP8X) file may set apart from its canvas.
// Animated files are refused. Data that is not a RIFF WebP returns nil.
func checkWebP(data []byte) error {
	if len(data) < riffHeader || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return nil
	}
	end := min(len(data), chunkHeader+int(binary.LittleEndian.Uint32(data[4:8])))
	canvasW, canvasH := 0, 0
	for off := riffHeader; off < end; {
		if end-off < chunkHeader {
			return fmt.Errorf("%w: truncated chunk", ErrBadWebP)
		}
		id := string(data[off : off+4])
		n := int(binary.LittleEndian.Uint32(data[off+4 : off+8]))
		off += chunkHeader
		if n > end-off {
			return fmt.Errorf("%w: chunk %q overruns the file", ErrBadWebP, id)
		}
		w, h, sized, err := chunkSize(id, data[off:off+n])
		if err != nil {
			return err
		}
		if sized {
			if err := checkSize(w, h); err != nil {
				return err
			}
			switch {
			case id == "VP8X":
				canvasW, canvasH = w, h
			case canvasW > 0 && (w != canvasW || h != canvasH):
				return fmt.Errorf("%w: %s frame %dx%d on a %dx%d canvas", ErrBadWebP, id, w, h, canvasW, canvasH)
			}
		}
		off += n + n%2
	}
	return nil
}

// chunkSize returns the pixel size a VP8X, VP8 or VP8L chunk declares
// (sized true), sized false for other chunks, and an error for animation
// or a bad header.
func chunkSize(id string, p []byte) (w, h int, sized bool, err error) {
	switch id {
	case "ANIM", "ANMF":
		return 0, 0, false, fmt.Errorf("%w: animated", ErrBadWebP)
	case "VP8X":
		if len(p) < vp8xLen {
			return 0, 0, false, fmt.Errorf("%w: short VP8X", ErrBadWebP)
		}
		if p[0]&webpAnimBit != 0 {
			return 0, 0, false, fmt.Errorf("%w: animated", ErrBadWebP)
		}
		return 1 + int(le24(p[4:7])), 1 + int(le24(p[7:10])), true, nil
	case "VP8 ":
		// Frame tag (key frame when bit 0 is clear), start code, sizes.
		if len(p) < vp8HeaderLen || p[0]&1 != 0 || !bytes.Equal(p[3:6], []byte{0x9d, 0x01, 0x2a}) {
			return 0, 0, false, fmt.Errorf("%w: bad VP8 header", ErrBadWebP)
		}
		return int(binary.LittleEndian.Uint16(p[6:8]) & frameSideCap), int(binary.LittleEndian.Uint16(p[8:10]) & frameSideCap), true, nil
	case "VP8L":
		if len(p) < vp8lLen || p[0] != vp8lMagic {
			return 0, 0, false, fmt.Errorf("%w: bad VP8L header", ErrBadWebP)
		}
		b := binary.LittleEndian.Uint32(p[1:5])
		return 1 + int(b&frameSideCap), 1 + int(b>>14&frameSideCap), true, nil
	}
	return 0, 0, false, nil
}

func le24(b []byte) uint32 { return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 }
