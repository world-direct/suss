# Build the manager binary
FROM golang:1.21 as builder

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY *.go .
COPY looper/ looper/
COPY kmutex/ kmutex/
COPY suss/ suss/

# Build
RUN CGO_ENABLED=0 go build -a -o ./out/suss .

FROM builder AS chartbuilder
WORKDIR /workspace

# We use an alpine image here, for distroless refer to: https://github.com/GoogleContainerTools/distroless for more details
FROM alpine
WORKDIR /
COPY --from=builder /workspace/out/suss .
USER 65532:65532

ENTRYPOINT ["/suss"]
