package tray

import (
	"encoding/binary"
	"image"
	"image/color"
)

// KeyIcon is an authored, high-contrast key, shared by every connectivity state.
// Text in the menu and tooltip carries the status as well as the icon color.
func KeyIcon(s Snapshot) *image.NRGBA {
	bg := color.NRGBA{R: 88, G: 97, B: 108, A: 255}
	if s.Proxy {
		bg = color.NRGBA{R: 153, G: 97, B: 0, A: 255}
		if s.Healthy {
			bg = color.NRGBA{R: 23, G: 114, B: 69, A: 255}
		}
	}
	im := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			dx, dy := float64(x)-15.5, float64(y)-15.5
			if dx*dx+dy*dy <= 225 {
				im.SetNRGBA(x, y, bg)
			}
			r := (x-10)*(x-10) + (y-14)*(y-14)
			ring := r >= 9 && r <= 30
			shaft := x >= 14 && x <= 25 && y >= 13 && y <= 15
			teeth := ((x >= 20 && x <= 22) || (x >= 24 && x <= 25)) && y >= 15 && y <= 20
			if ring || shaft || teeth {
				im.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
			}
		}
	}
	return im
}

func iconDIB(s Snapshot) []byte {
	// BITMAPINFOHEADER, bottom-up BGRA pixels, then a 1-bit transparency mask.
	data := make([]byte, 40+32*32*4+32*4)
	put := func(offset int, value uint32) { binary.LittleEndian.PutUint32(data[offset:], value) }
	put(0, 40)
	put(4, 32)
	put(8, 64) // XOR + AND bitmap height
	binary.LittleEndian.PutUint16(data[12:], 1)
	binary.LittleEndian.PutUint16(data[14:], 32)
	put(20, 32*32*4)
	im := KeyIcon(s)
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			c := im.NRGBAAt(x, 31-y)
			i := 40 + (y*32+x)*4
			data[i], data[i+1], data[i+2], data[i+3] = c.B, c.G, c.R, c.A
			if c.A == 0 {
				data[40+32*32*4+y*4+x/8] |= 1 << (7 - x%8)
			}
		}
	}
	return data
}
