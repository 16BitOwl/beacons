FROM golang:1.26 AS builder

ARG VERSION=dev
ARG BUILDTIME=unknown

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-X main.version=${VERSION} -X main.buildTime=${BUILDTIME}" -o beacons ./cmd/beacons

FROM gcr.io/distroless/static
COPY --from=builder /app/beacons /beacons
ENTRYPOINT ["/beacons"]
