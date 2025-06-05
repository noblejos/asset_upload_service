package main

import (
	"log"
	"os"

	"github.com/asset_upload_service/handlers"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
)

func main() {
	// Only load .env file in development
	if os.Getenv("ENV") != "production" {
		err := godotenv.Load()
		if err != nil {
			log.Println("Warning: could not load .env file (only needed for local dev)")
		}
	}

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
