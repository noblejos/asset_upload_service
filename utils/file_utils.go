package utils

import (
	"fmt"
	"os"
	"os/exec"
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
