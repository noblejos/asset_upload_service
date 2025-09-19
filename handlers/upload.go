package handlers

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/asset_upload_service/models"
	"github.com/asset_upload_service/services"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/asset_upload_service/utils"
)

type UploadHandler struct{}

func NewUploadHandler() *UploadHandler {
	return &UploadHandler{}
}

func (h *UploadHandler) HandleUpload(c *gin.Context) { // Parse form data (10MB max)
	// Log Content-Type header to debug issues with multipart form parsing
	contentType := c.GetHeader("Content-Type")
	logrus.Infof("Received request with Content-Type: %s", contentType)

	// Handle special case when content type might have issues with boundary
	if !strings.Contains(contentType, "boundary=") {
		logrus.Warnf("Content-Type doesn't contain boundary parameter, trying to handle anyway")
	}

	// Try to parse the multipart form
	if err := c.Request.ParseMultipartForm(10 << 20); err != nil {
		logrus.Errorf("Failed to parse multipart form: %v", err)
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
	var message string

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

		num, den := utils.FloatToRatio(ratio, 100)

		ratioStr := fmt.Sprintf("%d:%d", num, den)

		fileInfo = &models.FileInfo{
			FileType:      "image",
			Width:         dimensions.Width,
			Height:        dimensions.Height,
			OriginalRatio: ratioStr, // Use the float64 ratio value here
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
		defer os.Remove(tempPath) // Get path for metadata extraction (will be either original or processed)
		metadataPath := tempPath
		var wasProcessed bool // Process video: reduce bitrate while maintaining original resolution and convert to MP4
		processedPath, processed, err := utils.ProcessVideoWithBitrateReduction(tempPath)
		if err != nil {
			// Log the error for debugging
			fmt.Printf("Video processing error: %v\n", err)

			// Check if it's a format we can handle without processing
			if strings.HasSuffix(strings.ToLower(header.Filename), ".mp4") {
				// If it's already MP4 but processing failed, we can try to use the original
				fmt.Println("Skipping processing for MP4 file that couldn't be converted")
				wasProcessed = false
			} else {
				// For other formats that aren't MP4, we must convert them
				c.JSON(http.StatusInternalServerError, models.UploadResponse{
					Message:  "Failed to process non-MP4 video: " + err.Error(),
					FileType: fileType,
					FileName: header.Filename,
				})
				return
			}
		} else {
			wasProcessed = processed
		}

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

			standardFormat := resizer.DetectFormat(dimensions.Width, dimensions.Height)

			num, den := utils.FloatToRatio(ratio, 100)

			ratioStr := fmt.Sprintf("%d:%d", num, den)

			fileInfo = &models.FileInfo{
				FileType:      "video",
				Width:         dimensions.Width,
				Height:        dimensions.Height,
				OriginalRatio: ratioStr,
				MatchedFormat: standardFormat,
				Duration:      dimensions.Duration,
			}
		}
	} else {
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
	} // Prepare response	message := "File uploaded successfully without processing"
	// Track video processing for message
	originalExt := c.Request.FormValue("originalExt")
	if strings.Contains(header.Filename, "_processed") && strings.HasSuffix(header.Filename, ".mp4") {
		message = "Video was processed: bitrate reduced while maintaining original resolution, cut to 59 seconds, and converted to MP4 format"
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
		AspectRatio:   fileInfo.OriginalRatio,
		Duration:      fileInfo.Duration,
		Message:       message,
	}

	c.JSON(http.StatusOK, response)
}

