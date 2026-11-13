FROM golang:1.27.3-alpine3.24@sha256:fa3d596898d9f126af7c01d14ee7597bab3f39cccf392ef36b69fe77cd681f18 AS builder

ENV GOPROXY=https://proxy.golang.org,direct \
    GOSUMDB=sum.golang.org \
    CGO_ENABLED=0

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN GOOS=linux go build -trimpath -buildvcs=false -ldflags="-s -w" -o denkit-stash .

FROM alpine:3.24.4@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf

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
