# im-core

无状态消息服务：单聊时间线、ACK 幂等、inbox、会话列表。

- 正文只写 `messages(cid, conv_seq)` 一次
- inbox 按 uid 追加 `sync_seq` 指针
- `client_msg_id` 按发送方去重
- 同一 `cid` 进程内串行写；seq 用 Redis INCR

```bash
export MYSQL_DSN='surge:surge@tcp(127.0.0.1:3306)/surge?parseTime=true&charset=utf8mb4'
export REDIS_ADDR=127.0.0.1:6379
go run ./im-core
```
