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
	cmd := exec.Command("ffprobe", "-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height,duration",
		"-of", "csv=p=0",
		"-read_intervals", "%0:5", // Only analyze first 5 seconds
		filePath)
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

// TrimVideoTo30Seconds trims a video file to the first 30 seconds using ffmpeg
func TrimVideoTo30Seconds(inputPath, outputPath string) error {
	logrus.Infof("Trimming video to 30 seconds: %s -> %s", inputPath, outputPath)

	// Check if FFmpeg is available
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		logrus.Errorf("FFmpeg not found: %v", err)
		return fmt.Errorf("ffmpeg is not installed: %w", err)
	}
	logrus.Infof("Using FFmpeg at path: %s", ffmpegPath)

	// Build ffmpeg command to trim video to first 30 seconds
	// -i: input file
	// -t 30: duration of 30 seconds
	// -c copy: copy streams without re-encoding (faster)
	// -avoid_negative_ts make_zero: handle timestamp issues
	cmd := exec.Command(ffmpegPath, 
		"-i", inputPath,
		"-t", "30",
		"-c", "copy",
		"-avoid_negative_ts", "make_zero",
		"-y", // overwrite output file if it exists
		outputPath,
	)

	// Capture output for debugging
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	logrus.Infof("Running ffmpeg command: %s", cmd.String())
	
	if err := cmd.Run(); err != nil {
		logrus.Errorf("FFmpeg command failed: %v, stderr: %s", err, stderr.String())
		return fmt.Errorf("ffmpeg failed to trim video: %w, stderr: %s", err, stderr.String())
	}

	// Verify the output file was created
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return fmt.Errorf("trimmed video file was not created: %s", outputPath)
	}

	logrus.Infof("Successfully trimmed video to 30 seconds: %s", outputPath)
	return nil
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
