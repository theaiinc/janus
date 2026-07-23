FROM golang:1.25 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/janus ./cmd/janus

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /var/lib/janus
COPY --from=build /out/janus /usr/local/bin/janus
COPY janus.docker.yaml /etc/janus/janus.yaml

EXPOSE 8088
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/janus"]
CMD ["run", "--config", "/etc/janus/janus.yaml"]
