# proto

P0 冻结的 WebSocket 帧与 im-core gRPC。不要在网关里加业务字段；先改这里再改两端。

## 生成

```bash
make proto
```

产出在 `proto/gen/im/v1/`。

## WebSocket Envelope

| 帧 | 方向 | 作用 |
|---|---|---|
| `auth` / `auth_ok` | C→S / S→C | JWT 绑定连接与 `device_id` |
| `heartbeat` | 双向 | 30–45s；网关读超时 90s 踢连接 |
| `send` | C→S | `client_msg_id` + `cid` 或 `peer_uid` + payload |
| `ack` | S→C | `client_msg_id` → `msg_id` + `conv_seq` + `sync_seq` |
| `push` | S→C | 对端 inbox 指针 + P0 文本正文 |
| `sync` / `sync_resp` | C→S / S→C | `last_sync_seq` 拉增量 |
| `error` | S→C | `code` + `message` |

编码：二进制 protobuf（默认）。调试可用 JSON 文本帧（protojson）。

## gRPC IMCore

`Send` `Sync` `Watermark` `ListConversations` `GetTimeline`

Gateway 持 TCP；本服务只落库。在线路由写 Redis，`Send` 成功后按 `gw:{gateway_id}` 发布 `GatewayPush`。
