# Build stage
FROM golang:1.22-alpine AS builder
WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/     cmd/
COPY api/     api/
COPY internal/ internal/

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /manager ./cmd/main.go

# Runtime stage — distroless for minimal attack surface
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /manager .
USER 65532:65532
ENTRYPOINT ["/manager"]
