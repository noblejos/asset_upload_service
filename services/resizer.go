package services

import (
	"bytes"
	"fmt"
	"log"
	"math"
	"sync"

	"github.com/disintegration/imaging"
	ffmpeg "github.com/u2takey/ffmpeg-go"
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
	targetFormat, err := r.validateFormat(format)
	if err != nil {
		return err
	}

	log.Printf("Processing video: %s to %s with format %s", inputPath, outputPath, targetFormat.FormattedRatio)

	scaleFilter := fmt.Sprintf("scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease,pad=%d:%d:(%d-iw*min(1\\,min(%d/iw\\,%d/ih)))/2:(%d-ih*min(1\\,min(%d/iw\\,%d/ih)))/2", targetFormat.Width, targetFormat.Height, targetFormat.Width, targetFormat.Height, targetFormat.Width, targetFormat.Width, targetFormat.Height, targetFormat.Height, targetFormat.Width, targetFormat.Height)

	// Try hardware acceleration if available
	var videoCodec string
	if r.checkHardwareAcceleration() {
		videoCodec = "h264_nvenc" // NVIDIA GPU example; adjust for your hardware
	} else {
		videoCodec = "libx264"
	}

	args := ffmpeg.KwArgs{
		"vf":       scaleFilter,
		"c:v":      videoCodec,
		"crf":      fmt.Sprintf("%d", r.calculateCRF()),
		"preset":   "ultrafast",
		"c:a":      "aac",
		"movflags": "faststart",
		"threads":  "1",
	}

	errBuf := pool.Get().(*bytes.Buffer)
	errBuf.Reset()
	defer pool.Put(errBuf)

	err = ffmpeg.Input(inputPath).
		Output(outputPath, args).
		OverWriteOutput().
		WithErrorOutput(errBuf).
		Run()

	if err != nil {
		return fmt.Errorf("ffmpeg error: %w\nLogs:\n%s", err, errBuf.String())
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
