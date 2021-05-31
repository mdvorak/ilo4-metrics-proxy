# Build the manager binary
FROM docker.io/golang:1.16.4 as builder

# Prepare Go environment
ARG GOPROXY
ENV GOPROXY $GOPROXY

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
# NOTE this step does not help on ephemeral CI agent, but does not hurt either
RUN go mod download

# Copy the go source
COPY main.go main.go
COPY pkg/ pkg/

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -a -o exporter main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM registry.access.redhat.com/ubi8/ubi-minimal:8.4
WORKDIR /
COPY --from=builder /workspace/exporter .
USER nobody:nobody

ENTRYPOINT ["/exporter"]
