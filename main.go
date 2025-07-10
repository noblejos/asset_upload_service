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
	} // Initialize Gin router
	router := gin.Default()

	// Configure router with larger body size limit for multipart forms
	router.MaxMultipartMemory = 10 << 20 // 10 MiB

	// Configure CORS
	router.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	})
	// Set up routes
	uploadHandler := handlers.NewUploadHandler()
	// rawUploadHandler := handlers.NewRawUploadHandler()

	// Standard multipart form upload endpoint
	router.POST("/upload", uploadHandler.HandleUpload)

	// Raw binary upload endpoint - doesn't require multipart form data
	// router.POST("/upload/raw", rawUploadHandler.HandleRawUpload)

	// Start server
	port := ":8080"
	logrus.Infof("Server starting on port %s", port)
	if err := router.Run(port); err != nil {
		logrus.Fatalf("Failed to start server: %v", err)
	}
}
