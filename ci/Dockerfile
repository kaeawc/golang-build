# Use a minimal base image
FROM golang:1.23.1-alpine AS builder

# Set work directory inside the container
WORKDIR /app

# Copy go.mod and go.sum first to take advantage of Docker's caching mechanism
COPY go.mod ./
COPY go.sum ./
RUN go mod download

# Copy the rest of the project files
COPY . .

# Build the Go binary
RUN go build cmd/server/main.go

# Final stage: start from scratch to minify the final image
FROM scratch
COPY --from=builder /app/main /main

# Define entry point
ENTRYPOINT ["/main"]
