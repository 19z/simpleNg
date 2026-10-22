FROM --platform=$BUILDPLATFORM golang:1.25-bookworm AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/simpleNg .

FROM debian:bookworm-slim
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates openssh-server tini \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /run/sshd
COPY --from=build /out/simpleNg /usr/local/bin/simpleNg
COPY docker/shell-entrypoint.sh /usr/local/bin/shell-entrypoint
RUN chmod 0755 /usr/local/bin/shell-entrypoint
RUN rm -f /etc/ssh/ssh_host_*
ENV PATH="/usr/local/bin:${PATH}"
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/shell-entrypoint"]
