FROM gcr.io/distroless/static:nonroot
COPY ./bin/flux-bridge /
ENTRYPOINT ["/flux-bridge"]
