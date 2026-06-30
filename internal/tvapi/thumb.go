package tvapi

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"io"
	"math"
	"net/http"
	"time"
)

// ChannelThumb downloads the logo at logoURL, samples its dominant colour
// (alpha-weighted average), and returns a PNG with a tinted solid background
// composited behind the logo.
func (c *TVClient) ChannelThumb(logoURL string) ([]byte, error) {

	if logoURL == "" {

		return nil, nil

	}

	client := &http.Client{Timeout: 8 * time.Second}

	req, err := http.NewRequest(http.MethodGet, logoURL, nil)

	if err != nil {

		return nil, err

	}

	req.Header.Set("User-Agent", tvBrowserUA)

	resp, err := client.Do(req)

	if err != nil {

		return nil, err

	}

	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))

	if err != nil {

		return nil, err

	}

	img, _, err := image.Decode(bytes.NewReader(data))

	if err != nil {

		return nil, err

	}

	bg := sampleTintColor(img)

	thumb := image.NewNRGBA(img.Bounds())
	draw.Draw(thumb, thumb.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)
	draw.Draw(thumb, thumb.Bounds(), img, image.Point{}, draw.Over)

	var buf bytes.Buffer

	if err := png.Encode(&buf, thumb); err != nil {

		return nil, err

	}

	return buf.Bytes(), nil

}

// sampleTintColor computes the alpha-weighted average colour of img and
// returns a light or dark tint derived from it — matching the logic in
// logoBackdrop.ts.
func sampleTintColor(img image.Image) color.NRGBA {

	bounds := img.Bounds()

	var sumR, sumG, sumB, sumA float64

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {

		for x := bounds.Min.X; x < bounds.Max.X; x++ {

			// RGBA() returns premultiplied values in [0, 65535].
			// Because we want (actual_channel * alpha), the premultiplied r32
			// already equals that product (scaled by 65535), so we can sum it
			// directly and divide by sumA to recover the weighted average.
			r32, g32, b32, a32 := img.At(x, y).RGBA()

			if float64(a32)/65535 < 0.16 {

				continue

			}

			sumR += float64(r32)
			sumG += float64(g32)
			sumB += float64(b32)
			sumA += float64(a32)

		}

	}

	if sumA == 0 {

		return color.NRGBA{R: 255, G: 255, B: 255, A: 255}

	}

	// Scale from premultiplied [0, 65535] space back to [0, 255].
	ri := uint8(math.Round(sumR / sumA * 255))
	gi := uint8(math.Round(sumG / sumA * 255))
	bi := uint8(math.Round(sumB / sumA * 255))

	return tintForLogo(ri, gi, bi)

}

func tintForLogo(r, g, b uint8) color.NRGBA {

	lum := relativeLuminance(r, g, b)

	if lum < 0.42 {

		return color.NRGBA{

			R: mixChannel(r, 255, 0.88),
			G: mixChannel(g, 255, 0.88),
			B: mixChannel(b, 255, 0.88),
			A: 255,
		}

	}

	return color.NRGBA{

		R: mixChannel(r, 0, 0.82),
		G: mixChannel(g, 0, 0.82),
		B: mixChannel(b, 0, 0.82),
		A: 255,
	}

}

func relativeLuminance(r, g, b uint8) float64 {

	linearize := func(channel uint8) float64 {

		v := float64(channel) / 255

		if v <= 0.03928 {

			return v / 12.92

		}

		return math.Pow((v+0.055)/1.055, 2.4)

	}

	return 0.2126*linearize(r) + 0.7152*linearize(g) + 0.0722*linearize(b)

}

func mixChannel(value, target uint8, amount float64) uint8 {

	result := float64(value) + (float64(target)-float64(value))*amount

	return uint8(math.Round(math.Max(0, math.Min(255, result))))

}
