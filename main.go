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

	router := gin.Default()

	// Configure router with larger body size limit for multipart forms
	// router.MaxMultipartMemory = 10 << 20 // 10 MiB
	// Configure CORS
	router.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, Authorization, Accept")
		c.Writer.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Type")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")

		// Log request headers for debugging
		logrus.Infof("Request method: %s, path: %s", c.Request.Method, c.Request.URL.Path)
		for name, values := range c.Request.Header {
			logrus.Infof("Header %s: %s", name, values)
		}

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}) // Set up routes

	uploadHandler := handlers.NewUploadHandler()

	// Standard multipart form upload endpoint
	router.POST("/upload", uploadHandler.HandleUpload)

	// Simple upload endpoint - processes images normally, extracts aspect ratio for videos
	router.POST("/upload/simple", uploadHandler.HandleSimpleUpload)

	// Endpoint to retrieve video aspect ratio from AWS S3
	router.GET("/video/aspect-ratio", uploadHandler.GetVideoAspectRatioHandler)

	// Start server
	port := ":8080"
	logrus.Infof("Server starting on port %s", port)
	if err := router.Run(port); err != nil {
		logrus.Fatalf("Failed to start server: %v", err)
	}
}
