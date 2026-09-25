FROM golang:1.27.1-alpine3.23@sha256:d9e2f2f07b10cc922da3e80e035c3058810b328d5aef82d2c63680967c5e2ec9 AS builder

ENV GOPROXY=https://proxy.golang.org,direct \
    GOSUMDB=sum.golang.org \
    CGO_ENABLED=0

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN GOOS=linux go build -trimpath -buildvcs=false -ldflags="-s -w" -o denkit-stash .

FROM alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6

RUN apk --no-cache add ca-certificates

RUN addgroup -g 1001 -S denkit && \
    adduser -u 1001 -S denkit -G denkit

WORKDIR /app

COPY --from=builder /app/denkit-stash .

RUN chown -R denkit:denkit /app

# Archive staging area; compose mounts a volume here and points TMPDIR at it.
# Creating it in the image gives a fresh named volume the right ownership.
RUN mkdir -p /scratch && chown denkit:denkit /scratch

USER denkit

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/ || exit 1

CMD ["./denkit-stash"]
