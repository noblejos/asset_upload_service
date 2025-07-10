package utils

import (
	"bytes"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/disintegration/imaging"
	"github.com/h2non/filetype"
	"github.com/sirupsen/logrus"

	"github.com/asset_upload_service/models"

	ffmpeg "github.com/u2takey/ffmpeg-go"
)

func ProcessFile(filePath string) (*models.FileInfo, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %v", err)
	}
	defer file.Close()

	// Read first 261 bytes for file type detection
	head := make([]byte, 261)
	if _, err := file.Read(head); err != nil {
		return nil, fmt.Errorf("failed to read file header: %v", err)
	}

	kind, err := filetype.Match(head)
	if err != nil {
		return nil, fmt.Errorf("failed to determine file type: %v", err)
	}

	info := &models.FileInfo{
		FileType: kind.MIME.Value,
	}

	// Process based on file type
	switch {
	case strings.HasPrefix(kind.MIME.Value, "image/"):
		if err := processImage(filePath, info); err != nil {
			return nil, fmt.Errorf("image processing failed: %v", err)
		}
	case strings.HasPrefix(kind.MIME.Value, "video/"):
		if err := processVideo(filePath, info); err != nil {
			logrus.Warnf("Video processing failed: %v", err)
			// Continue without video metadata if processing fails
		}
	// Add document processing if needed
	default:
		// For documents, we don't process dimensions
	}

	return info, nil
}

func processImage(filePath string, info *models.FileInfo) error {
	img, err := imaging.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open image: %v", err)
	}

	bounds := img.Bounds()
	info.Width = bounds.Dx()
	info.Height = bounds.Dy()
	info.AspectRatio = calculateAspectRatio(info.Width, info.Height)

	return nil
}

func processVideo(filePath string, info *models.FileInfo) error {
	// Get video metadata using ffprobe
	_, err := ffmpeg.Probe(filePath)
	if err != nil {
		return fmt.Errorf("failed to probe video: %w", err)
	}

	// Parse width and height
	cmd := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height,duration", "-of", "csv=p=0", filePath)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get video metadata: %w", err)
	}

	// Output format: width,height,duration
	parts := strings.Split(strings.TrimSpace(string(out)), ",")
	if len(parts) < 3 {
		return fmt.Errorf("unexpected ffprobe output format")
	}

	info.Width, _ = strconv.Atoi(parts[0])
	info.Height, _ = strconv.Atoi(parts[1])
	duration, _ := strconv.ParseFloat(parts[2], 64)
	info.Duration = duration
	info.AspectRatio = calculateAspectRatio(info.Width, info.Height)

	return nil
}

func calculateAspectRatio(width, height int) string {
	if width == 0 || height == 0 {
		return "0:0"
	}

	gcd := func(a, b int) int {
		for b != 0 {
			a, b = b, a%b
		}
		return a
	}

	divisor := gcd(width, height)
	return fmt.Sprintf("%d:%d", width/divisor, height/divisor)
}

type Dimensions struct {
	Width    int
	Height   int
	Duration float64
}

func GetVideoMetadata(filePath string) (Dimensions, error) {
	// Get video metadata using ffprobe
	_, err := ffmpeg.Probe(filePath)
	if err != nil {
		return Dimensions{}, fmt.Errorf("failed to probe video: %w", err)
	}

	// Parse width and height
	cmd := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height,duration", "-of", "csv=p=0", filePath)
	out, err := cmd.Output()
	if err != nil {

		return Dimensions{}, fmt.Errorf("failed to get video metadata: %w", err)
	}

	// Output format: width,height,duration
	parts := strings.Split(strings.TrimSpace(string(out)), ",")
	if len(parts) < 3 {
		return Dimensions{}, fmt.Errorf("unexpected ffprobe output format")
	}

	width, _ := strconv.Atoi(parts[0])
	height, _ := strconv.Atoi(parts[1])
	duration, _ := strconv.ParseFloat(parts[2], 64)

	return Dimensions{width, height, duration}, nil
}

// List of common video extensions
var videoExtensions = map[string]bool{
	".mp4":  true,
	".mov":  true,
	".avi":  true,
	".wmv":  true,
	".flv":  true,
	".webm": true,
	".mkv":  true,
	".m4v":  true,
	".3gp":  true,
	".ogg":  true,
	".ogv":  true,
	".mpg":  true,
	".mpeg": true,
	".ts":   true,
	".mts":  true,
	".vob":  true,
	".divx": true,
	".m2ts": true,
	".mxf":  true,
	".asf":  true,
	".rm":   true,
	".rmvb": true,
	".dv":   true,
	".f4v":  true,
}

