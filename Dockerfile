# We use an alpine image here, for distroless refer to: https://github.com/GoogleContainerTools/distroless for more details
FROM alpine
WORKDIR /
USER 65532:65532
COPY suss /
ENTRYPOINT ["/suss"]
