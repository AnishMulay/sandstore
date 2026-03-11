FROM golang:1.24-alpine AS builder

WORKDIR /src

ARG TAGS
ARG GOOS
ARG GOARCH

# Build a static binary so runtime image has no libc dependency surprises. This keeps the final image minimal and portable.
ENV CGO_ENABLED=0

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN GOOS="${GOOS}" GOARCH="${GOARCH}" go build -tags="${TAGS}" -o /build/sandstore ./cmd/sandstore
RUN GOOS="${GOOS}" GOARCH="${GOARCH}" go build -o /build/smoke ./clients/open_smoke
RUN GOOS="${GOOS}" GOARCH="${GOARCH}" go build -o /build/durability_smoke ./clients/durability_smoke
RUN GOOS="${GOOS}" GOARCH="${GOARCH}" go test -c -tags="${TAGS}" -o /build/cluster-tests ./integration/cluster

FROM alpine:latest AS runner

RUN mkdir -p /var/lib/sandstore/data /var/lib/sandstore/logs

COPY --from=builder /build/sandstore /usr/local/bin/sandstore
COPY --from=builder /build/smoke /usr/local/bin/smoke
COPY --from=builder /build/durability_smoke /usr/local/bin/durability_smoke

ENTRYPOINT ["/usr/local/bin/sandstore"]

FROM alpine:latest AS test-runner

RUN apk add --no-cache bash ca-certificates

COPY --from=builder /build/cluster-tests /usr/local/bin/cluster-tests
COPY --from=bitnami/kubectl:1.32.2 /opt/bitnami/kubectl/bin/kubectl /usr/local/bin/kubectl

ENTRYPOINT ["/usr/local/bin/cluster-tests"]
