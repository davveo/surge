# Surge

对标微信网页版的 Web 端即时通讯：单聊 + 群聊。目标是 **100 万同时在线** 长连接。

当前仓库 **P1**：单聊 + 群聊（含成员变更系统消息）、2 分钟撤回、单聊已读、正在输入、草稿/通知/免打扰、MinIO 预签名图片文件、扫码登录。

打开 http://127.0.0.1:8080 ，两个窗口分别登录 `u1` / `u2`（或一端扫码确认），通讯录里互相添加后再发消息；也可建群、发图。

## 文档

| 文档 | 内容 |
|---|---|
| [docs/README.md](docs/README.md) | 方案总览 |
| [docs/architecture.md](docs/architecture.md) | 架构分层 |
| [docs/messaging.md](docs/messaging.md) | 消息协议 |
| [docs/capacity.md](docs/capacity.md) | 容量与验收 |
| [docs/roadmap.md](docs/roadmap.md) | 功能分期 |
| [docs/im-architecture.html](docs/im-architecture.html) | 架构图 |

## 一键启动

需要本机已安装 Docker。唯一命令（二选一，等价）：

```bash
make up
# 或
./scripts/up.sh
```

内部即 `docker compose up -d --build`，会构建并启动：

| 服务 | 端口 | 说明 |
|---|---|---|
| mysql | 3306 | MySQL 8 |
| redis | 6379 | Redis 7 |
| minio | 9001 | S3 API（控制台 9002） |
| im-core | 9000 | gRPC |
| gateway | 8080 | HTTP / WebSocket；`web/` 静态页 |

停栈：`make down` 或 `./scripts/down.sh`。

容器内已对齐代码环境变量：`MYSQL_DSN`、`REDIS_ADDR`、`IMCORE_ADDR`、`GATEWAY_ADDR`、`GATEWAY_ID`、`JWT_SECRET`、`WEB_DIR`。连接串写的是 compose 服务名（`mysql` / `redis` / `im-core`），不会读取你本机 `go run` 用的 `127.0.0.1`。

若重建 MySQL 时偶发 `No such container`，镜像一般已经构建成功，再执行一次 `docker compose up -d` 即可。若 9000 被占用，可删掉 `im-core` 的 `ports`（gateway 仍走容器内网）。

访问：

- HTTP API / 静态页：http://127.0.0.1:8080
- 健康检查：http://127.0.0.1:8080/healthz
- WebSocket：`ws://127.0.0.1:8080/v1/ws`
- im-core gRPC：`127.0.0.1:9000`

## P0 本地跑通（go run）

需要 Go 1.20+、Docker（仅 MySQL 8 + Redis 7）、`protoc`（改协议时）。国内网络建议：

```bash
export GOPROXY=https://goproxy.cn,direct
```

只起依赖，再本地跑 Go（不要先 `make up` 全栈，否则 8080/9000 会占用）：

```bash
docker compose up -d mysql redis
make proto
make test
# 终端 1
go run ./im-core
# 终端 2
go run ./gateway
```

开发登录后须先加好友，否则发送返回 403：

```bash
TOKEN=$(curl -s -X POST localhost:8080/v1/auth/dev-login -d '{"uid":"u1"}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["access_token"])')
curl -s -X POST localhost:8080/v1/friends -H "Authorization: Bearer $TOKEN" -d '{"peer_uid":"u2"}'
```

把返回的 `access_token` 用于 WebSocket。调试可用文本帧（protojson）：

```json
{"auth":{"accessToken":"<JWT>","deviceId":"web"}}
{"send":{"clientMsgId":"m1","peerUid":"u2","payload":{"type":"TEXT","text":"hi"}}}
{"send":{"clientMsgId":"m2","cid":"grp:...","payload":{"type":"TEXT","text":"hello group"}}}
{"recall":{"cid":"p2p:u1:u2","msgId":"<uuid>"}}
{"typing":{"cid":"p2p:u1:u2"}}
{"read":{"cid":"p2p:u1:u2","convSeq":"1"}}
{"sync":{"lastSyncSeq":"0","limit":100}}
{"heartbeat":{}}
```

连 `ws://127.0.0.1:8080/v1/ws`。对端 `u2` 同样登录后会收到 `push`；刷新后带 `last_sync_seq` 做 `sync`。

会话列表：`GET /v1/conversations`（`Authorization: Bearer <token>`）。

建群（成员须已是好友）：

```bash
curl -s -X POST localhost:8080/v1/groups -H "Authorization: Bearer $TOKEN" \
  -d '{"name":"周末局","members":["u2","u3"]}'
```

拉人 / 踢人：`POST /v1/group-invite`、`POST /v1/group-kick`；群详情：`GET /v1/group?cid=`。

媒体：`POST /v1/media/presign` 拿 PUT URL，浏览器直传 MinIO，再 `POST /v1/media/complete` 生成缩略图，WS 只发 object key。扫码：登录页展示 `/v1/auth/qr.png`，已登录窗口打开二维码里的链接后点确认。

两端连上后可跑通发送与推送：

```bash
go run ./tools/p0smoke
make g200   # 200 人群，默认发 10 条，检查 ACK 无丢失
```
