// png2ico converts a set of PNGs (named icon-256.png / icon-48.png /
// icon-32.png / icon-16.png) into a DIB-based multi-size .ico file.
//
// DIB (uncompressed) icon entries are used on purpose: PNG-compressed .ico
// entries are not decoded by every loader (LoadImageW from file, .NET).
//
// Usage: go run ./tools/png2ico -src <dir> -out <file.ico>
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"sort"
)

func main() {
	src := flag.String("src", "", "directory containing icon-{256,48,32,16}.png")
	out := flag.String("out", "", "output .ico path")
	flag.Parse()
	if *src == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: png2ico -src <dir> -out <file.ico>")
		os.Exit(2)
	}

	var entries []entry
	for _, size := range []int{256, 48, 32, 16} {
		p := filepath.Join(*src, fmt.Sprintf("icon-%d.png", size))
		f, err := os.Open(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "png2ico: %v\n", err)
			os.Exit(1)
		}
		img, err := png.Decode(f)
		f.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "png2ico: decode %s: %v\n", p, err)
			os.Exit(1)
		}
		entries = append(entries, entry{size: size, payload: dibEntry(img)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].size > entries[j].size })

	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint16(0)) // reserved
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // type: icon
	binary.Write(&buf, binary.LittleEndian, uint16(len(entries)))
	offset := 6 + 16*len(entries)
	for _, e := range entries {
		wh := byte(e.size)
		if e.size >= 256 {
			wh = 0
		}
		binary.Write(&buf, binary.LittleEndian, wh)
		binary.Write(&buf, binary.LittleEndian, wh)
		binary.Write(&buf, binary.LittleEndian, byte(0))
		binary.Write(&buf, binary.LittleEndian, byte(0))
		binary.Write(&buf, binary.LittleEndian, uint16(1))  // planes
		binary.Write(&buf, binary.LittleEndian, uint16(32)) // bpp
		binary.Write(&buf, binary.LittleEndian, uint32(len(e.payload)))
		binary.Write(&buf, binary.LittleEndian, uint32(offset))
		offset += len(e.payload)
	}
	for _, e := range entries {
		buf.Write(e.payload)
	}
	if err := os.WriteFile(*out, buf.Bytes(), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "png2ico: write: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("png2ico: wrote %s (%d bytes, %d entries)\n", *out, buf.Len(), len(entries))
}

type entry struct {
	size    int
	payload []byte
}

// dibEntry encodes an image as an icon DIB: BITMAPINFOHEADER (biHeight = 2*h)
// + BGRA XOR rows (bottom-up) + an all-zero 1bpp AND mask (alpha lives in the
// 32bpp XOR data).
func dibEntry(img image.Image) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()

	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint32(40)) // biSize
	binary.Write(&buf, binary.LittleEndian, int32(w))   // biWidth
	binary.Write(&buf, binary.LittleEndian, int32(h*2)) // biHeight
	binary.Write(&buf, binary.LittleEndian, uint16(1))  // biPlanes
	binary.Write(&buf, binary.LittleEndian, uint16(32)) // biBitCount
	binary.Write(&buf, binary.LittleEndian, uint32(0))  // biCompression
	binary.Write(&buf, binary.LittleEndian, uint32(w*h*4))
	binary.Write(&buf, binary.LittleEndian, int32(0))
	binary.Write(&buf, binary.LittleEndian, int32(0))
	binary.Write(&buf, binary.LittleEndian, uint32(0))
	binary.Write(&buf, binary.LittleEndian, uint32(0))

	// XOR data: BGRA, bottom-up rows.
	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			r, g, bl, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			buf.WriteByte(byte(bl >> 8))
			buf.WriteByte(byte(g >> 8))
			buf.WriteByte(byte(r >> 8))
			buf.WriteByte(byte(a >> 8))
		}
	}
	// AND mask: all zero, row-padded to 4 bytes.
	andRow := make([]byte, (w+31)/32*4)
	for y := 0; y < h; y++ {
		buf.Write(andRow)
	}
	return buf.Bytes()
}