func (h *UploadHandler) uploadToS3(file *os.File, fileName string, config models.UploadRequest) (string, error) {
	// Create a production-ready HTTP client with robust TLS configuration
	var rootCAs *x509.CertPool
	
	// Try to load system root CAs, with fallback for Docker environments
	if systemRoots, err := x509.SystemCertPool(); err != nil {
		logrus.Warnf("Failed to load system cert pool, using default: %v", err)
		rootCAs = nil // Use Go's built-in root CAs as fallback
	} else {
		rootCAs = systemRoots
	}

	// Additional certificate handling for Docker/production environments
	if certFile := os.Getenv("SSL_CERT_FILE"); certFile != "" {
		if certData, err := os.ReadFile(certFile); err == nil {
			if rootCAs == nil {
				rootCAs = x509.NewCertPool()
			}
			rootCAs.AppendCertsFromPEM(certData)
			logrus.Infof("Loaded additional certificates from %s", certFile)
		} else {
			logrus.Warnf("Failed to load certificate file %s: %v", certFile, err)
		}
	}

	// Check for common certificate bundle locations in Docker containers
	certPaths := []string{
		"/etc/ssl/certs/ca-certificates.crt", // Debian/Ubuntu
		"/etc/pki/tls/certs/ca-bundle.crt",   // RHEL/CentOS
		"/etc/ssl/ca-bundle.pem",             // OpenSUSE
	}
	
	for _, certPath := range certPaths {
		if _, err := os.Stat(certPath); err == nil {
			if certData, err := os.ReadFile(certPath); err == nil {
				if rootCAs == nil {
					rootCAs = x509.NewCertPool()
				}
				rootCAs.AppendCertsFromPEM(certData)
				logrus.Infof("Loaded certificates from %s", certPath)
				break
			}
		}
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: false,
				RootCAs:            rootCAs,
				ServerName:         "", // Let Go handle server name verification
				MinVersion:         tls.VersionTLS12,
				MaxVersion:         tls.VersionTLS13,
				// Additional settings for production environments
				CipherSuites: []uint16{
					tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
					tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
					tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
				},
			},
			// Optimized transport settings for production
			DisableKeepAlives:     false,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
		},
	}

	// Create AWS session with custom HTTP client
	sess, err := session.NewSession(&aws.Config{
		Region: aws.String(config.AWSRegion),
		Credentials: credentials.NewStaticCredentials(
			config.AWSAccessKeyID,
			config.AWSSecretAccessKey,
			"",
		),
		HTTPClient: httpClient,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create AWS session: %v", err)
	}

	// Create an uploader with optimized settings for better performance
	uploader := s3manager.NewUploader(sess, func(u *s3manager.Uploader) {
		// Increase part size to 10MB for better performance with larger files
		u.PartSize = 10 * 1024 * 1024 // 10MB
		// Increase concurrency for faster uploads
		u.Concurrency = 5
	})

	logrus.Infof("Starting S3 upload for file: %s", fileName)

	// Upload the file to S3 with optimized settings
	result, err := uploader.Upload(&s3manager.UploadInput{
		Bucket: aws.String(config.S3BucketName),
		Key:    aws.String(fileName),
		Body:   file,
		ACL:    aws.String("public-read"), // Set ACL to public-read if needed
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload file: %v", err)
	}

	logrus.Infof("Successfully uploaded file to S3: %s", result.Location)
	return result.Location, nil
}

// HandleSimpleUpload processes images normally but only extracts aspect ratio for videos
func (h *UploadHandler) HandleSimpleUpload(c *gin.Context) {
	// Log Content-Type header to debug issues with multipart form parsing
	contentType := c.GetHeader("Content-Type")
	logrus.Infof("Received request with Content-Type: %s", contentType)

	// Handle special case when content type might have issues with boundary
	if !strings.Contains(contentType, "boundary=") {
		logrus.Warnf("Content-Type doesn't contain boundary parameter, trying to handle anyway")
	}

	// Try to parse the multipart form
	if err := c.Request.ParseMultipartForm(10 << 20); err != nil {
		logrus.Errorf("Failed to parse multipart form: %v", err)
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
	var message string

	if strings.HasPrefix(fileType, "image/") {
		// Process images the same way as the original endpoint
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

		num, den := utils.FloatToRatio(ratio, 100)
		ratioStr := fmt.Sprintf("%d:%d", num, den)

		fileInfo = &models.FileInfo{
			FileType:      "image",
			Width:         dimensions.Width,
			Height:        dimensions.Height,
			OriginalRatio: ratioStr,
			MatchedFormat: standardFormat,
		}
		message = "Image uploaded successfully with metadata extracted"

	} else if strings.HasPrefix(fileType, "video/") || utils.IsVideoFile(header.Filename) {
		// For videos, extract aspect ratio and trim to first 30 seconds
		tempPath := filepath.Join(os.TempDir(), header.Filename)
		if err := os.WriteFile(tempPath, fileBytes, 0644); err != nil {
			c.JSON(http.StatusInternalServerError, models.UploadResponse{
				Message: "Failed to create temp video file: " + err.Error(),
			})
			return
		}
		defer os.Remove(tempPath)

		// Get metadata from the original video
		dimensions, err := utils.GetVideoMetadata(tempPath)
		if err != nil {
			// If we can't get metadata, continue with basic info
			fileInfo = &models.FileInfo{
				FileType: "video",
			}
			logrus.Warnf("Failed to extract video metadata: %v", err)
		} else {
			// Calculate original aspect ratio
			ratio := float64(dimensions.Width) / float64(dimensions.Height)
			standardFormat := resizer.DetectFormat(dimensions.Width, dimensions.Height)

			num, den := utils.FloatToRatio(ratio, 100)
			ratioStr := fmt.Sprintf("%d:%d", num, den)

			fileInfo = &models.FileInfo{
				FileType:      "video",
				Width:         dimensions.Width,
				Height:        dimensions.Height,
				OriginalRatio: ratioStr,
				MatchedFormat: standardFormat,
				Duration:      dimensions.Duration,
			}
		}

		// Trim video to first 30 seconds using ffmpeg
		trimmedPath := filepath.Join(os.TempDir(), "trimmed_"+header.Filename)
		defer os.Remove(trimmedPath)

		if err := utils.TrimVideoTo30Seconds(tempPath, trimmedPath); err != nil {
			logrus.Errorf("Failed to trim video: %v", err)
			c.JSON(http.StatusInternalServerError, models.UploadResponse{
				Message: "Failed to trim video: " + err.Error(),
			})
			return
		}

		// For videos, upload the trimmed file directly to S3 (streaming)
		trimmedFile, err := os.Open(trimmedPath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.UploadResponse{
				Message: "Failed to open trimmed video: " + err.Error(),
			})
			return
		}
		defer trimmedFile.Close()

		// Get file size for response
		trimmedFileInfo, err := trimmedFile.Stat()
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.UploadResponse{
				Message: "Failed to get trimmed video info: " + err.Error(),
			})
			return
		}

		fileURL, err := h.uploadToS3(trimmedFile, header.Filename, awsConfig)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.UploadResponse{
				Message: "Failed to upload trimmed video to S3: " + err.Error(),
			})
			return
		}

		response := models.UploadResponse{
			FileName:      header.Filename,
			FileURL:       fileURL,
			FileType:      fileInfo.FileType,
			FileSize:      trimmedFileInfo.Size(),
			Width:         fileInfo.Width,
			Height:        fileInfo.Height,
			OriginalRatio: fileInfo.OriginalRatio,
			MatchedFormat: fileInfo.MatchedFormat,
			AspectRatio:   fileInfo.OriginalRatio,
			Duration:      fileInfo.Duration,
			Message:       "Video trimmed to 30 seconds and uploaded successfully with aspect ratio extracted",
		}

		c.JSON(http.StatusOK, response)
		return

	} else {
		fileInfo = &models.FileInfo{
			FileType: fileType,
		}
		message = "File uploaded successfully"
	}

	// Upload original file to S3 (for images and other files)
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
		AspectRatio:   fileInfo.OriginalRatio,
		Duration:      fileInfo.Duration,
		Message:       message,
	}

	c.JSON(http.StatusOK, response)
}

// Note: getImageDimensions moved to utils package to avoid duplication
