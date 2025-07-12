package utils

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/disintegration/imaging"
	"github.com/h2non/filetype"
	"github.com/sirupsen/logrus"

	"github.com/asset_upload_service/models"
	"github.com/asset_upload_service/services"

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
			logrus.Errorf("Failed to open file for type detection: %v", err)
			return "", false, fmt.Errorf("failed to open file for type detection: %w", err)
		}
		defer file.Close()

		// Read enough bytes for detection
		head := make([]byte, 261)
		if _, err := file.Read(head); err != nil {
			logrus.Errorf("Failed to read file header: %v", err)
			return "", false, fmt.Errorf("failed to read file header: %w", err)
		}

		kind, err := filetype.Match(head)
		if err != nil || !strings.HasPrefix(kind.MIME.Value, "video/") {
			// Not a video or unrecognized format
			logrus.Infof("Not a video or unrecognized format. MIME type: %s", kind.MIME.Value)
			return inputPath, false, nil
		}
	}

	// Log file information
	fileInfo, err := os.Stat(inputPath)
	if err != nil {
		logrus.Warnf("Could not stat file: %v", err)
	} else {
		logrus.Infof("Processing file: %s, size: %d bytes", inputPath, fileInfo.Size())
	}

	// 1. Detect quality and dimensions
	_, width, height, err := DetectVideoQuality(inputPath)
	if err != nil {
		logrus.Warnf("Failed to detect video quality: %v, proceeding with conversion anyway", err)
	}

	// Generate output path
	outputPath := strings.TrimSuffix(inputPath, filepath.Ext(inputPath)) + "_processed.mp4"

	// Check if FFmpeg is available
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		logrus.Errorf("FFmpeg not found: %v", err)
		return "", false, fmt.Errorf("ffmpeg is not installed: %w", err)
	}
	logrus.Infof("Using FFmpeg at path: %s", ffmpegPath)

	// Set target resolution to exactly 480p (854x480) while maintaining aspect ratio
	// This is the standard 480p resolution with 16:9 aspect ratio
	const targetHeight = 480
	// Calculate width maintaining aspect ratio with height=480
	var targetWidth int

	if width > 0 && height > 0 {
		// Calculate original aspect ratio
		origAspectRatio := float64(width) / float64(height)
		targetWidth = int(float64(targetHeight) * origAspectRatio)
		// Ensure width is even (required by some codecs)
		targetWidth = targetWidth + targetWidth%2
		logrus.Infof("Original aspect ratio: %f, calculated target width: %d", origAspectRatio, targetWidth)
	} else {
		// Default to 16:9 aspect ratio if we couldn't get the original dimensions
		targetWidth = 854
		logrus.Infof("Using default 16:9 aspect ratio (854x480)")
	}

	logrus.Infof("Processing video from %s to %s (target resolution: %dx%d)",
		inputPath, outputPath, targetWidth, targetHeight)
	// Try a simpler ffmpeg command first to check if the input file is valid
	probeCmd := exec.Command(ffmpegPath, "-i", inputPath, "-f", "null", "-")
	probeOutput, probeErr := probeCmd.CombinedOutput()
	if probeErr != nil {
		logrus.Errorf("FFmpeg probe failed: %v, output: %s", probeErr, string(probeOutput))
		return "", false, fmt.Errorf("failed to process video - input file may be corrupted: %w", probeErr)
	}
	// Get standard aspect ratios from resizer service
	resizer := services.NewResizer(90)
	formats := services.GetFormats()

	// Find the closest standard aspect ratio
	originalRatio := float64(0)
	if width > 0 && height > 0 {
		originalRatio = float64(width) / float64(height)
	} else {
		originalRatio = float64(16) / float64(9) // Default to 16:9
	}

	var closestFormat services.MediaFormat
	minDiff := math.MaxFloat64

	for _, format := range formats {
		diff := math.Abs(originalRatio - format.AspectRatio)
		if diff < minDiff {
			minDiff = diff
			closestFormat = format
		}
	}

	standardFormat := resizer.DetectFormat(width, height)
	logrus.Infof("Closest standard format: %s (ratio: %s)", standardFormat, closestFormat.FormattedRatio)
	// We're not changing the resolution, so we don't need to calculate standard dimensions
	// This code is left in place but commented out for reference
	/*
		var standardWidth, standardHeight int

		if closestFormat.FormattedRatio == "1:1" { // Square
			standardWidth = 480
			standardHeight = 480
		} else if closestFormat.FormattedRatio == "4:5" { // Portrait
			standardWidth = 384
			standardHeight = 480
		} else if closestFormat.FormattedRatio == "9:16" { // Story/vertical video
			standardWidth = 270
			standardHeight = 480
		} else { // Landscape (1.91:1 or others)
			standardWidth = 854 // 16:9 at 480p
			standardHeight = 480
		}

		// Calculate crop-scale parameters to avoid black bars
		if originalRatio > 0 && width > 0 && height > 0 {
			targetAspectRatio := float64(standardWidth) / float64(standardHeight)

			if originalRatio > targetAspectRatio {
				// Original is wider than target - need to crop width
				// Scale to match target height, then crop sides
				vfCmd = fmt.Sprintf("scale=-1:%d,crop=%d:%d:(iw-ow)/2:0",
					standardHeight, standardWidth, standardHeight)
			} else if originalRatio < targetAspectRatio {
				// Original is taller than target - need to crop height
				// Scale to match target width, then crop top/bottom
				vfCmd = fmt.Sprintf("scale=%d:-1,crop=%d:%d:0:(ih-oh)/2",
					standardWidth, standardWidth, standardHeight)
			} else {
				// Aspect ratios match, just scale
				vfCmd = fmt.Sprintf("scale=%d:%d", standardWidth, standardHeight)
			}
		} else {
			// Default scale if we can't determine aspect ratio
			vfCmd = fmt.Sprintf("scale=%d:%d", standardWidth, standardHeight)
		}
	*/ // Process video with ffmpeg to reduce bitrate while maintaining original resolution
	logrus.Infof("Starting video processing with bitrate reduction")

	// Build the ffmpeg command that maintains resolution but reduces bitrate
	ffmpegCmd := ffmpeg.Input(inputPath).
		Output(outputPath, ffmpeg.KwArgs{
			"t":        "59",         // Cut to 59 seconds
			"c:v":      "libx264",    // Use H.264 codec for video
			"preset":   "medium",     // Use medium preset for better compatibility
			"crf":      "28",         // Higher CRF value = lower bitrate (default is 23, 28 gives significant reduction)
			"c:a":      "aac",        // Use AAC codec for audio
			"b:a":      "128k",       // Reduced audio bitrate
			"movflags": "+faststart", // Optimize for web playback
			"pix_fmt":  "yuv420p",    // Pixel format for maximum compatibility
		}).
		OverWriteOutput()

	// Log the actual command that will be executed
	cmdString := ffmpegCmd.String()
	logrus.Infof("Running FFmpeg command: %s", cmdString)
	// Run the command
	err = ffmpegCmd.Run()
	if err != nil {
		logrus.Errorf("Failed to process video: %v", err) // Try a more basic conversion as a fallback - just reduce bitrate without any scaling
		logrus.Infof("Trying fallback conversion with simpler settings")

		// Use a simpler approach without scaling/cropping for the fallback
		fallbackCmd := exec.Command(ffmpegPath,
			"-i", inputPath,
			"-t", "59", // Cut to 59 seconds
			"-c:v", "libx264", // Use H.264 codec for video
			"-preset", "ultrafast", // Use fastest preset for better compatibility
			"-crf", "30", // Higher CRF value = even lower bitrate
			"-c:a", "aac", // Use AAC codec for audio
			"-b:a", "96k", // Lower audio bitrate
			"-pix_fmt", "yuv420p", // Pixel format for maximum compatibility
			"-y", outputPath)

		logrus.Infof("Running fallback FFmpeg command with higher compression")
		fallbackOutput, fallbackErr := fallbackCmd.CombinedOutput()
		if fallbackErr != nil {
			logrus.Errorf("Fallback conversion also failed: %v, output: %s", fallbackErr, string(fallbackOutput))
			return "", false, fmt.Errorf("failed to process video (all methods): %w", fallbackErr)
		}
		logrus.Infof("Fallback conversion with higher compression succeeded")
		return outputPath, true, nil
	}

	// Check if the output file exists and has non-zero size
	if outInfo, err := os.Stat(outputPath); err != nil {
		logrus.Errorf("Output file doesn't exist after processing: %v", err)
		return "", false, fmt.Errorf("output file not created: %w", err)
	} else if outInfo.Size() == 0 {
		logrus.Errorf("Output file has zero size")
		return "", false, fmt.Errorf("output file has zero size")
	}
	logrus.Infof("Video processing with bitrate reduction completed successfully")
	return outputPath, true, nil
}