// IsVideoFile checks if a file is a video based on its extension
func IsVideoFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	return videoExtensions[ext]
}

// DetectVideoQuality returns the estimated quality level of a video (low, medium, high)
// and actual resolution width and height
func DetectVideoQuality(filePath string) (string, int, int, error) {
	// Get video dimensions using ffprobe
	dimensions, err := GetVideoMetadata(filePath)
	if err != nil {
		return "", 0, 0, fmt.Errorf("failed to detect video quality: %w", err)
	}

	width, height := dimensions.Width, dimensions.Height

	// Determine quality based on resolution
	var quality string
	if width >= 1920 || height >= 1080 {
		quality = "high"
	} else if width >= 1280 || height >= 720 {
		quality = "medium"
	} else {
		quality = "low"
	}

	logrus.Infof("Video quality detected: %s (%dx%d)", quality, width, height)
	return quality, width, height, nil
}

func ProcessVideo(inputPath string) (string, bool, error) {
	// First check if it's a video
	if !IsVideoFile(inputPath) {
		// Use filetype library to check if it's a video
		file, err := os.Open(inputPath)
		if err != nil {
			return "", false, fmt.Errorf("failed to open file for type detection: %w", err)
		}
		defer file.Close()

		// Read enough bytes for detection
		head := make([]byte, 261)
		if _, err := file.Read(head); err != nil {
			return "", false, fmt.Errorf("failed to read file header: %w", err)
		}

		kind, err := filetype.Match(head)
		if err != nil || !strings.HasPrefix(kind.MIME.Value, "video/") {
			// Not a video or unrecognized format
			return inputPath, false, nil
		}
	}

	// 1. Detect quality
	quality, width, height, err := DetectVideoQuality(inputPath)
	if err != nil {
		logrus.Warnf("Failed to detect video quality: %v, proceeding with conversion", err)
	}

	// Generate output path
	outputPath := strings.TrimSuffix(inputPath, filepath.Ext(inputPath)) + "_processed.mp4"
	logrus.Infof("Processing video from %s to %s (quality: %s)", inputPath, outputPath, quality)

	// Check if FFmpeg is available
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return "", false, fmt.Errorf("ffmpeg is not installed: %w", err)
	}

	// 2-4. Convert to low quality, cut to 59 seconds, and convert to MP4
	// Determine target resolution based on detected quality
	targetWidth := 640  // Default low quality width
	targetHeight := 360 // Default low quality height

	// Maintain aspect ratio if original video is available
	if width > 0 && height > 0 {
		aspectRatio := float64(width) / float64(height)
		targetHeight = int(float64(targetWidth) / aspectRatio)
	}

	// Process video with ffmpeg
	err = ffmpeg.Input(inputPath).
		Output(outputPath, ffmpeg.KwArgs{
			"t":        "59",                                                  // Cut to 59 seconds
			"vf":       fmt.Sprintf("scale=%d:%d", targetWidth, targetHeight), // Resize to low quality
			"c:v":      "libx264",                                             // Use H.264 codec for video
			"preset":   "slow",                                                // Better compression
			"crf":      "28",                                                  // Lower quality (higher CRF value)
			"c:a":      "aac",                                                 // Use AAC codec for audio
			"b:a":      "64k",                                                 // Lower audio bitrate
			"movflags": "+faststart",                                          // Optimize for web playback
			"pix_fmt":  "yuv420p",                                             // Pixel format for maximum compatibility
		}).
		OverWriteOutput().
		Run()

	if err != nil {
		return "", false, fmt.Errorf("failed to process video: %w", err)
	}

	return outputPath, true, nil
}

func ConvertVideoToMP4(inputPath string) (string, bool, error) {
	// This function is now a wrapper around the more comprehensive ProcessVideo function
	// to maintain backward compatibility
	return ProcessVideo(inputPath)
}

// GetImageDimensions extracts width and height from image bytes
func GetImageDimensions(buffer []byte) (struct{ Width, Height int }, error) {
	img, _, err := image.DecodeConfig(bytes.NewReader(buffer))
	if err != nil {
		return struct{ Width, Height int }{}, err
	}
	return struct{ Width, Height int }{img.Width, img.Height}, nil
}
