package main

import (
	"github.com/asset_upload_service/handlers"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func main() {
	// Initialize Gin router
	router := gin.Default()

	// Set up routes
	uploadHandler := handlers.NewUploadHandler()
	router.POST("/upload", uploadHandler.HandleUpload)

	// Start server
	port := ":8080"
	logrus.Infof("Server starting on port %s", port)
	if err := router.Run(port); err != nil {
		logrus.Fatalf("Failed to start server: %v", err)
	}
}
