# Dockerfile to deploy Go Backend on Render

# Build stage
FROM golang:alpine AS builder

WORKDIR /app

# Install required build tools
RUN apk add --no-cache build-base

# Copy dependency configs and download
COPY go.mod go.sum ./
RUN go mod download

# Copy the actual application files
COPY . .

# Build the Go application highly optimized
RUN CGO_ENABLED=0 GOOS=linux go build -o main .

# Run stage
FROM alpine:latest

WORKDIR /app

# Install Root Certificates and Timezones
RUN apk --no-cache add ca-certificates tzdata

# Copy binary from the builder phase
COPY --from=builder /app/main .

# Optionally, expose the volume so the tg_session.json can persist if you mount a disk in Render.
VOLUME /app/temp_uploads

# We will let Render override the `PORT` automatically, but by default expose 8000
EXPOSE 8000

# Run the binary executable
CMD ["./main"]
