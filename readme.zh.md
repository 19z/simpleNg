# simpleNg

simpleNg 可通过有公网 IP 的服务器，将本地 HTTP 和 WebSocket 服务映射到公网。也支持静态目录、文件收集和浏览器 SSH 终端。

## 服务端

在可公网访问的 Linux 服务器启动：

```sh
./simpleNg --type=server --port=8066
```

将泛域名（例如 `*.example.com`）解析到服务器，并开放 TCP 8066 端口；也可以在前面配置 Nginx 等 TLS 反向代理。

## 客户端

映射本地 HTTP 服务：

```sh
./simpleNg --domain app.example.com:8066 --local 127.0.0.1:8080
```

然后访问 `http://app.example.com:8066`；若使用 TLS 代理，则访问对应的 HTTPS 地址。每个客户端使用不同域名。网络中断后客户端会自动重连，按 Ctrl+C 停止。

`--local` 也支持本地目录模式：

```sh
./simpleNg --domain files.example.com:8066 --local file:///srv/site
./simpleNg --domain uploads.example.com:8066 --local collect:///srv/uploads
./simpleNg --domain shell.example.com:8066 --local ssh://
```

`file://` 用于发布目录；`collect://` 提供浏览器上传页面；`ssh://` 提供浏览器终端，并默认进入认证成功的 SSH 账号 home 目录。SSH 模式要求 Linux 上的 sshd 监听 `127.0.0.1:22`。可指定目录，例如 `ssh:///srv/work`；`ssh://./` 使用进程当前目录。打开终端后，输入本地操作系统的 SSH 用户名和密码。

所有本地 URL 均可用 `./` 表示进程当前目录，例如 `file://./` 发布当前目录，`collect://./` 将上传文件保存到当前目录。目标目录必须已存在。Windows 路径示例：`file:///C:/site`、`collect:///C:/uploads`。

运行 `simpleNg --help` 查看参数和示例。
