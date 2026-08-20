# 架构分层

五层各做一件事：客户端负责本地可靠；网关负责连接；`im-core` 负责顺序与落库；Kafka 负责扇出；存储按冷热拆开。

架构图见 [im-architecture.html](im-architecture.html)。

## 分层总表

| 层 | 组件 | 职责 | 扩容方式 |
|---|---|---|---|
| 01 客户端 | React + IndexedDB | 本地队列、重试、`last_seq`、多 Tab 同步 | CDN 静态资源 |
| 02 接入 | CDN / WAF / LB / HTTP GW / WS GW | TLS、鉴权、连接保持、协议编解码 | Gateway 按连接数水平加机器 |
| 03 应用 | auth / user / relation / im-core / presence / media | 无状态业务；im-core 对单个会话串行写 | 按 CPU 扩副本；会话用 cid 哈希亲和 |
| 04 事件 | Kafka | 持久化后的 inbox 扇出、会话列表通知、媒体转码 | 按 topic 分区 = cid 或 uid |
| 05 数据 | Redis / TiDB / 消息库 / OSS / OpenSearch | 路由与 seq 走 Redis；关系走 SQL；消息按 cid 分片 | P0 单机 → P2 集群与分片 |

## 接入拆分

| 通道 | 用途 |
|---|---|
| HTTPS | 登录、资料、历史分页、媒体上传凭证 |
| WSS | 发送、接收、ACK、在线状态、正在输入 |

网关持有 TCP；Redis 持有 `uid → node` 路由。业务 Pod 保持无状态。

## 关键服务

### ws-gateway（有状态）

只持有 TCP 与 `conn_id`。登录后把 `uid → gateway_id + conn_id` 写入 Redis，心跳 30–45 秒。业务消息全部转 gRPC，网关不落库。

- 单节点目标：50–80k 连接
- 1M 在线：约 16–20 台
- 推送路径：im-core / fanout worker 查路由表 → 命中的网关节点 Push
- 用户重连换节点时覆盖路由，旧连接踢掉

### im-core（顺序写）

会话时间线 `timeline(cid, conv_seq)` 只存一份正文。用户收件箱 `inbox(uid, sync_seq)` 只存指针（cid、msg_id、未读增量）。群消息禁止把正文复制 N 份。

同一 `cid` 必须单写者：进程内 channel，或 Redis 锁 + 分区。ACK 返回 `client_msg_id`、`server_msg_id`、两个 seq。

## 服务清单

| 服务 | P0 | P1 | P2 | 接口形态 |
|---|---|---|---|---|
| auth-svc | 密码登录 JWT | 二维码登录、多设备 | 风控会话 | HTTPS |
| user-svc | 资料 / 头像 | 搜索用户 | 推荐/备注 | HTTPS |
| relation-svc | 直接加好友 | 申请与通过、黑名单 | 分组 | HTTPS |
| im-core | 单聊文本 + 会话列表 | 群 ≤200、撤回、已读 | 分片、大群 ≤2000 | gRPC + WSS |
| presence-svc | 在线/离线 | 正在输入 | 精确心跳与隐私 | WSS |
| media-svc | — | 预签名上传、缩略图 | 转码与审核 | HTTPS + OSS |
| search-svc | — | — | 历史消息搜索 | HTTPS + OpenSearch |

## 可观测性

SLO 建议：

- 连接成功率 99.9%
- 发送 p99 &lt; 200ms
- 推送 p99 &lt; 500ms

组件：Prometheus（连接 / QPS / 积压）、Grafana、Loki（网关日志）、OpenTelemetry（send → persist → push）、告警（连接跌落 / Kafka lag）。
