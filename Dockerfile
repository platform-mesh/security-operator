# Build the manager binary
FROM docker.io/golang:1.24 AS builder
ARG TARGETOS
ARG TARGETARCH

ARG GITHUB_HOST
ARG GITHUB_USER
ARG GITHUB_TOKEN

ENV GITHUB_HOST=${GITHUB_HOST}
ENV GITHUB_USER=${GITHUB_USER}
ENV GITHUB_TOKEN=${GITHUB_TOKEN}

# Setting up .netrc file for pulling go private packadjes
RUN printf  "machine %s\nlogin %s\npassword %s\n" \
    "GITHUB_HOST" "GITHUB_USER" "GITHUB_TOKEN" > ~/.netrc && \
    chmod 600 ~/.netrc

WORKDIR /workspace

# Copy your local .netrc file
COPY .netrc /root/.netrc
RUN chmod 600 /root/.netrc

ENV GOPRIVATE=github.com/
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download
RUN rm -rf ~/.netrc
# Copy the go source
COPY cmd/ cmd/
COPY api/ api/
COPY internal/ internal/
COPY main.go main.go

# Build
# the GOARCH has not a default value to allow the binary be built according to the host where the command
# was called. For example, if we call make docker-build in a local env which has the Apple Silicon M1 SO
# the docker BUILDPLATFORM arg will be linux/arm64 when for Apple x86 it will be linux/amd64. Therefore,
# by leaving it empty we can ensure that the container and binary shipped on it will have the same platform.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o manager main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
