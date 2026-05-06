FROM golang:1.25-alpine AS builder

WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /bin/p2p-app ./cmd/app

FROM alpine:3.20
WORKDIR /app
RUN adduser -D appuser
COPY --from=builder /bin/p2p-app /app/p2p-app
RUN chown -R appuser:appuser /app
USER appuser

ENTRYPOINT ["/app/p2p-app"]
