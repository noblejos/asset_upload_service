package handlers

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/asset_upload_service/models"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/gin-gonic/gin"

	"github.com/asset_upload_service/services"
	"github.com/asset_upload_service/utils"
)

type UploadHandler struct{}

func NewUploadHandler() *UploadHandler {
	return &UploadHandler{}
}

func (h *UploadHandler) HandleUpload(c *gin.Context) {
	// Initialize resizer with quality setting (85 is good for balance of quality/size)
	resizer := services.NewResizer(85)

	// Parse form data (10MB max)
	if err := c.Request.ParseMultipartForm(10 << 20); err != nil {
		c.JSON(http.StatusBadRequest, models.UploadResponse{
			Message: "Failed to parse multipart form: " + err.Error(),
		})
		return
	}

	// Get AWS credentials from form/env
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

	// Process based on file type
	fileType := http.DetectContentType(fileBytes)
	var processedBytes []byte
	var fileInfo *models.FileInfo

	if strings.HasPrefix(fileType, "image/") {
		// Process image
		dimensions, err := getImageDimensions(fileBytes)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.UploadResponse{
				Message: "Failed to get image dimensions: " + err.Error(),
			})
			return
		}

		format := resizer.DetectFormat(dimensions.Width, dimensions.Height)
		fmt.Println(format)

		processedBytes, err = resizer.ResizeImage(fileBytes, format)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.UploadResponse{
				Message: "Failed to resize image: " + err.Error(),
			})
			return
		}

		fmt.Println(processedBytes)

		fileInfo = &models.FileInfo{
			FileType:      "image",
			Width:         dimensions.Width,
			Height:        dimensions.Height,
			OriginalRatio: float64(dimensions.Width) / float64(dimensions.Height),
			MatchedFormat: format,
		}
	} else if strings.HasPrefix(fileType, "video/") {
		// Save temp file for video processing
		tempPath := filepath.Join(os.TempDir(), header.Filename)
		if err := os.WriteFile(tempPath, fileBytes, 0644); err != nil {
			c.JSON(http.StatusInternalServerError, models.UploadResponse{
				Message: "Failed to create temp video file: " + err.Error(),
			})
			return
		}
		defer os.Remove(tempPath)

		dimensions, err := utils.GetVideoMetadata(tempPath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.UploadResponse{
				Message: "Failed to get image dimensions: " + err.Error(),
			})
			return
		}

		format := resizer.DetectFormat(dimensions.Width, dimensions.Height)
		fmt.Print(format)

		// Process video
		outputPath := tempPath + "_processed.mp4"
		if err := resizer.ProcessVideo(tempPath, outputPath, format); err != nil {
			c.JSON(http.StatusInternalServerError, models.UploadResponse{
				Message: "Failed to process video: " + err.Error(),
			})
			return
		}
		defer os.Remove(outputPath)

		// Read processed video
		processedBytes, err = os.ReadFile(outputPath)
		fmt.Print(err)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.UploadResponse{
				Message: "Failed to read processed video: " + err.Error(),
			})
			return
		}

		fileInfo = &models.FileInfo{
			FileType:      "video",
			Width:         dimensions.Width,
			Height:        dimensions.Height,
			OriginalRatio: float64(dimensions.Width) / float64(dimensions.Height),
			MatchedFormat: format,
		}
	} else {
		c.JSON(http.StatusBadRequest, models.UploadResponse{
			Message: "Unsupported file type",
		})
		return
	}

	// Upload to S3
	// Create a temporary file to store processed bytes
	tempFile, err := os.CreateTemp("", "upload-*")
	if err != nil {
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Write processed bytes to temp file
	if _, err := tempFile.Write(processedBytes); err != nil {
		return
	}

	// Seek to beginning of file for reading
	if _, err := tempFile.Seek(0, 0); err != nil {
		return
	}

	fileURL, err := h.uploadToS3(tempFile, header.Filename, awsConfig)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.UploadResponse{
			Message: "Failed to upload to S3: " + err.Error(),
		})
		return
	}

	// Prepare response
	response := models.UploadResponse{
		FileName:      header.Filename,
		FileURL:       fileURL,
		FileType:      fileInfo.FileType,
		FileSize:      int64(len(processedBytes)),
		Width:         fileInfo.Width,
		Height:        fileInfo.Height,
		OriginalRatio: fileInfo.OriginalRatio,
		MatchedFormat: fileInfo.MatchedFormat,
		AspectRatio:   fileInfo.MatchedFormat,
		Duration:      fileInfo.Duration,
		Message:       "File processed and uploaded successfully",
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

func getImageDimensions(buffer []byte) (struct{ Width, Height int }, error) {
	img, _, err := image.DecodeConfig(bytes.NewReader(buffer))
	if err != nil {
		return struct{ Width, Height int }{}, err
	}
	return struct{ Width, Height int }{img.Width, img.Height}, nil
}
