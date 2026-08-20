# gateway

有状态 WebSocket 接入。不落消息库。

- `POST /v1/auth/dev-login` 签发 JWT（开发登录）
- `GET /v1/ws` 长连接（send / recall / typing / read / sync）
- `GET /v1/conversations` / `GET /v1/timeline?cid=` HTTPS 读路径
- `POST /v1/groups`、`POST /v1/group-invite`、`POST /v1/group-kick`、`GET /v1/group?cid=`
- `POST /v1/auth/qr/new` / `GET /v1/auth/qr.png` / `GET /v1/auth/qr/status` / `POST /v1/auth/qr/approve`
- `POST /v1/media/presign` / `POST /v1/media/complete`（MinIO 预签名直传）
- 心跳刷新 Redis `route:{uid}`（TTL 90s）
- 订阅 `gw:{GATEWAY_ID}` 向本地连接 Push / recalled / typing / read

```bash
export IMCORE_ADDR=127.0.0.1:9000
export REDIS_ADDR=127.0.0.1:6379
export GATEWAY_ID=gw-1
export WEB_DIR=web
export MINIO_ENDPOINT=127.0.0.1:9001
export MINIO_PUBLIC_URL=http://127.0.0.1:9001
go run ./gateway
```
