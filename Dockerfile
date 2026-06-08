FROM golang:1.26 AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o beacons ./cmd/beacons

FROM gcr.io/distroless/static
COPY --from=builder /app/beacons /beacons
ENTRYPOINT ["/beacons"]
