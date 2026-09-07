package media

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"strings"
)

// ISO BMFF starts with a size followed by "ftyp", NOT "ftyp" at byte zero.
func sniffImageContainer(data []byte) (string, string) {
	if len(data) < 16 || string(data[4:8]) != "ftyp" {
		return "", ""
	}
	n := int(binary.BigEndian.Uint32(data[:4]))
	if n < 16 || n > len(data) || n%4 != 0 {
		return "", ""
	}
	brands := []string{string(data[8:12])}
	for i := 16; i < n; i += 4 {
		brands = append(brands, string(data[i:i+4]))
	}
	for _, b := range brands {
		if b == "avif" || b == "avis" {
			return ".avif", "image/avif"
		}
	}
	for _, b := range brands {
		switch b {
		case "heic", "heix", "hevc", "hevx":
			return ".heic", "image/heic"
		}
	}
	// Generic HEIF brands alone also appear in AVIF. AVIF has priority above.
	for _, b := range brands {
		if b == "mif1" || b == "msf1" {
			return ".heif", "image/heif"
		}
	}
	return "", ""
}
func validImageContainer(data []byte) bool {
	meta, payload := false, false
	for len(data) > 0 {
		if len(data) < 8 {
			return false
		}
		n := uint64(binary.BigEndian.Uint32(data[:4]))
		header := uint64(8)
		if n == 1 {
			if len(data) < 16 {
				return false
			}
			n = binary.BigEndian.Uint64(data[8:16])
			header = 16
		}
		if n == 0 {
			n = uint64(len(data))
		}
		if n < header || n > uint64(len(data)) {
			return false
		}
		switch string(data[4:8]) {
		case "meta", "moov":
			meta = n > header+4
		case "mdat":
			payload = n > header
		}
		data = data[n:]
	}
	return meta && payload
}

// Only an XML document whose root is SVG is an SVG; an XML declaration alone
// is not evidence of an image. Active content is rejected; static serving is sandboxed.
func validSVG(data []byte) bool {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '<' {
		return false
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	depth, roots := 0, 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return roots == 1 && depth == 0
		}
		if err != nil {
			return false
		}
		switch e := tok.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots != 1 || e.Name.Local != "svg" || (e.Name.Space != "" && e.Name.Space != "http://www.w3.org/2000/svg") {
					return false
				}
			}
			switch strings.ToLower(e.Name.Local) {
			case "script", "foreignobject", "iframe", "object", "embed":
				return false
			}
			for _, a := range e.Attr {
				v := strings.ToLower(strings.TrimSpace(a.Value))
				if strings.HasPrefix(strings.ToLower(a.Name.Local), "on") || strings.HasPrefix(v, "javascript:") {
					return false
				}
			}
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && len(bytes.TrimSpace(e)) != 0 {
				return false
			}
		}
	}
}
func validateICO(data []byte) (int, int, error) {
	if len(data) < 6 {
		return 0, 0, ErrNotImage
	}
	n := int(binary.LittleEndian.Uint16(data[4:6]))
	if n < 1 || 6+n*16 > len(data) {
		return 0, 0, ErrNotImage
	}
	w, h := int(data[6]), int(data[7])
	if w == 0 {
		w = 256
	}
	if h == 0 {
		h = 256
	}
	for i := 0; i < n; i++ {
		p := data[6+i*16:]
		size, off := uint64(binary.LittleEndian.Uint32(p[8:12])), uint64(binary.LittleEndian.Uint32(p[12:16]))
		if size < 4 || off < uint64(6+n*16) || off+size > uint64(len(data)) {
			return 0, 0, ErrNotImage
		}
		frame := data[off : off+size]
		if !bytes.HasPrefix(frame, []byte("\x89PNG\r\n\x1a\n")) && binary.LittleEndian.Uint32(frame[:4]) < 40 {
			return 0, 0, ErrNotImage
		}
	}
	return w, h, nil
}

