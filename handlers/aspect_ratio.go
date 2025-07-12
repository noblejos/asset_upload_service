package handlers

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/asset_upload_service/utils"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// GetVideoAspectRatioHandler retrieves the aspect ratio from a video URL (typically on S3)
func (h *UploadHandler) GetVideoAspectRatioHandler(c *gin.Context) {
	// Get the video URL from the query parameter
	videoURL := c.Query("url")
	if videoURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Missing required 'url' parameter",
		})
		return
	}

	// Validate the URL
	_, err := url.ParseRequestURI(videoURL)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid URL format",
		})
		return
	}

	// Get the aspect ratio from the URL
	aspectRatio, err := utils.GetVideoAspectRatioFromURL(videoURL)
	if err != nil {
		logrus.Errorf("Failed to get aspect ratio: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to get aspect ratio: %v", err),
		})
		return
	}

	// Return the aspect ratio
	c.JSON(http.StatusOK, aspectRatio)
}
