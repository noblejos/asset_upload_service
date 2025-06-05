# Stage 1: Build
FROM golang:1.23 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . ./
RUN CGO_ENABLED=0 GOOS=linux go build -v -o /asset_upload_service .

# Stage 2: Run the application
FROM alpine:latest

# Install ffmpeg and libc6-compat for Go binaries
RUN apk update && \
    apk add --no-cache ffmpeg libc6-compat

WORKDIR /app

# Copy the built executable from the builder stage
COPY --from=builder /asset_upload_service .

EXPOSE 8080

CMD ["/app/asset_upload_service"]
