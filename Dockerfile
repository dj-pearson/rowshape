# FROM scratch image for CI (PRD §7). goreleaser injects the prebuilt static
# binary; no build happens here. Single static binary, no runtime.
FROM scratch
COPY rowshape /rowshape
ENTRYPOINT ["/rowshape"]