// GetImageDimensions extracts width and height from image bytes
func GetImageDimensions(buffer []byte) (struct{ Width, Height int }, error) {
	img, _, err := image.DecodeConfig(bytes.NewReader(buffer))
	if err != nil {
		return struct{ Width, Height int }{}, err
	}
	return struct{ Width, Height int }{img.Width, img.Height}, nil
}

// floatToRatio converts float64 to nearest fraction within max denominator
func FloatToRatio(f float64, maxDenominator int) (num, den int) {
	minDiff := math.MaxFloat64
	for d := 1; d <= maxDenominator; d++ {
		n := int(math.Round(f * float64(d)))
		diff := math.Abs(f - float64(n)/float64(d))
		if diff < minDiff {
			minDiff = diff
			num, den = n, d
		}
	}
	// simplify
	g := gcd(num, den)
	return num / g, den / g
}

// gcd computes the greatest common divisor
func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// GetVideoAspectRatioFromURL retrieves the aspect ratio of a video from a URL (such as S3)
// It downloads the file temporarily to extract metadata then deletes it
func GetVideoAspectRatioFromURL(videoURL string) (*models.VideoAspectRatio, error) {
	logrus.Infof("Getting aspect ratio for video at URL: %s", videoURL)

	// Create a temporary file to store the downloaded video
	tempFile, err := os.CreateTemp("", "video-*.mp4")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary file: %w", err)
	}
	tempFilePath := tempFile.Name()
	tempFile.Close() // Close it now as we'll reopen it for writing

	// Clean up the temp file when done
	defer os.Remove(tempFilePath)

	// Create an HTTP client with timeout
	client := http.Client{
		Timeout: 30 * time.Second,
	}

	// Download just enough of the video to get metadata (first 1MB should be enough)
	req, err := http.NewRequest("GET", videoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Range", "bytes=0-1048576") // First 1MB

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download video: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("failed to download video, status code: %d", resp.StatusCode)
	}

	// Write the downloaded bytes to the temp file
	out, err := os.Create(tempFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file for writing: %w", err)
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to save video file: %w", err)
	}

	// Get video metadata including dimensions
	dimensions, err := GetVideoMetadata(tempFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get video metadata: %w", err)
	}

	// Calculate aspect ratio
	width, height := dimensions.Width, dimensions.Height
	originalRatio := float64(0)
	if width > 0 && height > 0 {
		originalRatio = float64(width) / float64(height)
	} else {
		return nil, fmt.Errorf("invalid video dimensions: width=%d, height=%d", width, height)
	}

	// Convert to formatted ratio (e.g. "16:9")
	num, den := FloatToRatio(originalRatio, 100)
	formattedRatio := fmt.Sprintf("%d:%d", num, den)

	// Get the closest standard format
	resizer := services.NewResizer(90)
	standardFormat := resizer.DetectFormat(width, height)

	return &models.VideoAspectRatio{
		Width:          width,
		Height:         height,
		OriginalRatio:  originalRatio,
		FormattedRatio: formattedRatio,
		StandardFormat: standardFormat,
		Duration:       dimensions.Duration,
	}, nil
}
