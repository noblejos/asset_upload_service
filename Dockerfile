# Stage 1: Build the application
FROM golang:1.20 AS builder

WORKDIR /app

# Copy go.mod and go.sum and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the application
# CGO_ENABLED=0 is important for creating a static binary
# GOOS=linux ensures the binary is built for Linux, which is common for Docker containers
RUN CGO_ENABLED=0 GOOS=linux go build -o /asset_upload_service .

# Stage 2: Run the application
FROM alpine:latest

WORKDIR /app

# Copy the built executable from the builder stage
COPY --from=builder /asset_upload_service .

# Expose the port your application listens on (assuming 8080)
EXPOSE 8080

# Command to run the executable
CMD ["/app/asset_upload_service"]