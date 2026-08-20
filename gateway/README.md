# gateway

有状态 WebSocket 接入。不落消息库。

- `POST /v1/auth/dev-login` 签发 JWT（P0 开发登录）
- `GET /v1/ws` 长连接
- `GET /v1/conversations` / `GET /v1/timeline?cid=` HTTPS 读路径
- 心跳刷新 Redis `route:{uid}`（TTL 90s）
- 订阅 `gw:{GATEWAY_ID}` 向本地连接 Push

```bash
export IMCORE_ADDR=127.0.0.1:9000
export REDIS_ADDR=127.0.0.1:6379
export GATEWAY_ID=gw-1
export WEB_DIR=web   # 可选；目录存在则提供静态资源，默认即为 web
go run ./gateway
```
