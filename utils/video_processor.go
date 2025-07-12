package utils

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/h2non/filetype"
	"github.com/sirupsen/logrus"
	ffmpeg "github.com/u2takey/ffmpeg-go"
)

// ProcessVideoWithBitrateReduction compresses a video by reducing its bitrate without changing resolution
func ProcessVideoWithBitrateReduction(inputPath string) (string, bool, error) {
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
	} // Get video metadata including dimensions
	_, err = GetVideoMetadata(inputPath)
	if err != nil {
		logrus.Warnf("Failed to get video metadata: %v, proceeding with conversion anyway", err)
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

	// Try a simpler ffmpeg command first to check if the input file is valid
	probeCmd := exec.Command(ffmpegPath, "-i", inputPath, "-f", "null", "-")
	probeOutput, probeErr := probeCmd.CombinedOutput()
	if probeErr != nil {
		logrus.Errorf("FFmpeg probe failed: %v, output: %s", probeErr, string(probeOutput))
		return "", false, fmt.Errorf("failed to process video - input file may be corrupted: %w", probeErr)
	}

	// Process video with ffmpeg to reduce bitrate while maintaining original resolution
	logrus.Infof("Starting video processing with bitrate reduction (original resolution maintained)")

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
		logrus.Errorf("Failed to process video: %v", err)
		// Try a more basic conversion as a fallback
		logrus.Infof("Trying fallback conversion with simpler settings")

		// Fallback with simpler settings but still maintaining resolution
		fallbackCmd := exec.Command(ffmpegPath,
			"-i", inputPath,
			"-t", "59",
			"-c:v", "libx264",
			"-preset", "ultrafast",
			"-crf", "30", // Even higher CRF for more bitrate reduction
			"-c:a", "aac",
			"-b:a", "96k", // Lower audio bitrate
			"-pix_fmt", "yuv420p",
			"-y", outputPath)

		logrus.Infof("Running fallback FFmpeg command")
		fallbackOutput, fallbackErr := fallbackCmd.CombinedOutput()
		if fallbackErr != nil {
			logrus.Errorf("Fallback conversion also failed: %v, output: %s", fallbackErr, string(fallbackOutput))
			return "", false, fmt.Errorf("failed to process video (all methods): %w", fallbackErr)
		}

		logrus.Infof("Fallback conversion succeeded")
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
