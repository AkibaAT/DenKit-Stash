FROM golang:1.27.3-alpine3.24@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS builder

ENV GOPROXY=https://proxy.golang.org,direct \
    GOSUMDB=sum.golang.org \
    CGO_ENABLED=0

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN GOOS=linux go build -trimpath -buildvcs=false -ldflags="-s -w" -o denkit-stash .

FROM alpine:3.24.3@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a

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
