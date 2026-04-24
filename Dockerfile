# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

WORKDIR /src

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH

RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/job-runner ./cmd

FROM alpine:3.21

WORKDIR /app

RUN apk add --no-cache docker-cli ca-certificates && \
    mkdir -p /app/data /app/logs /app/artifacts

COPY --from=builder /out/job-runner /app/job-runner
COPY config.example.yaml /app/config.yml

EXPOSE 8888

ENTRYPOINT ["/app/job-runner"]
