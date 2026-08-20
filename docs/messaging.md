# 消息协议与同步

对标微信网页版的同步模型：会话时间线是正文权威；用户 inbox 是增量事件流；客户端用 `last_seq` 补齐。

投递语义是**至少一次**。客户端用 `client_msg_id` 去重。不要追求服务端恰好一次，代价高于收益。

## 发送热路径

```
Client  --WSS-->  Gateway  --gRPC-->  im-core
                                      ├─ persist timeline(cid, conv_seq)
                                      ├─ ACK (client_msg_id, msg_id, seq)
                                      └─ Kafka inbox.fanout
                                           ├─ 在线：查 Redis 路由 → 对端 Gateway Push
                                           └─ 离线 / Push 失败：留 inbox，下次 sync 拉
```

1. 客户端生成 `client_msg_id`，先写入 IndexedDB，再经 WSS 发出。
2. `im-core` 写入 `timeline(cid, conv_seq)`（正文只这一份），分配 `sync_seq`，立刻 ACK。
3. Kafka 异步写入各成员 `inbox(uid, sync_seq)`，只存指针，不复制正文。
4. 在线用户：查 Redis 路由，命中的网关 Push。
5. 离线或 Push 失败：下次重连带上 `last_sync_seq` 拉增量。

## 可靠性步骤

| 步骤 | 谁做 | 关键字段 | 失败怎么恢复 |
|---|---|---|---|
| 本地入队 | Web | `client_msg_id`, `cid`, payload | 刷新页面后从 IndexedDB 继续发 |
| 发送 | Gateway → im-core | JWT, `device_id`, `client_msg_id` | 超时重试；服务端按 `client_msg_id` 幂等 |
| 落库 | im-core | `msg_id`, `conv_seq`, `sync_seq` | 写时间线成功才 ACK；inbox 异步可重放 |
| ACK | Gateway → Web | `client_msg_id` → `msg_id` + seq | 未 ACK 保持发送中，指数退避 |
| 在线推送 | fanout → 对端 Gateway | inbox 指针，不推全文大媒体 | 推失败留 inbox，对端下次 sync 拉 |
| 重连补齐 | Web | `last_sync_seq` | 拉增量事件再按 cid 拉缺口消息 |

## 双层存储

| 结构 | 键 | 存什么 |
|---|---|---|
| 会话时间线 | `(cid, conv_seq)` | 消息正文，全员共享，只写一次 |
| 用户 inbox | `(uid, sync_seq)` | 指针：cid、msg_id、未读增量、会话列表变更 |

客户端本地还要保存：发送队列、`last_sync_seq`、按会话的已拉 `conv_seq`。多 Tab 用 `BroadcastChannel` 共享同一条 WSS，避免重复连接。

## 群扇出策略

| 群规模 | 策略 | 原因 |
|---|---|---|
| ≤ 200（P1） | 写扩散指针：在线推 + 离线 inbox | 成员少，延迟优先 |
| 201–2000（P2） | 在线推指针；离线只记会话未读，打开再拉时间线 | 避免 inbox 写放大 |
| 更大 | 不做，或拆频道/直播间 | IM 会话模型会先被写放大打穿 |

## 媒体

图片和文件禁止走 WSS。

1. 客户端向 `media-svc` 申请预签名 PUT。
2. 直传 OSS，消息里只带 object key、尺寸、缩略图信息。
3. 展示走 CDN。网关只传信令，不被文件字节打满。

## 建议冻结的协议帧

| 帧 | 阶段 | 方向 | 作用 |
|---|---|---|---|
| `auth` | P0 | C→S | JWT 绑定连接与 `device_id` |
| `heartbeat` | P0 | 双向 | 30–45s，空闲 90s 踢连接 |
| `send` | P0 | C→S | `client_msg_id` + cid / peer_uid + payload；群聊必带 cid |
| `ack` | P0 | S→C | 映射 `client_msg_id` → `msg_id` + seq |
| `push` | P0 | S→C | inbox 指针或轻量消息 |
| `sync` | P0 | C→S | 携带 `last_sync_seq` 拉增量 |
| `sync_resp` | P0 | S→C | 增量事件列表 |
| `recall` / `recalled` | P1 | 双向 | 2 分钟内撤回，广播会话成员 |
| `typing` | P1 | 双向 | 正在输入，网关节流转发 |
| `read` | P1 | 双向 | 单聊已读回执（`conv_seq`） |

编码：Protobuf over WebSocket 二进制帧；本地调试可用 protojson 文本帧。HTTP 用于 CRUD（好友、建群、拉人踢人）与后续上传凭证。