// Preflight GIF descriptors before DecodeAll can allocate every frame.
func checkGIFBudget(data []byte) error {
	if len(data) < 13 {
		return ErrNotImage
	}
	pos := 13
	if data[10]&0x80 != 0 {
		pos += 3 * (1 << ((data[10] & 7) + 1))
	}
	var pixels int64
	frames := 0
	skipBlocks := func() bool {
		for pos < len(data) {
			n := int(data[pos])
			pos++
			if n == 0 {
				return true
			}
			pos += n
		}
		return false
	}
	for pos < len(data) {
		marker := data[pos]
		pos++
		switch marker {
		case 0x3b:
			if frames == 0 {
				return ErrNotImage
			}
			return nil
		case 0x21:
			pos++
			if !skipBlocks() {
				return ErrNotImage
			}
		case 0x2c:
			if pos+9 > len(data) {
				return ErrNotImage
			}
			w, h := int(binary.LittleEndian.Uint16(data[pos+4:])), int(binary.LittleEndian.Uint16(data[pos+6:]))
			pixels += int64(w) * int64(h)
			frames++
			if !validDimensions(w, h) || pixels > maxAnimationPixels {
				return ErrImageDimensions
			}
			flags := data[pos+8]
			pos += 9
			if flags&0x80 != 0 {
				pos += 3 * (1 << ((flags & 7) + 1))
			}
			pos++
			if !skipBlocks() {
				return ErrNotImage
			}
		default:
			return ErrNotImage
		}
	}
	return ErrNotImage
}

// Validate lengths/checksums and animation frame budget while preserving APNG
// control chunks. Trim anything after IEND instead of turning it into a still PNG.
func writePNGChunk(buf *bytes.Buffer, kind string, body []byte) {
	binary.Write(buf, binary.BigEndian, uint32(len(body)))
	buf.WriteString(kind)
	buf.Write(body)
	crc := crc32.NewIEEE()
	crc.Write([]byte(kind))
	crc.Write(body)
	binary.Write(buf, binary.BigEndian, crc.Sum32())
}
func checkPNG(data []byte) (int, bool, error) {
	animated := false
	var frames, expected, sequence uint32
	var pixels int64
	var ihdr, frameHeader []byte
	var palette bytes.Buffer
	var frameData [][]byte
	validateFrame := func() error {
		if frameHeader == nil {
			return nil
		}
		if len(frameData) == 0 {
			return ErrNotImage
		}
		var buf bytes.Buffer
		buf.WriteString("\x89PNG\r\n\x1a\n")
		writePNGChunk(&buf, "IHDR", frameHeader)
		buf.Write(palette.Bytes())
		for _, body := range frameData {
			writePNGChunk(&buf, "IDAT", body)
		}
		writePNGChunk(&buf, "IEND", nil)
		if _, err := png.Decode(&buf); err != nil {
			return ErrNotImage
		}
		return nil
	}
	for p := 8; p < len(data); {
		if p+12 > len(data) {
			return 0, false, ErrNotImage
		}
		n := int(binary.BigEndian.Uint32(data[p:]))
		if n > len(data)-p-12 {
			return 0, false, ErrNotImage
		}
		end := p + 12 + n
		kind := string(data[p+4 : p+8])
		body := data[p+8 : p+8+n]
		if crc32.ChecksumIEEE(data[p+4:p+8+n]) != binary.BigEndian.Uint32(data[p+8+n:end]) {
			return 0, false, ErrNotImage
		}
		switch kind {
		case "IHDR":
			if n != 13 || ihdr != nil {
				return 0, false, ErrNotImage
			}
			ihdr = body
		case "PLTE", "tRNS":
			palette.Write(data[p:end])
		case "acTL":
			if n != 8 || animated {
				return 0, false, ErrNotImage
			}
			animated = true
			expected = binary.BigEndian.Uint32(body)
			if expected == 0 {
				return 0, false, ErrNotImage
			}
		case "fcTL":
			if !animated || n != 26 || ihdr == nil || binary.BigEndian.Uint32(body) != sequence {
				return 0, false, ErrNotImage
			}
			sequence++
			if err := validateFrame(); err != nil {
				return 0, false, err
			}
			frames++
			w, h := int(binary.BigEndian.Uint32(body[4:])), int(binary.BigEndian.Uint32(body[8:]))
			if !validDimensions(w, h) {
				return 0, false, ErrImageDimensions
			}
			pixels += int64(w) * int64(h)
			if pixels > maxAnimationPixels {
				return 0, false, ErrImageDimensions
			}
			x, y := uint64(binary.BigEndian.Uint32(body[12:])), uint64(binary.BigEndian.Uint32(body[16:]))
			if x+uint64(w) > uint64(binary.BigEndian.Uint32(ihdr)) || y+uint64(h) > uint64(binary.BigEndian.Uint32(ihdr[4:])) {
				return 0, false, ErrNotImage
			}
			frameHeader = append([]byte{}, ihdr...)
			copy(frameHeader[:8], body[4:12])
			frameData = nil
		case "IDAT":
			if frameHeader != nil {
				frameData = append(frameData, body)
			}
		case "fdAT":
			if n < 4 || frameHeader == nil || binary.BigEndian.Uint32(body) != sequence {
				return 0, false, ErrNotImage
			}
			sequence++
			frameData = append(frameData, body[4:])
		case "IEND":
			if n != 0 || (animated && frames != expected) {
				return 0, false, ErrNotImage
			}
			if err := validateFrame(); err != nil {
				return 0, false, err
			}
			return end, animated, nil
		}
		p = end
	}
	return 0, false, ErrNotImage
}

