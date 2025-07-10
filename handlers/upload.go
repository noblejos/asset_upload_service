package handlers

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/asset_upload_service/models"
	"github.com/asset_upload_service/services"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/gin-gonic/gin"

	"github.com/asset_upload_service/utils"
)

type UploadHandler struct{}

func NewUploadHandler() *UploadHandler {
	return &UploadHandler{}
}

func (h *UploadHandler) HandleUpload(c *gin.Context) { // Parse form data (10MB max)
	if err := c.Request.ParseMultipartForm(10 << 20); err != nil {
		c.JSON(http.StatusBadRequest, models.UploadResponse{
			Message: "Failed to parse multipart form: " + err.Error(),
		})
		return
	}

	resizer := services.NewResizer(90)
	awsConfig := models.UploadRequest{
		AWSAccessKeyID:     os.Getenv("AWS_ACCESS_KEY_ID"),
		AWSSecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
		AWSRegion:          os.Getenv("AWS_REGION"),
		S3BucketName:       os.Getenv("AWS_S3_BUCKET"),
	}

	// Validate AWS credentials
	if awsConfig.AWSAccessKeyID == "" || awsConfig.AWSSecretAccessKey == "" ||
		awsConfig.AWSRegion == "" || awsConfig.S3BucketName == "" {
		c.JSON(http.StatusBadRequest, models.UploadResponse{
			Message: "AWS credentials and configuration are required",
		})
		return
	}

	// Get the file from form data
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, models.UploadResponse{
			Message: "Failed to get file from form data: " + err.Error(),
		})
		return
	}
	defer file.Close()

	// Read file into memory
	fileBytes, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.UploadResponse{
			Message: "Failed to read file: " + err.Error(),
		})
		return
	}
	// Get file type without processing
	fileType := http.DetectContentType(fileBytes)
	var fileInfo *models.FileInfo

	if strings.HasPrefix(fileType, "image/") { // Just get image dimensions without processing
		dimensions, err := utils.GetImageDimensions(fileBytes)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.UploadResponse{
				Message: "Failed to get image dimensions: " + err.Error(),
			})
			return
		}

		// Calculate original aspect ratio
		ratio := float64(dimensions.Width) / float64(dimensions.Height)

		// Get the closest standard aspect ratio without resizing
		standardFormat := resizer.DetectFormat(dimensions.Width, dimensions.Height)

		fileInfo = &models.FileInfo{
			FileType:      "image",
			Width:         dimensions.Width,
			Height:        dimensions.Height,
			OriginalRatio: ratio,
			MatchedFormat: standardFormat,
		}
	} else if strings.HasPrefix(fileType, "video/") || utils.IsVideoFile(header.Filename) {
		// Save temp file for video metadata extraction and potential conversion
		tempPath := filepath.Join(os.TempDir(), header.Filename)
		if err := os.WriteFile(tempPath, fileBytes, 0644); err != nil {
			c.JSON(http.StatusInternalServerError, models.UploadResponse{
				Message: "Failed to create temp video file: " + err.Error(),
			})
			return
		}
		defer os.Remove(tempPath)
		// Get path for metadata extraction (will be either original or processed)
		metadataPath := tempPath
		var wasProcessed bool

		// Process video: detect quality, convert to low quality, cut to 59 seconds, convert to MP4
		processedPath, processed, err := utils.ProcessVideo(tempPath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.UploadResponse{
				Message: "Failed to process video: " + err.Error(),
			})
			return
		}

		wasProcessed = processed

		// If processing happened, make sure to clean up the processed file too
		if wasProcessed {
			defer os.Remove(processedPath)

			// Read the processed file to update fileBytes
			fileBytes, err = os.ReadFile(processedPath)
			if err != nil {
				c.JSON(http.StatusInternalServerError, models.UploadResponse{
					Message: "Failed to read processed video: " + err.Error(),
				})
				return
			}

			// Update the filename to have .mp4 extension
			header.Filename = strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename)) + "_processed.mp4"
			fileType = "video/mp4" // Update the file type since we processed it
			metadataPath = processedPath
		}

		// Get metadata from the video (original or converted)
		dimensions, err := utils.GetVideoMetadata(metadataPath)
		if err != nil {
			// If we can't get metadata, continue with basic info
			fileInfo = &models.FileInfo{
				FileType: "video",
			}
		} else {
			// Calculate original aspect ratio
			ratio := float64(dimensions.Width) / float64(dimensions.Height)

			// Get the closest standard aspect ratio without resizing
			standardFormat := resizer.DetectFormat(dimensions.Width, dimensions.Height)

			fileInfo = &models.FileInfo{
				FileType:      "video",
				Width:         dimensions.Width,
				Height:        dimensions.Height,
				OriginalRatio: ratio,
				MatchedFormat: standardFormat,
				Duration:      dimensions.Duration,
				// VideoCodec:    dimensions.VideoCodec,
				// AudioCodec:    dimensions.AudioCodec,
				// FrameRate:     dimensions.FrameRate,
			}
		}
	} else {
		// Allow other file types to be uploaded without processing
		fileInfo = &models.FileInfo{
			FileType: fileType,
		}
	}
	// Upload to S3
	// Create a temporary file to store file bytes
	tempFile, err := os.CreateTemp("", "upload-*")
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.UploadResponse{
			Message: "Failed to create temporary file: " + err.Error(),
		})
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Write original file bytes to temp file
	if _, err := tempFile.Write(fileBytes); err != nil {
		c.JSON(http.StatusInternalServerError, models.UploadResponse{
			Message: "Failed to write to temporary file: " + err.Error(),
		})
		return
	}

	// Seek to beginning of file for reading
	if _, err := tempFile.Seek(0, 0); err != nil {
		c.JSON(http.StatusInternalServerError, models.UploadResponse{
			Message: "Failed to seek temporary file: " + err.Error(),
		})
		return
	}

	fileURL, err := h.uploadToS3(tempFile, header.Filename, awsConfig)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.UploadResponse{
			Message: "Failed to upload to S3: " + err.Error(),
		})
		return
	} // Prepare response
	message := "File uploaded successfully without processing"
	// Track video processing for message
	originalExt := c.Request.FormValue("originalExt")
	if strings.Contains(header.Filename, "_processed") && strings.HasSuffix(header.Filename, ".mp4") {
		message = "Video was processed: quality reduced, cut to 59 seconds, and converted to MP4 format"
	} else if strings.HasSuffix(header.Filename, ".mp4") &&
		(originalExt != "" || strings.HasPrefix(fileInfo.FileType, "video/")) {
		message = "Video converted to MP4 and uploaded successfully"
	}

	response := models.UploadResponse{
		FileName:      header.Filename,
		FileURL:       fileURL,
		FileType:      fileInfo.FileType,
		FileSize:      int64(len(fileBytes)),
		Width:         fileInfo.Width,
		Height:        fileInfo.Height,
		OriginalRatio: fileInfo.OriginalRatio,
		MatchedFormat: fileInfo.MatchedFormat,
		AspectRatio:   fileInfo.MatchedFormat,
		Duration:      fileInfo.Duration,
		Message:       message,
	}

	c.JSON(http.StatusOK, response)
}

func (h *UploadHandler) uploadToS3(file *os.File, fileName string, config models.UploadRequest) (string, error) {
	// Create AWS session
	sess, err := session.NewSession(&aws.Config{
		Region: aws.String(config.AWSRegion),
		Credentials: credentials.NewStaticCredentials(
			config.AWSAccessKeyID,
			config.AWSSecretAccessKey,
			"",
		),
	})
	if err != nil {
		return "", fmt.Errorf("failed to create AWS session: %v", err)
	}

	// Create an uploader with the session and default options
	uploader := s3manager.NewUploader(sess)

	// Upload the file to S3
	result, err := uploader.Upload(&s3manager.UploadInput{
		Bucket: aws.String(config.S3BucketName),
		Key:    aws.String(fileName),
		Body:   file,
		ACL:    aws.String("public-read"), // Set ACL to public-read if needed
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload file: %v", err)
	}

	return result.Location, nil
}

// getImageDimensions moved to utils package to avoid duplication
