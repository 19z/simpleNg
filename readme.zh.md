# simpleNg

## 介绍

一个简单的用来将本地 http 服务暴露到公网的工具，可替代 ngrok。在同一个可执行文件中包含服务端模式和客户端模式。

你需要一台公网环境的服务器，以及一个指向你服务器的泛域名（可以是多级域名，比如 *.simple-ng.example.com ，如果使用中国大陆的服务器，域名需要备案）。

你可以在服务端前置 Nginx 来与你的服务器上的其他服务共享 http / https 端口。同时你也可以借助 Let's Encrypt 等免费的域名证书来提供 https 服务。

支持转发本地 HTTP 和 WebSocket 服务，包括 WebSocket 二进制消息、子协议、压缩及控制帧。隧道客户端固定连接 `/__simple_ng_internal__/tunnel/v1`，其他路径均转发到本地服务，业务 WebSocket 不会替换隧道连接。域名没有在线客户端时立即返回 HTTP 404。

此版本更新了内部隧道协议，服务端和客户端需要同时升级。命令行参数无需调整；如 Nginx 按路径配置代理，请确保上述内部路径也能转发 WebSocket Upgrade。

```mermaid
graph TD
    subgraph 内网环境
        A[内网机器] -- 客户端模式 --> B[ngrok 客户端]
    end
    subgraph 公网环境
        C[服务端]
    end
    subgraph 其他网络环境
        D[其他机器] -- 通过域名访问 --> C
    end
    B -- 连接 --> C
    C -- 转发请求 --> B

```
## 使用

提前准备：
1. 一台可以公网访问的服务器
2. 一个可以公网访问的泛域名，比如 `*.simple-ng.example.com` ，解析到你的服务器 

**服务器端**：

1. 下载编译好的二进制文件。
2. 运行 `./simpleNg -type=server -port=8066` 启动服务端
   1. `type` 运行模式，`server` 服务端模式
   2. `port` 服务端监听端口，默认为 8066
   3. 你可以使用 `nohup ./simpleNg -type=server -port=8066 > ./server.log 2>&1 &` 来将服务端运行在后台，同时将日志输出到 `./server.log`。或者使用网络上的一些教程来将其配置为一个自启动的服务。
3. 使用 `Nginx` 代理你的 `simpleNg` 服务（可跳过），下面是一个可以参考的配置文件：

```nginx

server {
    listen 80;
    listen 443 ssl http2;
    server_name *.simple-ng.example.com;
    
    if ($server_port !~ 443){
        rewrite ^(/.*)$ https://$host$1 permanent;
    }
    
    ssl_certificate /etc/letsencrypt/live/simple-ng.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/simple-ng.example.com/privkey.pem;
    ssl_protocols TLSv1 TLSv1.1 TLSv1.2;
    ssl_ciphers "EECDH+AESGCM:EDH+AESGCM:AES256+EECDH:AES256+EDH";
    ssl_prefer_server_ciphers on;
    ssl_session_cache shared:SSL:10m;
    ssl_session_timeout 10m;
    add_header Strict-Transport-Security "max-age=31536000";
    error_page 497  https://$host$request_uri;
    
    location / {
        proxy_pass http://127.0.0.1:8066;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_redirect off;
        proxy_buffering off;
    }
}
```

**客户端**：

下面的例子假设你要将本地 `127.0.0.1:8080` 的 http 服务暴露到公网。

1. 下载编译好的二进制文件。
2. 运行 `./simpleNg -type=client -local=127.0.0.1:8080 -domain=testprefix.simple-ng.example.com` 启动客户端。
   1. `type` 运行模式，`client` 客户端模式，可省略，默认为 `client`
   2. `local` 客户端希望转发的本地 http 服务地址，如 `localhost:8080`。默认为 `127.0.0.1:8080`
   3. `domain` 客户端希望使用的公网域名。必须是您服务器端可接收的域名（您配置的泛域名下的某一个具体域名）。在同一个服务端上不可重复。可以使用下面的几种形式：
      1. `ws://testprefix.simple-ng.example.com`
      2. `wss://testprefix.simple-ng.example.com`
      3. `testprefix.simple-ng.example.com` （不带协议前缀，会自动分别尝试 ws 和 wss 协议）
      4. `testprefix.simple-ng.example.com:8066` （如果你的服务没有使用 nginx 代理到 80/443 端口，需要指定端口）

需要注意的是，在同一个服务器上，不同客户端的 `domain` 必须不同，否则旧客户端会被踢下线。

同域名的新客户端会接管转发，旧客户端收到关闭码 `4001`（域名被挤占）后停止运行，不再重连。网络中断或服务端暂时不可用时，客户端会按 1、2、4、8、16、30 秒的间隔自动重试，之后每 30 秒重试；连接稳定一分钟后重置退避。可用 Ctrl+C 停止客户端及重连等待。

接下来在任意位置，访问 `http(s)://testprefix.simple-ng.example.com` 即可访问到你内网机器中 `127.0.0.1:8080` 的 http 服务。

也可以直接将本地目录作为静态站点，无需额外运行 HTTP 服务：

```sh
simpleNg --domain some.example.com --local file:///C:/Document/example/
# Linux / macOS
simpleNg --domain some.example.com --local file:///home/user/example/
```

目录必须存在，使用绝对 `file:///` URL。路径含空格时可为整个参数加引号，`#`、`%` 等 URL 特殊字符需要百分号编码。访问目录时优先使用 `index.html`，不存在时使用 `index.htm`；均不存在时显示目录列表。支持静态资源、HEAD、Range 与条件请求，目录外的文件和符号链接目标不能访问。

因保护数据安全，本项目不提供公网演示服务器。

### WebSSH 测试环境

使用 Docker Compose 启动本地 Linux SSH 测试容器：

```sh
docker compose -f docker/ssh-test/compose.yaml up --build -d
```

容器 SSH 端口映射到 `127.0.0.1:2222`，测试账号为 `ssh-test`，密码为 `changeme`。该账号仅用于本机测试，可使用 `ssh -p 2222 ssh-test@127.0.0.1` 登录。WebSSH 支持 Linux 主机或容器，只要环境内 `127.0.0.1:22` 有 sshd 监听；Windows 暂不支持。容器也用于集成测试 SSH 登录、PTY 和命令执行。

`file://./`、`collect://./` 和 `ssh://./` 使用 simpleNg 进程的当前工作目录，也支持追加相对子目录，如 `collect://./uploads`；绝对路径使用 `file:///`、`collect:///` 和 `ssh:///`。目录必须存在。收集器上传页面显示每个文件的上传进度、完成状态和错误信息。
