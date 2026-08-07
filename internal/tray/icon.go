// Package tray provides the system tray shell: icon (color reflects
// status), hover tooltip, and right-click menu.
//
// Icon: a colored square (per-state) with the headphone glyph from
// assets/headphone-glyph.png composited on top. Glyph credit: Headphones
// icons created by Magnific - Flaticon
// (https://www.flaticon.com/free-icons/headphones).
package tray

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"sync"
)

//go:embed assets/headphone-glyph.png
var headphoneGlyphPNG []byte

var (
	glyphOnce sync.Once
	glyphImg  image.Image
)

// loadGlyph decodes the embedded headphone PNG once and caches it.
func loadGlyph() image.Image {
	glyphOnce.Do(func() {
		img, err := png.Decode(bytes.NewReader(headphoneGlyphPNG))
		if err != nil {
			// Falls back to a plain colored circle (see compositeIcon)
			// if the embedded asset somehow fails to decode.
			glyphImg = nil
			return
		}
		glyphImg = img
	})
	return glyphImg
}

// State represents the current status, reflected as the tray icon's
// color.
type State int

const (
	// StateOK means Game Mode is active, Sonar is reachable, and all
	// output channels are correctly routed to the headset.
	StateOK State = iota
	// StateProblem means Game Mode is active but something's wrong —
	// Sonar unreachable, or channels routed away from the headset.
	StateProblem
	// StateWorkMode means Work Mode is active — monitoring is
	// deliberately paused, regardless of actual routing state.
	StateWorkMode
)

var (
	colorOK      = color.RGBA{R: 0x4C, G: 0xD6, B: 0x7A, A: 0xFF} // lighter green
	colorProblem = color.RGBA{R: 0xE8, G: 0xA8, B: 0x1B, A: 0xFF} // amber
	colorWork    = color.RGBA{R: 0x5B, G: 0xB8, B: 0xFA, A: 0xFF} // light blue

	// iconSize is generous (64px) for a source .ico entry — Windows and
	// high-DPI displays scale down cleanly from a larger source; scaling
	// up a tiny source looks rough.
	iconSize = 64
)

// LoadIconBytes returns Windows .ico-format bytes for the given state.
func LoadIconBytes(state State) []byte {
	var c color.RGBA
	switch state {
	case StateOK:
		c = colorOK
	case StateWorkMode:
		c = colorWork
	default:
		c = colorProblem
	}

	img := compositeIcon(iconSize, c)
	icoBytes, err := encodeICO(img)
	if err != nil {
		return nil
	}
	return icoBytes
}

// compositeIcon fills a size x size square with the given color, then
// composites the headphone glyph centered on top (scaled to fit with a
// small margin). Falls back to a plain square if the glyph failed to
// decode.
func compositeIcon(size int, c color.RGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: c}, image.Point{}, draw.Src)

	glyph := loadGlyph()
	if glyph == nil {
		return img
	}

	// Scale the glyph to fill ~88% of the square, centered — more room
	// to work with now that there's no circular clip cutting into it.
	glyphSize := int(float64(size) * 0.88)
	resized := resizeImage(glyph, glyphSize, glyphSize)

	offset := (size - glyphSize) / 2
	destRect := image.Rect(offset, offset, offset+glyphSize, offset+glyphSize)
	draw.Draw(img, destRect, resized, image.Point{}, draw.Over)

	return img
}

// resizeImage downsamples (or upsamples) src to newW x newH using a
// simple box filter — averages each destination pixel's corresponding
// source region. Good enough quality for a small tray icon; avoids
// pulling in an external image-resizing dependency for one glyph.
func resizeImage(src image.Image, newW, newH int) *image.RGBA {
	srcB := src.Bounds()
	sw, sh := srcB.Dx(), srcB.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))

	for y := 0; y < newH; y++ {
		sy0 := y * sh / newH
		sy1 := (y + 1) * sh / newH
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}

		for x := 0; x < newW; x++ {
			sx0 := x * sw / newW
			sx1 := (x + 1) * sw / newW
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}

			var rSum, gSum, bSum, aSum, count uint64
			for sy := sy0; sy < sy1 && sy < sh; sy++ {
				for sx := sx0; sx < sx1 && sx < sw; sx++ {
					r, g, b, a := src.At(srcB.Min.X+sx, srcB.Min.Y+sy).RGBA()
					rSum += uint64(r)
					gSum += uint64(g)
					bSum += uint64(b)
					aSum += uint64(a)
					count++
				}
			}
			if count == 0 {
				count = 1
			}
			dst.Set(x, y, color.RGBA64{
				R: uint16(rSum / count),
				G: uint16(gSum / count),
				B: uint16(bSum / count),
				A: uint16(aSum / count),
			})
		}
	}
	return dst
}

