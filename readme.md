# simpleNg

simpleNg exposes local HTTP and WebSocket services through a server with a public IP address. It also supports static directories, file collection, and a browser-based SSH terminal.

## Setup

Run the server on a publicly reachable Linux machine:

```sh
./simpleNg --type=server --port=8066
```

Point a wildcard domain such as `*.example.com` to that machine. Open TCP port `8066`, or put a TLS reverse proxy such as Nginx in front of it.

## Client

Connect a local HTTP service:

```sh
./simpleNg --domain app.example.com:8066 --local 127.0.0.1:8080
```

Then open `http://app.example.com:8066` (or `https://app.example.com` when using a TLS proxy). Each client must use a different domain. The client reconnects after temporary network failures; press Ctrl+C to stop it.

`--local` also accepts local directory modes:

```sh
./simpleNg --domain files.example.com:8066 --local file:///srv/site
./simpleNg --domain uploads.example.com:8066 --local collect:///srv/uploads
./simpleNg --domain shell.example.com:8066 --local ssh://
```

`file://` serves a directory. `collect://` provides a browser upload page. `ssh://` opens a browser terminal and starts in the authenticated SSH account's home directory; on Linux, sshd must listen on `127.0.0.1:22`. A path can be supplied, for example `ssh:///srv/work`; `ssh://./` uses the process working directory. In the terminal, enter the local operating-system SSH username and password.

For any local URL, `./` refers to the process working directory. For example, `file://./` serves it and `collect://./` stores uploads there. Directories must already exist. On Windows, use paths such as `file:///C:/site` and `collect:///C:/uploads`.

Run `simpleNg --help` for command options and examples.

## Online Linux shell image

```sh
docker run -d --name online-shell -e DOMAIN=hello.example.com -e ROOTPWD=123456 ghcr.io/19z/simpleng-shell:latest
```

Point `DOMAIN` at an existing simpleNg server, open the domain in a browser, and log in as `root` using `ROOTPWD`. No container port mapping or inbound ports are required; outbound access to the server is needed. Use `passwd` to change the password, `apt-get update && apt-get install vim` to install software, or `simpleNg --domain app.example.com --local 127.0.0.1:8080` to publish another service.

The password is initialized only on the first start. Restarting the same container preserves password changes and installed software; recreating it starts fresh. The Publish online shell container workflow publishes amd64/arm64 images on master updates or manual dispatch.
