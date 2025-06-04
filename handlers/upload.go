package handlers

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/asset_upload_service/models"
	"github.com/asset_upload_service/utils"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/gin-gonic/gin"
)

type UploadHandler struct{}

func NewUploadHandler() *UploadHandler {
	return &UploadHandler{}
}

func (h *UploadHandler) HandleUpload(c *gin.Context) {
	// Parse the form data (10 MB max)
	if err := c.Request.ParseMultipartForm(10 << 20); err != nil {
		c.JSON(http.StatusBadRequest, models.UploadResponse{
			Message: "Failed to parse multipart form: " + err.Error(),
		})
		return
	}

	// log the request body for debugging
	log.Printf("Received upload request: %v", c.Request.Form)

	// Get AWS credentials from form
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

	// Get the file from the form data
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, models.UploadResponse{
			Message: "Failed to get file from form data: " + err.Error(),
		})
		return
	}
	defer file.Close()

	// Create a temporary file
	tempFile, err := os.CreateTemp("", "upload-*"+filepath.Ext(header.Filename))
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.UploadResponse{
			Message: "Failed to create temporary file: " + err.Error(),
		})
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Copy the file content to the temporary file
	if _, err := io.Copy(tempFile, file); err != nil {
		c.JSON(http.StatusInternalServerError, models.UploadResponse{
			Message: "Failed to copy file content: " + err.Error(),
		})
		return
	}

	// Process the file to get metadata (aspect ratio, duration, etc.)
	fileInfo, err := utils.ProcessFile(tempFile.Name())
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.UploadResponse{
			Message: "Failed to process file: " + err.Error(),
		})
		return
	}

	// Reset file reader for S3 upload
	if _, err := tempFile.Seek(0, 0); err != nil {
		c.JSON(http.StatusInternalServerError, models.UploadResponse{
			Message: "Failed to reset file reader: " + err.Error(),
		})
		return
	}

	// Upload to S3
	fileURL, err := h.uploadToS3(tempFile, header.Filename, awsConfig)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.UploadResponse{
			Message: "Failed to upload to S3: " + err.Error(),
		})
		return
	}

	// Prepare response
	response := models.UploadResponse{
		FileName:    header.Filename,
		FileURL:     fileURL,
		FileType:    fileInfo.FileType,
		FileSize:    header.Size,
		Width:       fileInfo.Width,
		Height:      fileInfo.Height,
		AspectRatio: fileInfo.AspectRatio,
		Duration:    fileInfo.Duration,
		Message:     "File uploaded successfully",
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