func uint24(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 }

// Validate animated WebP frame payloads using the existing still-image decoder.
// The original RIFF container retains duration, looping, blending and disposal.
func validateWebP(data []byte) ([]byte, int, int, error) {
	if len(data) < 12 {
		return nil, 0, 0, ErrNotImage
	}
	end := uint64(binary.LittleEndian.Uint32(data[4:8])) + 8
	if end > uint64(len(data)) || end < 12 {
		return nil, 0, 0, ErrNotImage
	}
	data = data[:end]
	w, h, frames := 0, 0, 0
	animated, animControl := false, false
	var pixels int64
	for p := 12; p < len(data); {
		if p+8 > len(data) {
			return nil, 0, 0, ErrNotImage
		}
		n := int(binary.LittleEndian.Uint32(data[p+4:]))
		if n > len(data)-p-8 {
			return nil, 0, 0, ErrNotImage
		}
		body := data[p+8 : p+8+n]
		switch string(data[p : p+4]) {
		case "VP8X":
			if n != 10 {
				return nil, 0, 0, ErrNotImage
			}
			animated = body[0]&2 != 0
			w, h = uint24(body[4:7])+1, uint24(body[7:10])+1
			if !validDimensions(w, h) {
				return nil, 0, 0, ErrImageDimensions
			}
		case "ANIM":
			if n != 6 || !animated {
				return nil, 0, 0, ErrNotImage
			}
			animControl = true
		case "ANMF":
			if !animated || !animControl || n < 16 {
				return nil, 0, 0, ErrNotImage
			}
			fw, fh := uint24(body[6:9])+1, uint24(body[9:12])+1
			pixels += int64(fw) * int64(fh)
			if !validDimensions(fw, fh) || pixels > maxAnimationPixels {
				return nil, 0, 0, ErrImageDimensions
			}
			if uint24(body[:3])*2+fw > w || uint24(body[3:6])*2+fh > h {
				return nil, 0, 0, ErrNotImage
			}
			// A VP8X header is needed to decode ALPH + VP8 frame subchunks.
			frame := make([]byte, 30)
			copy(frame, "RIFF")
			copy(frame[8:], "WEBPVP8X")
			binary.LittleEndian.PutUint32(frame[16:], 10)
			frame[20] = 0
			if len(body) >= 20 && string(body[16:20]) == "ALPH" {
				frame[20] = 0x10
			}
			copy(frame[24:27], body[6:9])
			copy(frame[27:30], body[9:12])
			frame = append(frame, body[16:]...)
			binary.LittleEndian.PutUint32(frame[4:], uint32(len(frame)-8))
			// Lossless frames carry their own alpha and do not use an ALPH chunk.
			if len(body) >= 20 && string(body[16:20]) == "VP8L" {
				frame[20] = 0
			}
			cfg, _, err := image.DecodeConfig(bytes.NewReader(frame))
			if err != nil || cfg.Width != fw || cfg.Height != fh {
				return nil, 0, 0, ErrNotImage
			}
			if _, _, err := image.Decode(bytes.NewReader(frame)); err != nil {
				return nil, 0, 0, ErrNotImage
			}
			frames++
		}
		p += 8 + n + (n & 1)
		if p > len(data) {
			return nil, 0, 0, ErrNotImage
		}
	}
	if animated {
		if !animControl || frames == 0 {
			return nil, 0, 0, ErrNotImage
		}
		return data, w, h, nil
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, ErrNotImage
	}
	if !validDimensions(cfg.Width, cfg.Height) {
		return nil, 0, 0, ErrImageDimensions
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return nil, 0, 0, ErrNotImage
	}
	return data, cfg.Width, cfg.Height, nil
}
