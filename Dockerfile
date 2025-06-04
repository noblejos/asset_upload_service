# Stage 1: Build
FROM golang:1.23 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . ./
RUN CGO_ENABLED=0 GOOS=linux go build -v -o /asset_upload_service .

# Stage 2: Run the application
FROM alpine:latest

WORKDIR /app

# Copy the built executable from the builder stage
COPY --from=builder /asset_upload_service .

# Expose the port your application listens on (assuming 8080)
EXPOSE 8080

# Command to run the executable
CMD ["/app/asset_upload_service"]