// encodeICO wraps a single PNG-encoded image in a minimal Windows .ico
// container. Modern Windows (Vista+) accepts PNG-compressed icon entries
// directly, so no BMP/DIB conversion is needed.
func encodeICO(img image.Image) ([]byte, error) {
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		return nil, err
	}
	pngData := pngBuf.Bytes()

	b := img.Bounds()
	width := b.Dx()
	height := b.Dy()

	widthByte := byte(width)
	if width >= 256 {
		widthByte = 0
	}
	heightByte := byte(height)
	if height >= 256 {
		heightByte = 0
	}

	var out bytes.Buffer

	// ICONDIR
	out.Write([]byte{0, 0}) // reserved
	out.Write([]byte{1, 0}) // type = 1 (icon)
	out.Write([]byte{1, 0}) // count = 1 image

	// ICONDIRENTRY
	out.WriteByte(widthByte)
	out.WriteByte(heightByte)
	out.WriteByte(0)         // color count (0 = no palette)
	out.WriteByte(0)         // reserved
	out.Write([]byte{1, 0})  // planes = 1
	out.Write([]byte{32, 0}) // bit count = 32 (RGBA)
	writeUint32LE(&out, uint32(len(pngData)))
	writeUint32LE(&out, 22) // offset: 6 (ICONDIR) + 16 (one ICONDIRENTRY)

	out.Write(pngData)

	return out.Bytes(), nil
}

func writeUint32LE(buf *bytes.Buffer, v uint32) {
	buf.WriteByte(byte(v))
	buf.WriteByte(byte(v >> 8))
	buf.WriteByte(byte(v >> 16))
	buf.WriteByte(byte(v >> 24))
}

func writeUint16LE(buf *bytes.Buffer, v uint16) {
	buf.WriteByte(byte(v))
	buf.WriteByte(byte(v >> 8))
}

// BuildAppIconICO builds a multi-resolution .ico (16/32/48/256px) for
// use as the compiled .exe's own icon (Explorer, taskbar, Alt+Tab) —
// distinct from the live tray icon, which changes color with state.
// Uses the green "OK" backdrop as a fixed, neutral default since the
// exe icon can't change at runtime.
func BuildAppIconICO() ([]byte, error) {
	sizes := []int{16, 32, 48, 256}

	var pngs [][]byte
	for _, s := range sizes {
		img := compositeIcon(s, colorOK)
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return nil, err
		}
		pngs = append(pngs, buf.Bytes())
	}

	var out bytes.Buffer

	// ICONDIR
	out.Write([]byte{0, 0}) // reserved
	out.Write([]byte{1, 0}) // type = 1 (icon)
	writeUint16LE(&out, uint16(len(sizes)))

	headerSize := 6 + 16*len(sizes)
	offset := headerSize
	for i, s := range sizes {
		wByte := byte(s)
		if s >= 256 {
			wByte = 0
		}
		hByte := byte(s)
		if s >= 256 {
			hByte = 0
		}

		// ICONDIRENTRY
		out.WriteByte(wByte)
		out.WriteByte(hByte)
		out.WriteByte(0)        // color count
		out.WriteByte(0)        // reserved
		out.Write([]byte{1, 0}) // planes
		out.Write([]byte{32, 0}) // bit count
		writeUint32LE(&out, uint32(len(pngs[i])))
		writeUint32LE(&out, uint32(offset))
		offset += len(pngs[i])
	}

	for _, p := range pngs {
		out.Write(p)
	}

	return out.Bytes(), nil
}
