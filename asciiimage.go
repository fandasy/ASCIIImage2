package asciiimage

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"path/filepath"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/webp"

	"github.com/fandasy/ASCIIimage/resize"
	"github.com/fandasy/ASCIIimage/validate"
)

const (
	// 1 	 = 10px
	// 10000 = 100000px

	maxWidth  uint = 10000
	maxHeight uint = 10000

	// dark ... light

	defaultChars = "@%#*+=:~-. "
)

var (
	ErrFileNotFound    = errors.New("file not found")
	ErrPageNotFound    = errors.New("page not found")
	ErrIncorrectFormat = errors.New("incorrect format")
	ErrIncorrectUrl    = errors.New("incorrect url")
)

type Options struct {
	Compress  uint8
	MaxWidth  uint
	MaxHeight uint
	Chars     string
}

// GetFromFile reads an image from a file and converts it to an ASCII art image.
//
// Possible output errors:
// ErrFileNotFound,
// ErrIncorrectFormat
func GetFromFile(ctx context.Context, path string, opts Options) (*image.RGBA, error) {
	const fn = "ascii_image.GetFromFile"

	ext := filepath.Ext(path)
	if !validate.ContentType(ext, ".png", ".jpg", ".jpeg", ".webp") {
		return nil, fmt.Errorf("%s: %w: %s", fn, ErrIncorrectFormat, ext)
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fn, ErrFileNotFound)
	}

	defer file.Close()

	var img image.Image

	switch ext {
	case ".png":
		img, err = png.Decode(file)
	case ".jpg", ".jpeg":
		img, err = jpeg.Decode(file)
	case ".webp":
		img, err = webp.Decode(file)
	}

	if err != nil {
		return nil, fmt.Errorf("%s: %w", fn, err)
	}

	return getASCIIImage(ctx, img, opts)
}

// GetFromWebsite downloads an image from a URL and converts it to an ASCII art image.
//
// Possible output errors:
// ErrIncorrectUrl,
// ErrPageNotFound,
// ErrIncorrectFormat
func GetFromWebsite(ctx context.Context, url string, opts Options) (*image.RGBA, error) {
	const fn = "ascii_image.GetFromWebsite"

	if !validate.URL(url) {
		return nil, ErrIncorrectUrl
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fn, err)
	}

	req.Close = true

	resp, err := http.DefaultClient.Do(req)
	if resp != nil {
		defer resp.Body.Close()
	}

	if err != nil {
		return nil, fmt.Errorf("%s: %w", fn, err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s: %w", fn, ErrPageNotFound)
	}

	contentType := resp.Header.Get("Content-Type")

	if !validate.ContentType(contentType, "image/png", "image/jpeg", "image/webp") {
		return nil, fmt.Errorf("%s: %w: %s", fn, ErrIncorrectFormat, contentType)
	}

	var img image.Image

	switch contentType {
	case "image/png":
		img, err = png.Decode(resp.Body)
	case "image/jpeg":
		img, err = jpeg.Decode(resp.Body)
	case "image/webp":
		img, err = webp.Decode(resp.Body)
	}

	if err != nil {
		return nil, fmt.Errorf("%s: %w", fn, err)
	}

	return getASCIIImage(ctx, img, opts)

}

// getASCIIImage processes the image and generates ASCII art.
func getASCIIImage(ctx context.Context, img image.Image, opts Options) (*image.RGBA, error) {

	compressionPercentage := clampCompressionPercentage(opts.Compress)

	// Clamp MaxWidth and MaxHeight to valid ranges
	if opts.MaxWidth == 0 {
		opts.MaxWidth = maxWidth
	}
	if opts.MaxHeight == 0 {
		opts.MaxHeight = maxHeight
	}

	// Default characters for ASCII art
	if opts.Chars == "" {
		opts.Chars = defaultChars
	}

	bounds := img.Bounds()
	width := uint(bounds.Max.X)
	height := uint(bounds.Max.Y)

	if width > opts.MaxWidth {
		width = opts.MaxWidth
	}
	if height > opts.MaxHeight {
		height = opts.MaxHeight
	}

	compressionFactor := uint(100 - compressionPercentage)

	newWidth := (width * (compressionFactor)) / 100
	newHeight := (height * (compressionFactor)) / 100

	img = resize.Resize(newWidth, newHeight, img)

	return generateASCIIImage(ctx, img, opts.Chars)
}

func clampCompressionPercentage(v uint8) uint8 {
	if v >= 100 {
		v = 0
	}

	return v
}

// generateASCIIImage converts the image to ASCII art.
func generateASCIIImage(ctx context.Context, img image.Image, chars string) (*image.RGBA, error) {
	const fn = "ascii_image.generateASCIIImage"

	resCh := make(chan *image.RGBA)

	go func() {
		bounds := img.Bounds()
		asciiWidth := bounds.Max.X
		asciiHeight := bounds.Max.Y

		asciiImg := image.NewRGBA(image.Rect(0, 0, asciiWidth*10, asciiHeight*10))

		draw.Draw(asciiImg, asciiImg.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)

		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				c := img.At(x, y)
				char := getCharFromBrightness(c, chars)

				point := fixed.Point26_6{X: fixed.I(x * 10), Y: fixed.I(y * 10)}
				d := &font.Drawer{
					Dst:  asciiImg,
					Src:  image.NewUniform(color.Black),
					Face: basicfont.Face7x13,
					Dot:  point,
				}
				d.DrawString(char)
			}
		}

		resCh <- asciiImg
	}()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("%s: %w", fn, ctx.Err())

	case res := <-resCh:
		return res, nil
	}
}

// getCharFromBrightness maps a color's brightness to a character.
func getCharFromBrightness(c color.Color, chars string) string {
	r, g, b, _ := c.RGBA()

	brightness := (r>>8 + g>>8 + b>>8) / 3
	idx := int(float64(brightness) / 255 * float64(len(chars)-1))

	return string(chars[clamp(idx, 0, len(chars)-1)])
}

// clamp ensures a value is within a specified range.
func clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
