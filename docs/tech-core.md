# Surge IM 核心技术点

本文写「怎么做成的、为什么这样设计、关键路径怎么走」。功能清单见 [features.md](features.md)；分层总览见 [architecture.md](architecture.md)；发送/seq 摘要见 [messaging.md](messaging.md)。

读者对象：接手或 review 本仓库的工程师。对照路径以 `im-core/`、`gateway/`、`pkg/conv/`、`proto/im/v1/`、`web/app.js` 为准。

**当前落地边界**：单机 Gateway + 单进程 im-core + MySQL 8 + Redis 7 + MinIO。扇出是 **同一事务里写 inbox 指针 + Redis Pub/Sub 在线推**。Kafka、会话分片、网关水平扩展属于 P2，**未落地**，下文凡提到这些都标「未做」。

---

## 目录

1. [设计原则](#1-设计原则)
2. [进程与数据面](#2-进程与数据面)
3. [会话模型](#3-会话模型)
4. [实时通道](#4-实时通道)
5. [发送路径](#5-发送路径)
6. [同步与未读](#6-同步与未读)
7. [群扇出与群模式](#7-群扇出与群模式)
8. [媒体](#8-媒体)
9. [存储](#9-存储)
10. [安全](#10-安全)
11. [前端关键点](#11-前端关键点)
12. [明确不做与已知限制](#12-明确不做与已知限制)

---

## 1. 设计原则

五条约束贯穿代码。架构文档也写过它们；这里用**实际调用链**印证，避免口号化。

| # | 约束 | 代码怎么兑现 | 为什么 |
|---|---|---|---|
| 1 | 网关只持 TCP，业务不持连接 | `gateway/ws.go` 升级 `/v1/ws`，`Hub.byUID` 记本机连接；`im-core` 是无状态 gRPC（`im-core/main.go` 只 Listen `:9000`） | 连接扩容不能拖垮落库。杀网关进程只丢 socket，消息已在 MySQL |
| 2 | 时间线正文只写一次；inbox 只存指针 | `messages` 按 `(cid, conv_seq)` 唯一；`inbox` 只有 `cid/msg_id/conv_seq`，见 `im-core/schema.sql` | 群消息禁止把正文复制 N 份。200 人群 = 1 行 messages + 最多 200 行 inbox |
| 3 | 至少一次投递；用 `client_msg_id` 去重 | `UNIQUE (from_uid, client_msg_id)`；重复 Send 走 `loadDup` 返回 `Ack.duplicate=true` | 恰好一次要分布式事务，代价高于收益。客户端超时重试是常态 |
| 4 | 图片/文件禁止走 WSS | `validateSend` 要求媒体带 `object_key`；字节走 `POST /v1/media/presign` → MinIO PUT | 64KB 读限制 + 信令通道不能被文件打满 |
| 5 | P0–P2 不做朋友圈、支付、小程序、音视频 | 无对应 RPC / 路由 | 长连接网关不能和 RTC/Feed 抢资源 |

补充两条实现层约定，不在 README 那五条里，但同样硬：

- **同一 `cid` 进程内串行写**。`im-core/server.go` `Send` 先 `s.locks.lock(canonical)`。`conv_seq` 来自 Redis `INCR seq:conv:{cid}`，锁防止并发 Send 把时间线插乱。锁 map 只增不删（群多了会占一点内存）。多 im-core 副本时这把锁不够（P2 才做 cid 亲和）。
- **HTTP 不发消息**。`gateway/http.go` `routes()` 没有 `/v1/send`。CRUD、历史、媒体凭证走 HTTPS；发送/ACK/同步/撤回/输入/已读走 WSS。
- **先持久化再 ACK**。`tx.Commit` 成功才构造 `Ack`。Push 在 Commit 之后，失败不回滚。客户端以 ACK 为准认为「服务器已收下」。

---

## 2. 进程与数据面

Compose 拓扑见仓库根 `docker-compose.yml`。浏览器只看见 Gateway。

```
浏览器
  HTTPS  :8080   登录 / CRUD / 时间线 / 预签名
  WSS    /v1/ws  发送、ACK、sync、push、recall、typing、read
        │
        ▼
  gateway  (GATEWAY_ID=gw-1)
        │  持有 WebSocket；JWT 鉴权；不落消息库
        │  gRPC → im-core:9000
        │  Redis 写 route:{uid}；订阅 gw:{GATEWAY_ID}
        │  MinIO SDK 签 PUT（对象字节不经 gateway）
        ▼
  im-core  :9000  gRPC
        │  无连接；写 MySQL；INCR Redis seq；Publish 到 gw:{id}
        ├─ MySQL 3306   权威数据
        ├─ Redis 6379   路由、seq、扫码、限流、refresh
        └─ 离线时 SMTP / SMS_WEBHOOK（可选）

  MinIO
        容器内 :9000，映射主机 9001（S3 API）/ 9002（控制台）
        浏览器直传 PUT；GET 走 MINIO_PUBLIC_URL
```

### 2.1 谁持连接、谁落库

| 组件 | 进程入口 | 持什么 | 不持什么 |
|---|---|---|---|
| 浏览器 | `web/` 静态页，Gateway FileServer | 一条 WSS（多 Tab 选举后）；IndexedDB 队列 | 服务端连接表 |
| Gateway | `gateway/main.go` `:8080` | `Hub.byConn` / `byUID`；本机 socket | 消息正文、会话列表 |
| im-core | `im-core/main.go` `:9000` | 无；每次 RPC 短生命周期 | TCP 会话 |
| MySQL | 8.0 `surge` 库 | messages / inbox / conversations / users / groups… | 在线路由表 |
| Redis | 7 | `route:`、`seq:`、`rl:`、`refresh:`、`qrlogin:`、Pub/Sub `gw:` | 消息权威副本 |
| MinIO | S3 | 对象字节 | 消息元数据（元数据在 messages.payload_media） |

Gateway 启动时 Redis ping 失败会把 `rdb` 置 `nil`（`gateway/main.go`），本机限流和扫码改走内存。**im-core 启动 Redis ping 失败直接 Fatal**：`conv_seq` / `sync_seq` 依赖 `INCR`。

### 2.2 端口与环境

| 对外 | 默认 | 环境变量 |
|---|---|---|
| 用户入口 | `http://127.0.0.1:8080` | `GATEWAY_ADDR` |
| im-core gRPC | `127.0.0.1:9000` | `IMCORE_ADDR`（gateway 侧填 `im-core:9000`） |
| MySQL | `3306` | `MYSQL_DSN` |
| Redis | `6379` | `REDIS_ADDR` |
| MinIO S3 | 主机 `9001` → 容器 `9000` | `MINIO_ENDPOINT=minio:9000`，`MINIO_PUBLIC_URL=http://127.0.0.1:9001` |

JWT 默认密钥 `surge-dev-secret`（`JWT_SECRET`）。生产必须换。

### 2.3 请求怎么进业务

HTTPS：`gateway/http.go` 解析 Bearer JWT → 取出 `uid` → 调 `imv1.IMCoreClient`。Gateway 是 BFF，不自己写 SQL。

WSS：`gateway/ws.go` `dispatch` 按 Envelope oneof 分发；`onSend` / `onSync` / `onRecall` / `onTyping` / `onRead` 全部转 gRPC。网关不改 payload，只填 `from_uid`（来自已鉴权连接）。

反向推送：im-core `publish` → Redis `PUBLISH gw:{gateway_id}` protobuf `GatewayPush` → 该节点 `Hub.listenPushes` → `Hub.push` 写 socket。

### 2.4 两平面怎么拆

| 平面 | 形态 | 典型入口 | 禁止放什么 |
|---|---|---|---|
| 控制面 | HTTPS JSON / protojson | `gateway/http.go` `routes()` | 发消息、ACK、inbox sync |
| 数据面（信令） | WSS Envelope | `/v1/ws` | 媒体字节、好友 CRUD、建群 |
| 数据面（对象） | 浏览器 → MinIO PUT/GET | presign 给出的 URL | 经 Gateway 中转文件 |

控制面按资源分，不是按「微信每个按钮一个微服务」。auth / user / relation / media 都是 **Gateway 转 im-core 同一个 gRPC 服务**。architecture.md 里的 auth-svc、user-svc 是目标拆分；当前一个 `IMCore` 接口打满（`proto/im/v1/core.proto`）。

`GatewayPush` 的 oneof 式字段（实际是并列 optional）：`Push` / `Typing` / `Recalled` / `Read`。`Hub.push` 按哪个非空选 Envelope。踢人走旁路 JSON，不是这个 protobuf。

### 2.5 进程内关键对象

| 对象 | 文件 | 生命周期 |
|---|---|---|
| `Hub` | `gateway/hub.go` | 进程级；订阅 `gw:{id}` 直到 SIGTERM |
| `Conn` | 同上 | 一条 WSS：`id=uuid`，`send chan` 容量 64 |
| `server.locks` | `im-core/server.go` | 进程级 `map[cid]*Mutex`，只增不删 |
| `mysqlStore` | `im-core/mysql.go` | 进程级，`MaxOpenConns=32` |
| `redisSeq` / `redisRouter` | `seq.go` / `route.go` | 包装同一 Redis client |
| `mailer` | `im-core/notify.go` | 可选；`SMTP_HOST`+`SMTP_FROM` / `SMS_WEBHOOK` |

---

## 3. 会话模型

会话主键是 **cid**，不是「两人各一份聊天记录」。规则在 `pkg/conv/cid.go`。

### 3.1 cid 规则

| 类型 | 格式 | 谁生成 | 不变量 |
|---|---|---|---|
| 单聊 | `p2p:{min_uid}:{max_uid}` | `conv.P2P(a,b)` 字典序 | 双方算出同一个 cid；`left < right`，否则 `splitP2P` 报 not canonical |
| 群聊 | `grp:{uuid}` | `im-core/groups_mysql.go` `CreateGroup`：`conv.GroupPrefix() + uuid` | uuid 无序，不能从 cid 反推成员 |
| 文件助手 | `p2p:filehelper:{uid}`（filehelper 字典序更小） | 对 `peer=filehelper` 走普通 P2P | `pkg/conv/filehelper.go`：`FileHelperUID = "filehelper"`，Send 跳过好友检查 |
| 花名册信令 | `sys:roster` | 常量 `rosterCID`（`im-core/server.go`） | **不写 timeline**；只 Push 给单用户，刷新通讯录 |

`conv.ResolveCID(from, cid, peer)`：有 cid 用 cid；群 cid 不解析 peer；单聊可只传 `peer_uid`。不能和自己聊（`cannot chat with self`）。

前端镜像：`web/app.js` `p2pCid(a,b)` 同样按字典序拼接。

### 3.2 两层存储：正文 vs 指针

```
messages   (cid, conv_seq)     全员共享的一条时间线，正文只这一份
inbox      (uid, sync_seq)     该用户的增量事件流：指向 messages 的指针
conversations (uid, cid)       会话列表行：last_text、unread、pin/mute/hide
```

**为什么 inbox 只存指针**：一条群消息若把 `payload_text` 复制给 200 个成员，存储和写入放大是线性的；成员读历史时应按 cid 拉时间线，而不是从自己的 inbox 拼全文。inbox 的职责是：

1. 给每个用户一条**单调递增**的 `sync_seq`（断线补齐游标）。
2. 驱动会话列表 `unread++`、`last_text`、`updated_at_ms`。
3. 在线 Push 时带上指针；P0 为了少一次 RTT，**WSS Push 帧里仍附带 Payload**（`frame.proto` 注释：inbox pointer plus P0 text body）。表里没有正文，帧上可以有。

`conversations` 是每个用户一份的物化视图，不是时间线。隐藏会话只改 `hidden`；清空聊天只抬 `cleared_seq`，不删 `messages`（别人还要看）。

### 3.3 两个 seq

| 字段 | 作用域 | 分配 | Redis key |
|---|---|---|---|
| `conv_seq` | 一个 cid 的时间线位置 | `INCR seq:conv:{cid}` | `im-core/seq.go` `convSeqKey` |
| `sync_seq` | 一个 uid 的 inbox 位置 | `INCR seq:sync:{uid}` | `syncSeqKey` |

ACK 同时带回两者：`Ack.conv_seq` 给会话内排序；`Ack.sync_seq` 是**发送者自己**这条 inbox 的序号（`mysql.go` 里 `senderSync`）。对端的 `sync_seq` 在各自 inbox 行上，互不相同。

MySQL 还有 `UNIQUE (cid, conv_seq)`。Redis 与表必须一致；没有从 DB `MAX(conv_seq)` 回填 Redis 的补偿（重启 Redis 会从 1 再 INCR，可能撞唯一键——已知限制）。

---

## 4. 实时通道

唯一 WSS 载荷是 `Envelope`（`proto/im/v1/frame.proto`）。默认二进制 protobuf；本地调试可用 protojson 文本帧。编解码：`pkg/wsframe/codec.go`。前端目前 `JSON.stringify` 发文本帧。

读限制：`SetReadLimit(64 << 10)`（64KB）。空闲：`IdleTimeout=90s`（`gateway/config.go`），心跳刷新 deadline。客户端 30s 发一次 `heartbeat`（`web/app.js` `setInterval(..., 30000)`）。

### 4.1 Envelope oneof

| 字段 | 方向 | 处理函数 | 作用 |
|---|---|---|---|
| `auth` / `auth_ok` | C→S / S→C | `onAuth` | JWT 绑定 `uid`+`device_id`；返回 `gateway_id`、`conn_id`、`last_sync_seq`（Watermark） |
| `heartbeat` | 双向 | `dispatch` | 刷新 Redis `route:{uid}` TTL（90s）；回当前 `ts_ms` |
| `send` | C→S | `onSend` | `client_msg_id` + cid/peer + payload |
| `ack` | S→C | — | `client_msg_id → msg_id + conv_seq + sync_seq`；可能 `duplicate=true` |
| `push` | S→C | — | 对端新消息（含 payload） |
| `sync` / `sync_resp` | C→S / S→C | `onSync` | `last_sync_seq` 拉 inbox 增量 |
| `recall` / `recalled` | 双向 | `onRecall` | 2 分钟内撤回 |
| `typing` | 双向 | `onTyping` | 不落库；`FanoutTyping` 转推 |
| `read` | C→S（回执 S→C） | `onRead` | `MarkRead`；单聊会推给对端 |
| `error` | S→C | — | `code` + `client_msg_id`（发送失败要对上本地队列） |

未鉴权就 `send`：401 `auth required`。未知 oneof：400 `unsupported frame`。

### 4.1.1 编解码坑（对着 `pkg/wsframe` 读）

前端发文本 JSON，字段是 camelCase（`clientMsgId`、`convSeq`）。`protojson` 认 proto 名或 json_name。`codec.go` 还做了两件补丁：

1. **`rewritePayloadTypeEnums`**：JS 常写 `"type":"TEXT"` 或 `"type":1` 或 `"type":"1"`。`DiscardUnknown` 会把未识别枚举丢掉，变成 `TYPE_UNSPECIFIED`。补丁在 Unmarshal 前改写成 proto 枚举名。`normalizeSendPayload`（`im-core/payload.go`）是第二道：Type=0 时根据 media/text 猜 IMAGE/FILE/VIDEO/CARD/MERGE/TEXT。
2. **孤立 UTF-16 代理项**：JSON 里坏掉的 `\ud83d` 会让 protojson 失败；`rewriteLoneJSONSurrogates` 修掉后再解。

二进制帧走 `proto.Unmarshal`，压测工具若改用二进制可避开上述 JSON 坑。`g200` 和 `p0smoke` 目前发文本帧。

`Payload.Type` 落地值：

| 枚举 | 值 | 校验要点 |
|---|---|---|
| TEXT | 1 | 非空，≤4000 rune |
| RECALL | 2 | **不能**经 Send 出现；只由 Recall RPC 把行改成这种类型 |
| SYSTEM | 3 | 非空；群模式 ephemeral 不强制烧系统消息 |
| IMAGE | 4 | `media.object_key`；贴纸也走 IMAGE + `sticker_id` |
| FILE | 5 | 含语音（`audio/*` 或文件名） |
| VIDEO | 6 | 同上 object_key |
| CARD | 7 | `card_uid` |
| MERGE | 8 | `merge_items` 非空 |

### 4.2 至少一次 + client_msg_id

发送侧：

1. 浏览器 `uuid()` 生成 `client_msg_id`，写入 outbox + IndexedDB，再 `sendFrame`。
2. 服务端 `validateSend`：`client_msg_id` 必填、≤64 字符。
3. `loadDup` 先查 `(from_uid, client_msg_id)`；命中则原样 ACK，`duplicate=true`，不再写库、不再扇出。
4. INSERT 撞 `uk_sender_client` 同样走 `loadDup`。

这覆盖：超时重试、多 Tab 重复点发送、刷新页面 flushOutbox。**不覆盖**「两个设备用不同 client_msg_id 发同一句话」——那是两条消息。

投递侧：写库成功才 ACK；Push 走 Redis，失败不回滚。对端靠下次 `sync` 补。Hub 发送队列满（64）会 **丢帧**（`enqueue` default 分支打日志）。所以语义是至少一次尝试，不是端到端保证可见。

### 4.3 Gateway 持连接、im-core 无连接

`Hub`（`gateway/hub.go`）：

- `byConn[conn_id]`、`byUID[uid][conn_id]`：本节点连接。
- `bind` **不再踢旧连接**（函数返回 `nil`）。多端同时在线是有意的。
- `registerRoute`：`route.Upsert` 把 `{gateway_id, conn_id, device_id}` 写入 `route:{uid}`，TTL 90s。
- `LookupAll`：im-core 对一个 uid 的**所有设备**各 Publish 一次。

im-core 的 `Router`（`im-core/route.go`）只认 Redis。没有 Redis 时 `publish` 直接走离线通知。

踢人：`GET /v1/devices` 列本机 + Redis 路由；`POST /v1/devices` `{conn_id}` 调 `Hub.kick`：本机 `kickLocal` 关 socket，并向其他 `gateway_id` 发 JSON `{"kick":"1"}`（与 protobuf GatewayPush 混在同一 channel，`listenPushes` 先用字符串探测 `"kick"`）。不能踢当前 device（`isSelfDevice`）。

### 4.4 多 Tab 一条 WSS

这是**浏览器侧**约束，网关仍接受同一 uid 多条连接。`web/app.js` `startWSElection`：

- `BroadcastChannel("surge-ws:" + uid)`，通道带 uid，避免 A 账号的 send 骑上 B 的 socket。
- 消息类型：`elect` / `leader` / `send` / `frame`。
- 400ms 内听到别人的 `leader` → 本 Tab 跟随；否则 `becomeLeader()` 真正 `new WebSocket`。
- Follower 的 `sendFrame` 把 env post 给 Leader；Leader 把收到的帧 `broadcastFrame` 给 Follower。
- 800ms 心跳；Leader 失联 >2s，Follower 抢接。
- 无 `BroadcastChannel` 时每个 Tab 自己连（降级）。

服务端不知道选举。多 Tab 若都当 Leader，会变成多连接，路由表里多条 Record，Push 会推多次；前端 `ingest` 用 `msg_id` 去重。

---

## 5. 发送路径

### 5.1 总路径（已落地，无 Kafka）

```
Web  sendFrame({send})
  → Gateway onSend
       限流 rl:send:{uid}  10s 内 >40 → 429
       gRPC IMCore.Send
  → im-core server.Send
       ResolveCID
       lock(cid)
       单聊：黑名单 / 好友（filehelper 例外）
       store.Send
  → mysqlStore.Send
       validateSend + 敏感词
       loadDup？
       targets()：群则 canSpeak + 成员列表
       群 ephemeral 模式强制 payload.ephemeral
       INCR conv_seq
       BEGIN
         INSERT messages          -- 正文一份
         对每个 member:
           INCR sync_seq
           INSERT inbox           -- 指针
           upsert conversations   -- unread+1（非自己）
           @ 则 unread_mention+1
       COMMIT
       返回 Ack + deliveries[]
  → server.publish 每个 delivery
       Redis LookupAll(uid)
       有路由：PUBLISH gw:{id} GatewayPush{Push}
       无路由：notifyOffline（邮件/短信，若配置）
  → Gateway listenPushes → Hub.push → 对端 Envelope.push
  → 发送者 Envelope.ack
```

HTTP **没有**对等的发消息接口。历史用 `GET /v1/timeline`，会话列表 `GET /v1/conversations`。

### 5.2 Send 里会卡住的检查

`im-core/server.go` `Send` + `mysqlStore.targets` + `validateSend`：

| 条件 | 错误 | 谁检查 |
|---|---|---|
| cid/uid 非法、自己和自己聊 | InvalidArgument | `conv.ResolveCID` |
| 未互为好友（非 filehelper） | PermissionDenied `add friend first` | `AreFriends` |
| 拉黑 | PermissionDenied `user blocked` | `IsBlocked` |
| 非群成员 | PermissionDenied | `targets` |
| 全员禁言 / 个人禁言（群主除外） | `group muted` | `canSpeak` |
| 空文本、无 object_key、client_msg_id 空或过长、文本 >4000 字 | InvalidArgument | `validateSend` |
| 直接发 `RECALL` payload | InvalidArgument | 撤回走独立 RPC |
| 限流 | WSS error 429 | Gateway，到不了 im-core |

群模式如何进一步卡住，见第 7 节。它们多数在 **进群/发言权**，不在 payload 类型。

### 5.3 ACK 之后发生什么

发送者：本地把 outbox 该项标 `acked`，用 `msg_id`/`conv_seq` 替换乐观消息。`duplicate` 时前端仍当成功（同一条）。

接收者：若在线，Push 进 `ingest`：更新 `lastSyncSeq`，当前会话则插入气泡（`msg_id` 去重），然后 `loadConvs`。若不在当前会话，只刷新列表未读。

离线：inbox 已有行。下次 WSS `auth_ok` 带 Watermark；客户端却把自己存的 `last_sync_seq` 覆盖成 watermark 再 sync——见第 6 节「坑」。

### 5.4 系统消息也走 Send

拉人、踢人、禁言、改名等调用 `notifyGroup`：`Send` + `Payload.SYSTEM` + `client_msg_id = "sys-" + uuid`。因此系统消息占用 `conv_seq`，出现在时间线上，并给每个成员写 inbox。

例外：`notifyRoster` 只 Push `sys:roster`，不写 messages。前端 `isRosterPush` 用来刷新好友/申请，不当聊天记录。

### 5.5 撤回

`recallWindowMS = 2 * 60 * 1000`。`mysqlStore.Recall`：

1. 按 `msg_id + cid` 取 `from_uid`、`created_at_ms`。
2. 只有发送者能撤；超时 `recall window exceeded`。
3. `UPDATE messages SET recalled=1, payload_type=RECALL, payload_text=''`。
4. 若该条仍是会话 `last_msg_id`，`last_text` 改成「已撤回一条消息」。
5. 成员列表：群走 `targets`（此时仍要能 `canSpeak`——群主撤自己的消息没问题；被禁言成员在窗口内撤回自己的消息会在 `targets` 里再走 `canSpeak`，**可能撤不掉**）。单聊 `PeerUID`。
6. `server.Recall` 对除自己外的成员 `publish Recalled`。发送者前端先 `applyRecall` 乐观更新。

### 5.6 正在输入与已读回执

`onTyping` 不 ACK、不落库。`FanoutTyping`：`hide_typing` 则吞掉；群推给其他成员，单聊推 peer。前端 2s 节流。这是纯在线信令，断线没有「正在输入」补齐。

`onRead` 调 `MarkRead`，不给发送者 ACK 帧。单聊对端收到 `Envelope.read` 更新 `peerReadSeq`（双勾）。群已读靠打开会话后 HTTP `GET /v1/read-state` 聚合 `read_cursors`。

### 5.7 引用、@、预览

- 引用：`SendRequest.quote_msg_id` → `enrichPayload` 把被引正文 clip 到 200 字写入 `quote_text`，避免接收方再查。
- `@`：客户端可带 `mention_uids`；为空则正则 `@([A-Za-z0-9._@+-]{1,64})` 从文本抽。`@所有人` 给每个非发送者 `unread_mention++`。
- 链接预览：前端先 `POST /v1/link-preview`，把结果放进 `payload.link` 再 send。Gateway 抓标题，失败则无预览，消息照发。

### 5.8 会话列表如何被写路径驱动

`ListConversations`（`mysql.go`）：`hidden=0`，`ORDER BY pinned DESC, updated_at_ms DESC`。Join `conv_mutes`、`im_groups`（群头像与 mode 塞进 `peer_profile.email = "grp:"+mode` 这一偏方，前端用它读群模式）。

每条 Send 的 `upsertConv` 会：更新 last_*、非自己 unread+1、**强制 hidden=0**。所以「删除聊天」= 清记录 + 隐藏，对方再发会重新出现。置顶/免打扰是独立表/列，不走 WSS。

---

## 6. 同步与未读

三套游标不要混：

| 游标 | 存在哪 | 含义 |
|---|---|---|
| `last_sync_seq` | 客户端 IndexedDB `{uid}:seq`；权威在 `MAX(inbox.sync_seq)` | 「我的 inbox 看到哪了」 |
| `read_cursors.conv_seq` | MySQL `(uid,cid)` | 「这个会话我已读到哪条」 |
| `conversations.unread` | MySQL | 列表红点；发送时对非自己 +1，MarkRead 清零 |

### 6.1 Watermark 与 Sync

`Watermark`：`SELECT MAX(sync_seq) FROM inbox WHERE uid=?`（`mysql.go`）。`auth_ok.last_sync_seq` 就是它。

`Sync(uid, last_sync_seq, limit)`：`inbox JOIN messages WHERE sync_seq > last`，升序，默认 limit 100。`has_more` 时客户端继续带新的 last 拉。`InboxEvent` 带 payload（join 了 messages），所以 sync 不需要再 GetTimeline 才能渲染提示。

前端 `onFrame` 收到 `auth_ok` 后：

```
state.lastSyncSeq = auth_ok.last_sync_seq
kvSet(uid+":seq", ...)
sendFrame({ sync: { lastSyncSeq, limit: 200 } })
flushOutbox()
```

注意：这里用 **watermark 覆盖本地 last_sync_seq**。若本地更旧，会跳过中间事件、只靠会话列表/打开聊天拉时间线。若本地更新（少见），sync 为空。这是实现选择，不是严格「从本地游标续拉」。断线补齐的可靠路径仍是：打开会话 `GET /v1/timeline`。

### 6.2 GetTimeline：after / before

RPC：`GetTimelineRequest`：`after_conv_seq`、`before_conv_seq`、`limit`、`query`。

HTTP：`GET /v1/timeline?cid=&after=&before=&limit=&q=`。

`mysqlStore.TimelineQuery`（`im-core/extras_mysql.go`）：

- 群：必须是成员，或至少有一条 `conversations` 行（退群后仍可能看自己那份列表，但 `isMember` 失败且无会话行则 `not a member`）。
- 单聊：`PeerUID` 校验你属于该 cid。
- 过滤：`conv_seq > cleared_seq`；排除 `hidden_messages`（单向删除）。
- `after_seq>0`：`conv_seq > after`，**升序**（补缺口）。
- `before_seq>0` 或 `after==0`：**降序** LIMIT（打开会话、上翻历史）。
- `query`：`payload_text LIKE`，且 `recalled=0`。
- 多取 1 条算 `has_more`。
- 群 `history_days>0` 时 `filterTimelineHistory`：非群主只保留 `created_at >= joined_at - history_days`。

打开会话：不带 before/after，limit=50，降序最新一页，前端再按 `conv_seq` 排回正序。

上翻：`loadOlder` 取当前最小 `conv_seq` 作为 `before`，`stick:false` 保持滚动位置。

### 6.3 已读 MarkRead

WSS `read` 或业务里打开会话：`MarkRead(uid, cid, conv_seq)`。

`conv_seq > 0`（`groups_mysql.go`）：

1. `read_cursors` upsert，`GREATEST` 只向前。
2. `conversations.unread=0` 且 `unread_mention=0`。
3. 单聊：把 `ReadReceipt` Push 给 peer（对方气泡双勾）。`settings.hide_read` 为真则不推。
4. 群：不推个人回执；`GetReadState` 聚合「N 人已读」。

`conv_seq == 0`：`UPDATE conversations SET unread = GREATEST(unread, 1)` —— **标未读**。HTTP `POST /v1/mark-unread` 就是这个。前端当前会话用 `holdUnreadCid` 禁止自动 `markRead`。

### 6.4 未读从哪来

发送时 `upsertConv(..., incoming=uid!=from)`：`unread = unread + 1`。自己发的不加。隐藏会话被新消息 `hidden=0` 拉回列表。

免打扰（`conv_mutes`）**不阻止 unread++**，只影响通知和角标展示。`@` 另计 `unread_mention`；免打扰会话仍可提示「有人 @ 你」。

---

## 7. 群扇出与群模式

上限 `maxGroupMembers = 200`（`im-core/memory.go` 常量，MySQL 路径同样检查）。超过 `errTooLarge`。压测：`make g200` → `tools/g200`。

### 7.0 角色

`group_members.role`：`owner` = 3，`admin` = 2，其余 = 1（`roleRank`）。

| 动作 | 谁可以 |
|---|---|
| 发言 | 见 `canSpeak`：owner 总可以；admin 在全员禁言下可以；被个人禁言的人不行 |
| 邀请 | 成员即可；`private` 仅 owner |
| 踢人 | admin+ 且职级严格高于对方（不能踢同级/上级） |
| 改资料 / 禁言全员 / 转让 | 基本 owner 或管理员，以各 RPC 为准 |
| 退群 | 非群主；群主需先转让或解散 |

建群：成员必须已是好友；cid=`grp:`+uuid；同时给每个成员插一条 conversations（`last_text=群聊已创建`，无 messages 行）。

### 7.1 写扩散 inbox（已落地）

`targets()` 返回**当前全部成员**（含发送者）。事务内对每人 INSERT inbox + upsert 会话。这就是「≤200 写扩散指针」。

在线 Push 只对 `uid != from`（发送者靠 ACK，不给自己 Push 以免闪烁）。多设备发送者的其他端：当前实现**不会**因这次 Send 收到 Push，靠 sync / 打开会话 / 多 Tab BroadcastChannel 对齐。多设备漫游依赖下次 sync 或主动拉会话。

**P2 未做**：Kafka 异步 inbox、大群只记未读不写 inbox、cid 分片。不要把 architecture.md 里的 Kafka 图当成现状。

### 7.2 系统消息

`notifyGroup` → 普通 `Send(SYSTEM)`，因此：

- 占用 conv_seq，历史里可见。
- 被禁言的普通成员**发不了**系统消息；操作者是群主/管理员才能改设置，他们 `canSpeak` 通过。
- 踢人时 `notifyGroup` 在 `KickGroup` **之前**调用，被踢者仍在 members 里，能收到「被移出」时间线；随后成员表删除。

### 7.3 模式如何卡住 Send / 进群

`im-core/groupmode.go`。`im_groups.mode` 列；`applyGroupMode` 会同步衍生标志。

| mode | 别名 | 衍生状态 | 对 Send | 对进群 |
|---|---|---|---|---|
| `normal` | 默认 | — | 成员可说；个人 `muted` / `muted_until_ms` 仍拦 | 邀请直接进 |
| `verify` | `approval`, `join` | `JoinApproval=true` | 不额外拦发言 | 非管理员邀请写入 `group_join_requests`，管理员 `DecideJoin` |
| `private` | `secret` | — | 不额外拦发言 | `canInvite`：仅群主可邀请 |
| `broadcast` | `notice`, `muted` | `MutedAll=true` | `canSpeak`：仅 owner、admin；群主永远可说 | 同 normal |
| `anonymous` | `anon` | — | **不改 from_uid** | 展示层匿名（见下） |
| `ephemeral` | `burn` | — | `applyEphemeralMode` 把非 SYSTEM 的 `payload.ephemeral=true` | 同 normal |

`canSpeak`（`grouputil.go`）：

1. 非成员 → 不能发。
2. **owner 直接通过**（全员禁言也说）。
3. `memberMuted`：`muted_until_ms` 未过期，或 `muted` 标志。
4. `MutedAll` 且角色不是 admin → 不能发。

前端 `speakBlockedReason` 会提前锁输入框，但权威在服务端。

### 7.4 匿名是展示，不是身份隐藏

库里 `from_uid` 仍是真 uid。`anonLabel(cid, uid)` = SHA256 前 4 字节 → `匿名1000–9999`。前端 `anonNick` 用 FNV 哈希，**与后端算法不同**，同一个人两端标签可能不一致。气泡不展示真 uid；系统文案里的 uid 会被 `maskGroupText` 替换。点头像不会打开资料。

不要把它当成安全匿名。管理员查库仍能看到谁发的。

### 7.5 阅后即焚

两条来源：

- 单聊/普通群：客户端 `payload.ephemeral=true`（设置 `burn_sec` 或开关）。
- 群 mode=`ephemeral`：服务端强制。

接收方看过后 `POST /v1/ephemeral/consume` → `ConsumeEphemeral`：校验 `ephemeral`、**发送者不能自己烧**、把 `payload_media.burned=true` 且 `recalled=1`。之后时间线显示「已销毁」，并 `recalled` 广播。这会改 **共享时间线**，群里一人消费则全员看到销毁。

### 7.6 验证群与广播群的其它卡住点

- 验证：`InviteGroup` 对普通成员 `pending=true`，只插 `group_join_requests`，不插 `group_members`，因此未通过者 **不在 targets 里，收不到群消息**。
- 广播：等同全员禁言；`SetGroupMuteAll` 也会写系统消息。mode 与 `muted_all` 可能被分别设置（`group-mute-all` RPC 只改标志，不一定改 mode 字符串）。

---

## 8. 媒体

原则：WSS 只传 `Media.object_key` 等元数据，**禁止**把文件字节塞进 Envelope（也塞不下 64KB 限制，视频更不行）。

### 8.1 路径

```
POST /v1/media/presign   {filename, content_type, size}
  → gateway/media.go presign
  → 对象键 {uid}/2006/01/02/{uuid}{ext}
  → PresignedPutObject 15 分钟
  → 返回 put_url, get_url, object_key

浏览器 PUT put_url  → MinIO（不经 Gateway）

POST /v1/media/complete  {object_key, filename}
  → StatObject；>20MB 拒绝
  → 图片则解码、生成 .thumb.jpg（最长边 200px JPEG q70）
  → 返回 width/height/thumb_key/urls

WSS send  payload.type=IMAGE|FILE|VIDEO  media.object_key=...
  → validateSend 要求 object_key，拒绝 ".."
```

语音按住录音：前端当 `FILE` + `audio/*`；预览文案 `[语音]`（`previewOf` / `isVoiceFilename`）。没有独立 AUDIO 枚举；protojson 的 `AUDIO` 会被 codec 映射成 FILE。

### 8.2 签名与 Docker 网络

容器内 SDK 打 `minio:9000`。浏览器必须打主机 `127.0.0.1:9001`。`newMediaStore` 若 `MINIO_PUBLIC_URL` 的 Host 与 `MINIO_ENDPOINT` 不同，会再造一个 **signer** 客户端，Transport 是 `refuseTransport`：Presign 只算 URL，禁止真去拨浏览器地址（在容器里 127.0.0.1 是容器自己）。

Bucket 策略公开 `s3:GetObject`。这是演示配置，不是私有桶。

未配 `MINIO_ENDPOINT`：`media==nil`，presign 返回 503。消息仍可发文本。

### 8.3 为什么不走 WSS

- 网关单连接目标按 50–80k 设计，心跳已占 CPU；再传多 MB 会挤掉 ack/push。
- 媒体要断点、进度、缩略图，HTTP PUT 更合适。
- 时间线只存 key，CDN/MinIO 负责字节与缓存。

---

## 9. 存储

### 9.1 MySQL 权威表

启动 `migrate(schema.sql)` + 一串 `ALTER` 补列（`im-core/mysql.go`）。以运行时为准，新列可能只在 ALTER 里。

| 表 | 主键 | 职责 |
|---|---|---|
| `users` | `uid` | 密码哈希、资料、`public_key`（E2EE 公钥）、email/phone |
| `messages` | `msg_id` | 时间线正文。`uk_sender_client (from_uid, client_msg_id)`，`uk_cid_seq (cid, conv_seq)`。`payload_media` JSON 扩展（媒体、@、e2ee、ephemeral、合并转发） |
| `inbox` | `(uid, sync_seq)` | 指针。无 payload 列 |
| `conversations` | `(uid, cid)` | 列表。`unread`、`unread_mention`、`hidden`、`pinned`、`cleared_seq`、`draft_text`、`last_text` |
| `friends` | `(uid, peer_uid)` | 双向各一行（添加时写两边） |
| `friend_requests` | `(from_uid, to_uid)` | 申请；Web 走申请，`POST /v1/friends` 仍可直接加 |
| `blocks` | `(uid, peer_uid)` | 拉黑 |
| `friend_remarks` / `friend_tags` | 备注、标签 | 标签含 `__star__` 星标 |
| `im_groups` | `cid` | 名、群主、头像、`muted_all`、`join_approval`、`mode`、`history_days`、公告 |
| `group_members` | `(cid, uid)` | `role` owner/admin/member、`nickname`、`muted`、`muted_until_ms`、`joined_at_ms` |
| `group_join_requests` | `(cid, uid)` | 验证群待审 |
| `group_invites` | `token` | 邀请链接，7 天 |
| `read_cursors` | `(uid, cid)` | 已读水位 |
| `conv_mutes` | `(uid, cid)` | 免打扰 |
| `hidden_messages` | `(uid, msg_id)` | 单向删除 |
| `message_reactions` | `(msg_id, uid)` | 每用户一条表情 |
| `favorites` | `fav_id`；`uk_uid_msg` | 收藏，自带 payload_json 快照 |
| `stickers` | `id` | 表情商店/自定义 |
| `chat_pins` | `cid` | 会话置顶消息（一群一条） |
| `reports` | `id` | 举报，无审核后台 |
| `user_settings` | `uid` | 暗色、通知、DND、`burn_sec`、`hide_read` 等 |

`payload_text` 仍存明文或 E2EE 密文字符串；扩展字段进 `payload_media` JSON，避免无休止加列。

### 9.2 Redis 用途（当前）

| key / channel | TTL | 谁写 | 干什么 |
|---|---|---|---|
| `route:{uid}` | 90s | Gateway 鉴权/心跳 | JSON 数组 `[{gateway_id, conn_id, device_id}]` |
| `gw:{gateway_id}` | Pub/Sub | im-core Publish；踢人 JSON | 在线 Push |
| `seq:conv:{cid}` | 无 | im-core INCR | conv_seq |
| `seq:sync:{uid}` | 无 | im-core INCR | sync_seq |
| `rl:login:{ip}` | 窗口 1min | Gateway | 登录/注册 ≤30 |
| `rl:send:{uid}` | 10s | Gateway WSS | 发送 ≤40 |
| `rl:forgot:{ip}` | 1min | 忘记密码 ≤8 | |
| `refresh:{token}` | 30 天 | 登录发 opaque refresh | 值 `uid|device_id`；刷新时 Del 再发新的 |
| `qrlogin:{ticket}` | 2min | 扫码 | 状态 pending/approved |

没有 Redis 时 Gateway 降级：扫码用 `qrMem`，限流用 `memLimiter`，路由为空 → 本机 Hub 仍能推**连在本进程**的人，跨进程推不了。im-core 不能没有 Redis。

### 9.4 扫码登录（控制面，但用 Redis）

`gateway/qr.go`，TTL 2 分钟。

1. 未登录页 `GET /v1/auth/qr/new` 拿 ticket，展示 `qr.png`。
2. 已登录端扫码/确认 `POST /v1/auth/qr/approve`（带 JWT），把 session 写进 `qrlogin:{ticket}`。
3. 未登录页轮询 `qr/status`，approved 后拿到 access/refresh。

有 Redis 时跨 Gateway 实例可见同一 ticket；无 Redis 时 `qrMem` 只在本进程。这不是 WSS 流程，但和「谁持会话」有关：approve 走 HTTPS，不占用长连接。

### 9.5 在线状态

`GET /v1/presence?uids=`：有 Redis 则看 `route:{uid}` 是否存在（TTL 90s，靠心跳 Expire）。无 Redis 则看本机 `Hub.isOnline`。没有独立 presence-svc；路由表即在线表。`hide_last_seen` 影响前端展示，不删路由。

### 9.3 memory store 仅测试

`im-core/memory.go` `newMemoryStore` 只在 `*_test.go` 出现。生产 `main` 固定 `newMySQLStore(db, newRedisSeq(rdb))`。测 Send/Sync/群模式不必起 Docker。memory 与 mysql 行为应对齐；发现分叉以 MySQL 为准。

seq 测试用 `memSeq`，生产用 `redisSeq`。

---

## 10. 安全

### 10.1 JWT 与 refresh

`pkg/auth/jwt.go`：HS256，claims `sub=uid`，`did=device_id`。

`gateway/session.go`：

| 票 | TTL | 形态 |
|---|---|---|
| access | 24h（dev-login 7 天） | JWT |
| refresh | 30 天 | 随机 UUID，只存在 Redis |

`POST /v1/auth/refresh`：读 `refresh:{token}` → **立刻 Del**（旋转）→ 再 `issueSession`。无 Redis 则 refresh 接口 503。前端 `api()` 遇 401 先 refresh 再重试一次。

WSS `auth` 只接受 access JWT，不接受 refresh。

默认 `JWT_SECRET=surge-dev-secret`。`CheckOrigin` 恒 true。两者都是演示级。

### 10.2 设备与踢下线

多端：同一 uid 多 `device_id` 可同时在 `route:{uid}` 里。`Hub.bind` 不互踢。

踢下线：设置页设备列表 → `POST /v1/devices` `{conn_id}`。被踢连接收到关闭（kick 路径直接 `ws.Close`，旧代码里 409 `kicked by another connection` 因 `bind` 不再返回被踢连接而基本走不到）。

登录并不作废旧 JWT；踢的是 **socket**。旧 access 在过期前仍能调 HTTP，除非再加 token 黑名单（未做）。

### 10.3 限流

| 键 | 阈值 | 作用点 |
|---|---|---|
| `rl:login:{ip}` | 30 / 分钟 | login / register / dev-login / oauth |
| `rl:send:{uid}` | 40 / 10 秒 | WSS send |
| `rl:forgot:{ip}` | 8 / 分钟 | 找回密码 |

Redis INCR + 首次 Expire；失败回落滑动窗口内存。**不是**令牌桶，也没有按 cid 限流。群 200 扇出时 40 条/10s 仍可能打出 8000 行 inbox。

### 10.4 可选文本 E2EE

只做 **1:1 文本**。群按钮隐藏（`toggleHidden("e2ee-btn", isGroup || helper)`）。

流程（`web/app.js` `e2eePair` / `encryptPayload`）：

1. 浏览器生成密钥对，私钥进 IndexedDB `{uid}:e2ee`，公钥 `POST /v1/e2ee/keys` → `users.public_key`。
2. 发送前用对端公钥加密，`payload.e2ee=true`，`text` 为密文。
3. 服务端当普通 TEXT 存储、转发；`previewOf` 对 e2ee 显示 `[加密消息]`，避免会话列表泄露。
4. 敏感词 `filterSensitive` 仍会扫 `text`——密文里几乎撞不上写死词。
5. 媒体、名片、合并转发不加密。无服务端密钥托管，换机需重新生成（对端看到新指纹）。

这不是 Signal 协议：无双棘轮、无匿名密钥、服务端能做流量分析。产品开关文案已写明「仅文本」。

### 10.5 敏感词（写死 + 环境变量）

`im-core/payload.go`：

```go
var sensitiveWords = []string{"违禁词"}
```

`im-core/accountx.go` `init`：若 `SENSITIVE_WORDS` 非空，按逗号覆盖整个切片。

`enrichPayload` 对 `text` 做 `strings.ReplaceAll` → 等长 `*`。不是拦截发送，是落库前改写。无词库文件、无正则、无审核后台。`reports` 表只插记录。

### 10.6 其它

- 密码：bcrypt。空密码走 `POST /v1/auth/dev-login`（演示）。
- 黑名单：双向发送在 `Send` 入口拦截。
- 对象键：拒绝 `..`；complete 再 Stat，不能指定别人的任意文件以外的……实际上 key 带 uid 前缀但是**没有校验 key 属于当前用户**，知道 key 就能 complete。演示可接受。
- 上传上限 20MB。

---

## 11. 前端关键点

文件：`web/app.js`（单文件应用）、`web/sw.js`、`web/index.html`。不要在业务逻辑里找第二个框架——没有 React 运行时，文档里 architecture 的「React」是目标选型，**当前是原生 JS**。

### 11.1 时间线分页与吸底

- 打开会话：`GET /v1/timeline?cid=&limit=50`，结果按 `conv_seq` 升序渲染。
- 上翻：滚动容器 `#msgs` `scrollTop < 48` → `loadOlder` → `before={当前最小 conv_seq}`。
- `hasMore` 来自服务端；搜索模式（`searchQ`）不上翻。
- `renderMsgs`：`nearBottom = scrollHeight - scrollTop - clientHeight < 96`。新消息默认仅在 nearBottom 或自己发送（`stick:true`）时吸底；上翻传 `{stick:false}`，用旧 `scrollHeight` 差还原位置。
- 未读分隔：`unread-split` 「以下为新消息」。
- 刷新合并：未 ACK 的本地行靠 `clientMsgId` 保留，避免被 timeline 覆盖成消失。

### 11.2 多 Tab Leader

见 §4.4。要点：通道名 `surge-ws:{uid}`；Follower 不连 WSS；Leader 转发 `frame` / 代发 `send`。

### 11.3 IndexedDB

库名 `surge-p0`，对象店 `kv`（键值，不是按消息分表）。

| key | 内容 |
|---|---|
| `{uid}:seq` | last_sync_seq |
| `{uid}:outbox` | 未 ACK 发送队列 |
| `{uid}:e2ee` | 本机密钥对 |

草稿可走服务端 `POST /v1/drafts`，另有「草稿仅本机」设置。消息历史**不以** IndexedDB 为权威，每次打开会话打 timeline。刷新能恢复的是：登录态（localStorage token）、outbox、sync 游标、E2EE 私钥。

`client_msg_id`：发送时 `uuid()`；ACK 按它匹配 outbox；失败 `error.client_msg_id` 标红可点重试。服务端 duplicate 与本地重试是同一 id。

### 11.4 PWA 与缓存版本

`web/sw.js`：

- Cache 名 `surge-im-v5`。改静态资源必须改这个字符串，否则旧 SW 继续伺候旧 JS。
- precache：`/`、`/app.css?v=im-ux11`、`/app.js?v=im-ux11`、`/manifest.json`。query 与 html 引用要一起改。
- `/v1/*` **不缓存**。策略：网络优先，失败回 cache。
- `skipWaiting` + `clients.claim`。

通知点击：聚焦已有窗口或 `openWindow("/")`。

### 11.5 其它与协议对齐的点

- 心跳 30s；断线 1.5s 重连（仅 Leader）。
- typing 前端 2s 节流；`hide_typing` 则不发。
- 打开会话会 `UnhideConversation`（GetTimeline 服务端副作用）。
- 乐观消息：先插入 `status=pending` 再 send；离线扔 outbox。

### 11.6 发送队列（对着 `sendPayload` / `flushOutbox`）

`sendPayload` 顺序：

1. 拉黑 / 禁言前端拦截（服务端仍会再拒）。
2. `@`、链接预览、阅后即焚标志、可选 E2EE。
3. `clientMsgId = uuid()`，`status=pending`，推进 `state.outbox` 和当前 `state.messages`。
4. **先 `kvSet` outbox**，再 `sendFrame`。崩溃也不会丢这条本地记录。
5. 无 WSS（非 Leader 且 channel 也挂了）抛 `offline`，标 `fail`。

`auth_ok` 之后 `flushOutbox`：未 acked 的再 send 一次。服务端按 `client_msg_id` 幂等。ACK 后从 IndexedDB 去掉该项。

重试：失败气泡 `fail-dot` → `retryOrDrop`。不要换新的 `clientMsgId`，否则会变成两条。

### 11.7 HTTP 时间线参数

打开/上翻都走 `GET /v1/timeline`（`gateway/http.go` `timeline`）：

| query | proto 字段 | 前端何时带 |
|---|---|---|
| `cid` | `cid` | 总是 |
| `limit` | `limit` | 50 |
| `before` | `before_conv_seq` | 上翻，值为当前最小 conv_seq |
| `after` | `after_conv_seq` | 本前端几乎不用（缺口补齐以 sync 为主） |
| `q` | `query` | 会话内搜索 |

响应 `has_more`：降序多取了一条。前端 `loadOlder` 若服务端有 more 但去重后 `added=0`，会把 `hasMore` 置 false，避免死循环。

---

## 12. 明确不做与已知限制

### 12.1 产品不做（P0–P2）

朋友圈、视频号、支付、小程序、音视频通话、原生 App、万人群/频道、语义搜索、服务端语音转写、全媒体 E2EE。

### 12.2 架构已写、代码未做（P2）

| 文档里的图 | 现状 |
|---|---|
| Kafka `inbox.fanout` | 同一 MySQL 事务同步写 inbox |
| Gateway 16–20 节点水平扩展 | Compose 单 `gw-1`；路由表已支持多 Record，但没有连接迁移/粘滞 |
| TiDB / 按 cid 分片消息库 | 单 MySQL，`messages` 单表 |
| 201–2000 人「只记未读」 | 硬上限 200，全员写 inbox |
| OpenSearch | `SearchMessages` 是 MySQL `LIKE` |
| 网关 netpoll/gnet | `gorilla/websocket` + 标准库 HTTP |

Review 时不要按 Kafka 消费者去搜代码——没有。

### 12.3 实现限制（接手时要知道）

1. **Redis seq 与 MySQL 无校准**。Redis 空了会从 1 INCR，可能撞 `uk_cid_seq`。
2. **cid 锁只在单 im-core 进程内**。两副本并发写同一群会乱序/唯一键冲突。
3. **Push 队列满丢帧**（64）。靠 sync/timeline 补，但列表未读可能已加。
4. **auth_ok 用 watermark 覆盖本地 last_sync_seq**，不是严格续拉。
5. **发送者其它设备**收不到这次 Send 的 Push。
6. **匿名哈希前后端不一致**。
7. **阅后即焚改共享时间线**，群里一人看等于全员烧。
8. **敏感词写死「违禁词」**，逗号表可覆盖，无运营后台。
9. **E2EE 仅 1:1 文本**，密文当 TEXT 存。
10. **refresh 无 Redis 不可用**；踢设备不作废 JWT。
11. **MinIO 桶公开读**；presign complete 不校验 key 归属。
12. **WSS CheckOrigin 全放行**。
13. **memory store 不能当生产**。
14. **前端不是 React**，与 capacity.md 选型表不一致，以 `web/app.js` 为准。
15. **系统消息走 Send**，全员禁言时若操作者不是 owner/admin 会失败（当前改设置的人通常是管理员）。
16. **`sys:roster` 不落库**，刷新页面靠 HTTP 通讯录接口，不靠 inbox 重放花名册事件。
17. **被禁言成员可能无法撤回**自己的消息（Recall 内部 `targets` 再走 `canSpeak`）。
18. **群 mode 放在 `Conversation.peer_profile.email`**（`grp:{mode}`），这是协议挤位，不是用户邮箱。

### 12.4 对照阅读顺序

1. 先读本文第 3、5、6 节，能讲清 cid / 双 seq / 发送事务。
2. 打开 `im-core/server.go` `Send` 和 `im-core/mysql.go` `Send`。
3. 打开 `gateway/ws.go` `onSend` / `onAuth` 和 `gateway/hub.go` `push`。
4. 打开 `pkg/conv/cid.go`。
5. 功能「有没有」查 [features.md](features.md)；容量数字查 [capacity.md](capacity.md)；分期查 [roadmap.md](roadmap.md)。
