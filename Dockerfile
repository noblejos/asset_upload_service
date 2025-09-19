# Stage 1: Build
FROM golang:1.23 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . ./
RUN CGO_ENABLED=0 GOOS=linux go build -v -o /asset_upload_service .

# Stage 2: Run the application
FROM debian:stable-slim

RUN apt-get update && \
    apt-get install -y ffmpeg ca-certificates && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /asset_upload_service .

EXPOSE 8080

CMD ["/app/asset_upload_service"]
