FROM golang:1.24-alpine AS builder

WORKDIR /src

ARG TAGS
ARG GOOS
ARG GOARCH

ENV CGO_ENABLED=0

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN GOOS="${GOOS}" GOARCH="${GOARCH}" go build -tags="${TAGS}" -o /build/sandstore ./cmd/sandstore
RUN GOOS="${GOOS}" GOARCH="${GOARCH}" go build -o /build/smoke ./clients/client

FROM alpine:latest AS runner

RUN mkdir -p /var/lib/sandstore/data /var/lib/sandstore/logs

COPY --from=builder /build/sandstore /usr/local/bin/sandstore
COPY --from=builder /build/smoke /usr/local/bin/smoke

ENTRYPOINT ["/usr/local/bin/sandstore"]
