FROM --platform=$BUILDPLATFORM golang:1.27.3@sha256:957861185dbf29d93668ace41e011d091c6e779953d47f0316ef2cc58e28fe2b AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go *.sql ./
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /exporter .

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /exporter /exporter
USER 65532:65532
EXPOSE 9808
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 CMD ["/exporter", "-healthcheck"]
ENTRYPOINT ["/exporter"]
