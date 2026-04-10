# Build stage
FROM golang:1.24-alpine AS builder
RUN apk add --no-cache git ca-certificates
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/nkorebank ./cmd/nkorebank

# Runtime stage
FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
RUN addgroup -S nkorebank && adduser -S nkorebank -G nkorebank
COPY --from=builder /bin/nkorebank /bin/nkorebank
USER nkorebank
EXPOSE 8080
ENTRYPOINT ["/bin/nkorebank"]
