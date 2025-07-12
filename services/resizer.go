package services

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"sync"

	"github.com/disintegration/imaging"
)

type MediaFormat struct {
	Name           string
	Width          int
	Height         int
	AspectRatio    float64
	FormattedRatio string
}

var (
	formats = []MediaFormat{
		{"square", 1080, 1080, 1.0, "1:1"},        // 1:1
		{"portrait", 1080, 1350, 0.8, "4:5"},      // 4:5
		{"story", 1080, 1920, 0.5625, "9:16"},     // 9:16
		{"landscape", 1080, 608, 1.776, "1.91:1"}, // 1.91:1
	}
)

// GetFormats returns the list of supported media formats
func GetFormats() []MediaFormat {
	return formats
}

type Resizer struct {
	Quality int
}

func NewResizer(quality int) *Resizer {
	return &Resizer{Quality: quality}
}

func (r *Resizer) DetectFormat(width, height int) string {
	originalRatio := float64(width) / float64(height)

	var closestFormat MediaFormat
	minDiff := math.MaxFloat64

	for _, format := range formats {
		diff := math.Abs(originalRatio - format.AspectRatio)
		if diff < minDiff {
			minDiff = diff
			closestFormat = format
		}
	}

	return closestFormat.FormattedRatio
}

func (r *Resizer) ResizeImage(buffer []byte, formatName string) ([]byte, error) {
	var targetFormat MediaFormat
	for _, f := range formats {
		if f.FormattedRatio == formatName {
			targetFormat = f
			break
		}
	}
	if targetFormat.FormattedRatio == "" {
		return nil, fmt.Errorf("invalid format name: %s", formatName)
	}

	// Decode image from buffer
	srcImage, err := imaging.Decode(bytes.NewReader(buffer))
	if err != nil {
		return nil, err
	}

	// Resize and crop
	dstImage := imaging.Resize(srcImage, targetFormat.Width, targetFormat.Height, imaging.Lanczos)

	// Encode to JPEG with quality
	var buf bytes.Buffer
	err = imaging.Encode(&buf, dstImage, imaging.JPEG, imaging.JPEGQuality(r.Quality))
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (r *Resizer) ProcessVideo(inputPath, outputPath, format string) error {
	// Bypass FFmpeg processing: just copy the input file to the output path
	input, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read input video: %w", err)
	}
	err = os.WriteFile(outputPath, input, 0644)
	if err != nil {
		return fmt.Errorf("failed to write output video: %w", err)
	}
	return nil
}

// Check for available hardware acceleration
func (r *Resizer) checkHardwareAcceleration() bool {
	// Implement actual hardware detection
	return false // Default to false, implement based on your environment
}

// Buffer pool to reduce allocations
var pool = sync.Pool{
	New: func() interface{} {
		return bytes.NewBuffer(make([]byte, 0, 1024))
	},
}

func (r *Resizer) calculateCRF() int {

	return 28
}

// Helper: Validate and get the target format
func (r *Resizer) validateFormat(format string) (MediaFormat, error) {
	for _, f := range formats {
		if f.FormattedRatio == format {
			return f, nil
		}
	}
	return MediaFormat{}, fmt.Errorf("invalid format name: %s", format)
}
