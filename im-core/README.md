# im-core

无状态消息服务：时间线、ACK 幂等、inbox、会话列表、群成员、撤回与已读。

- 正文只写 `messages(cid, conv_seq)` 一次；群聊对成员写扩散 inbox 指针
- inbox 按 uid 追加 `sync_seq` 指针
- `client_msg_id` 按发送方去重
- 同一 `cid` 进程内串行写；seq 用 Redis INCR
- 群上限 200；建群/拉人要求与对方已是好友；撤回窗口 2 分钟

```bash
export MYSQL_DSN='surge:surge@tcp(127.0.0.1:3306)/surge?parseTime=true&charset=utf8mb4'
export REDIS_ADDR=127.0.0.1:6379
go run ./im-core
```
