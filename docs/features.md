# Surge IM 功能清单

对标微信网页版的 Web 单聊 / 群聊。本文只列**当前已落地**的页面功能和后端能力，不含路线图里的 P2（Kafka / 分片 / 百万在线）和 P3（RTC / 朋友圈 / 支付 / 小程序 / 视频号）。

入口：浏览器打开 `http://127.0.0.1:8080`。静态页来自 `web/`；HTTPS 走 Gateway `8080`；实时通道走 `WSS /v1/ws`；落库与业务在 im-core gRPC。

---

## 1. 产品边界

| 做 | 不做 |
|---|---|
| Web 三栏：会话 / 通讯录 / 收藏 + 聊天 | 原生 App、桌面独立端 |
| 单聊、群聊、文件传输助手 | 音视频通话、会议 |
| 文本、图片、视频、文件、语音、贴纸、名片、合并转发 | 朋友圈、视频号、支付、小程序 |
| HTTPS CRUD + WSS send/ack/sync | 消息正文走 WebSocket 传媒体 |
| 可选文本端到端加密 | 全媒体 E2EE、服务端语音转写 |

媒体走 MinIO 预签名直传，不经 WSS。

---

## 2. 账号与登录

页面：登录卡、确认登录卡、设置、左栏头像菜单。

| 功能 | 页面入口 | 后端 |
|---|---|---|
| uid / 邮箱 / 手机号 + 密码登录 | `#login-uid` `#login-pass` | `POST /v1/auth/login` |
| 空密码开发登录 | 密码可空 | `POST /v1/auth/dev-login` |
| 注册（可选邮箱、手机号） | `#register-btn` | `POST /v1/auth/register` |
| 刷新令牌 | 静默 | `POST /v1/auth/refresh` |
| 第三方登录演示 | 「第三方登录（演示）」 | `POST /v1/auth/oauth/demo` |
| GitHub OAuth | 「GitHub 登录」 | `GET /v1/auth/oauth/github` + callback |
| 二维码登录 | 登录页二维码 | `GET /v1/auth/qr.png`、`/qr/new`、`/qr/status`、`POST /qr/approve` |
| 已登录窗口确认扫码 | `#confirm-qr` | 同上 approve |
| 改昵称 / 换头像 | 点左栏 `#me` | `GET/POST /v1/me`，头像走媒体预签名 |
| 改密码 | 设置 → 当前/新密码 | `POST /v1/me`（`old_password` / `new_password`） |
| 名片二维码 | 资料卡 / 单聊侧栏 | `GET /v1/me/qr.png` |
| 登录设备列表、踢下线 | 侧栏「登录设备」 | `GET/DELETE /v1/devices` |
| 退出登录 | 左栏退出 | 清本地令牌，断 WSS |
| 连接状态 | `#conn-state` | WSS 鉴权 / 心跳 |

设置（`#settings-box`，同步 `GET/POST /v1/settings`，部分落 `localStorage surge:ui:{uid}`）：

- 暗色模式
- Enter 发送 / Ctrl+Enter 换行
- 通知声音、通知预览内容
- 草稿仅保存在本机
- 字体大小（小 / 标准 / 大）
- 气泡背景（默认 / 浅绿 / 浅灰 / 深色）
- 免打扰时段（开始 / 结束）

PWA：`/manifest.json`、`/sw.js`、`icon-192.png` / `icon-512.png`，可安装到主屏幕。

---

## 3. 会话列表

左栏「会话」+ 中栏 `#pane-chats`。

| 功能 | 说明 |
|---|---|
| 搜索 | 会话 / 联系人 / 聊天记录；`GET /v1/search` + 本地过滤 |
| 发起聊天 / 建群 | `+` 打开通讯录选择器：1 人开单聊，多人建群 |
| 全部标为已读 | `#mark-all-read-btn` |
| 置顶 / 免打扰 / 隐藏 / 标未读 / 删除聊天 | 会话行右键；删除 = 清空记录 + 隐藏 |
| 草稿预览 | 列表显示 `[草稿]` |
| 未读角标、@ 提示 | 未读数、有人 @ 你 |
| 文件传输助手 | peer uid `filehelper`，列表置顶 |
| 在线绿点 | 好友在线（presence） |
| 隐藏会话找回 | 列表底部入口 |
| 浏览器标题未读数 | 如 `(3) Surge IM` |
| 中栏宽度可拖 | `#mid-split` |

会话接口：`GET /v1/conversations`；免打扰 `POST /v1/mute`；置顶 `POST /v1/pin`；隐藏 `POST /v1/conversation-hide`；标未读 `POST /v1/mark-unread`（或 `MarkRead` 且 `convSeq=0`，当前会话用 `holdUnreadCid` 暂不自动已读）；草稿 `POST /v1/drafts`。

---

## 4. 通讯录

左栏「通讯录」+ `#pane-contacts`。

| 功能 | 说明 |
|---|---|
| 搜索 / 添加好友 | uid 搜索，发申请 |
| 新的朋友 | 申请列表、通过 / 拒绝；通过时可带回复 |
| 建群 | 群名 + 通讯录多选 + 群模式 |
| 字母分组 + 右侧 A–Z | `#letter-index` |
| 星标好友 | 隐藏标签 `__star__` |
| 删除好友 | 好友行操作 |
| 黑名单 | 列表、拉黑 / 解除 |
| 标签 | 侧栏勾选、新建标签、标签组加人 |

群模式（建群时单选，后端 `im-core/groupmode.go`）：

| 模式 | 行为 |
|---|---|
| 普通群 | 默认 |
| 验证群 | `JoinApproval`，入群需管理员同意 |
| 私密群 | 非群主不能拉人 |
| 广播群 | 默认全员禁言，仅群主/管理员可说 |
| 匿名群 | 成员身份对他人隐藏；不展示「正在输入」 |
| 阅后即焚群 | 消息带 ephemeral，打开后销毁 |

好友 / 申请 / 黑名单 / 标签：`/v1/friends`、`/v1/users`、`/v1/friend-requests`、`/v1/blocks`、`/v1/remark`、`/v1/friend-tags`。

通讯录选择器 `#pick-box`：搜索、单选/多选、排除已选、可选文件传输助手。用于发起聊天、建群、拉人、转发。

---

## 5. 聊天主界面

### 5.1 时间轴

- 按会话加载时间线 `GET /v1/timeline`，向上加载更早
- 按日分割、未读分割线、「有 N 条新消息」`#unread-jump`
- 回到底部 `#jump-bottom`
- 切会话先清空当前消息，合并时校验 `cid`，避免串会话
- 群公告置顶条、聊天消息置顶条（可关闭）
- 引用回复条、正在输入、上传进度
- 连接中输入区锁定，未选会话不可发

### 5.2 消息类型

| 类型 | Payload | 页面表现 |
|---|---|---|
| 文本 | `TEXT` | 气泡；自己 `#D6ECFF`，对方 `#F2F3F5` |
| 图片 | `IMAGE` | 缩略图、灯箱、原图开关 |
| 视频 | `VIDEO` | 封面 + 灯箱播放（封面截帧受 CORS 限制） |
| 文件 | `FILE` | 文件名 / 大小，点击下载 |
| 语音 | 音频 media | 波形条、倍速、上滑取消、最长 60s；有 transcript 可转文字 |
| 贴纸 | `sticker_id` | 表情面板增删 |
| 名片 | `CARD` | 点开资料 |
| 合并转发 | `MERGE` | 点开聊天记录详情 |
| 链接预览 | `link` | 标题 / 描述 / 图 |
| 系统 | `SYSTEM` | 入群、改名等灰字 |
| 撤回 | `RECALL` | 「撤回了一条消息」；2 分钟内自己的可「重新编辑」 |
| 阅后即焚 | `ephemeral` | 对方打开后数秒销毁 |

媒体上传：`POST /v1/media/presign` → 直传 MinIO → `POST /v1/media/complete`。链接预览 `GET /v1/link-preview`。贴纸 `GET/POST/DELETE /v1/stickers`（删除也可 `AddSticker(pack=__delete__)`）。阅后即焚消费 `POST /v1/ephemeral/consume`。

### 5.3 发送与交互

- 表情、附件（多文件）、截图（涂抹 / 箭头）、按住录音
- 粘贴图片、拖放文件
- 原图（不压缩）、阅后即焚开关
- 群 @：输入 `@` 出成员列表，气泡插昵称，`mention_uids` 仍是 uid
- 引用回复
- 正在输入：单聊始终；群聊除匿名群外显示谁在输入
- 已读：单聊已读勾；群已读人数，点开已读详情
- 悬停条（飞书风）：赞、回复、转发、复制、更多；peer 在右、自己在左
- 右键：多选、置顶消息、收藏、复制、复制图片、另存视频、撤回、删除（仅自己）、举报
- 多选条：全选、逐条转发、合并转发（可留言）、收藏、删除
- 查找聊天内容：全部 / 图片 / 文件 / 视频 / 日期 / 发送人，上下一条
- 灯箱：图片 + 视频，上一张 / 下一张 / 另存为
- Esc 关闭浮层

全员禁言：普通成员输入框禁用；群主永远可说；管理员不受 `MutedAll` 限制，个人 `Muted` 仍禁（`canSpeak`）。

### 5.4 单聊侧栏

备注、标签、图片与文件、查找聊天内容、免打扰、置顶、端到端加密（仅文本，展示指纹）、隐藏会话、清空记录、发送名片、登录设备、加入黑名单。

E2EE：`GET/POST /v1/e2ee/keys`。清空 `POST /v1/conversation-clear`。删除单条 `POST /v1/message-delete`。

### 5.5 群资料侧栏

- 搜索成员、成员头像条、点开成员资料卡（加好友、发消息、发名片、复制账号）
- 从通讯录拉人 / 输入账号添加
- 群聊名称、群公告、我在本群的昵称
- 进群需验证、群二维码邀请（复制链接）、群头像、复制群 ID
- 图片与文件、查找、免打扰、置顶、隐藏、清空
- 管理员：全员禁言、入群申请审批、设置管理员、转让群主、退群、解散（仅群主）

群接口：`/v1/groups`、`/v1/group`、`/v1/group-invite`、`/v1/group-kick`、`/v1/group-update`、`/v1/group-leave`、`/v1/group-dissolve`、`/v1/group-transfer`、`/v1/group-mute-all`、`/v1/group-member`、`/v1/group-join-requests`、`/v1/group-join`、`/v1/group-invite-link`、`/v1/group-invite.png`、`/v1/group-join-invite`。置顶消息 `/v1/pinned`。

### 5.6 媒体页

侧栏「图片与文件」：图片 / 视频 / 文件 / 链接四个 Tab。

---

## 6. 收藏

左栏「收藏」。搜索、转发、转到当前会话、删除。接口 `GET/POST/DELETE /v1/favorites`。

---

## 7. 其它页面能力

| 功能 | 说明 |
|---|---|
| 表情反应 | 悬停赞 / 反应选择器；`POST /v1/react` |
| 举报 | 右键；`POST /v1/report` |
| 草稿 | 切会话立即提交；单独 `@` 不当草稿显示；本机草稿不走服务端 |
| 多 Tab | 同 uid 多标签页共用一条 WSS |
| IndexedDB | 本地缓存时间线（`surge-p0`） |
| Toast / 通用弹窗 | 确认改名、公告、踢人等 |

---

## 8. 实时通道（WebSocket）

`GET /v1/ws`，帧定义见 `proto/im/v1/frame.proto`。一条 Envelope，`oneof body`：

| 帧 | 方向 | 作用 |
|---|---|---|
| `auth` / `auth_ok` | 双向 | 令牌鉴权，下发 `last_sync_seq` |
| `heartbeat` | 双向 | 保活 |
| `send` / `ack` | 双向 | 发消息、服务端确认（含 `client_msg_id` 幂等） |
| `push` | 下行 | 新消息 / 系统通知 |
| `sync` / `sync_resp` | 双向 | 断线按 seq 补齐 |
| `recall` / `recalled` | 双向 | 撤回 |
| `typing` | 双向 | 正在输入 |
| `read` | 双向 | 已读回执 |
| `error` | 下行 | 业务错误 |

Gateway 持连接；im-core 无连接。投递至少一次，客户端用 `client_msg_id` 去重。图片文件禁止走 WSS。

---

## 9. HTTP API

Gateway `gateway/http.go`。除标注外需 `Authorization: Bearer`。静态资源：`WEB_DIR` FileServer。`GET /healthz` 无鉴权。

### 9.1 认证

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/v1/auth/dev-login` | 开发登录 |
| POST | `/v1/auth/register` | 注册 |
| POST | `/v1/auth/login` | 密码登录 |
| POST | `/v1/auth/refresh` | 刷新令牌 |
| GET | `/v1/auth/qr/new` | 创建扫码会话 |
| GET | `/v1/auth/qr/status` | 扫码状态 |
| GET | `/v1/auth/qr.png` | 登录二维码图 |
| POST | `/v1/auth/qr/approve` | 确认扫码登录 |
| POST | `/v1/auth/oauth/demo` | OAuth 演示 |
| GET | `/v1/auth/oauth/github` | GitHub 授权 |
| GET | `/v1/auth/oauth/github/callback` | GitHub 回调 |

### 9.2 资料与设置

| 方法 | 路径 | 说明 |
|---|---|---|
| GET/POST | `/v1/me` | 资料；POST 可改昵称、头像、密码 |
| GET | `/v1/me/qr.png` | 名片二维码 |
| GET/POST | `/v1/settings` | 用户设置 |
| GET/DELETE | `/v1/devices` | 设备列表 / 踢下线 |
| GET/POST | `/v1/e2ee/keys` | 公钥 |

### 9.3 会话与消息

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/v1/conversations` | 会话列表 |
| GET | `/v1/timeline` | 会话时间线 |
| POST | `/v1/mute` | 免打扰 |
| POST | `/v1/pin` | 会话置顶 |
| GET | `/v1/presence` | 在线状态 |
| POST | `/v1/conversation-hide` | 隐藏会话 |
| POST | `/v1/mark-unread` | 标为未读 |
| GET | `/v1/read-state` | 已读状态 |
| GET | `/v1/search` | 全局搜索 |
| POST | `/v1/ephemeral/consume` | 阅后即焚已读销毁 |
| GET/POST/DELETE | `/v1/stickers` | 贴纸 |
| GET | `/v1/link-preview` | 链接卡片 |
| POST | `/v1/media/presign` | 预签名上传 |
| POST | `/v1/media/complete` | 上传完成 |
| POST | `/v1/message-delete` | 删除自己的消息 |
| POST | `/v1/conversation-clear` | 清空会话 |
| POST | `/v1/react` | 表情反应 |
| GET/POST/DELETE | `/v1/favorites` | 收藏 |
| POST | `/v1/drafts` | 草稿 |
| GET/POST | `/v1/pinned` | 聊天内置顶消息 |
| POST | `/v1/report` | 举报 |

### 9.4 好友

| 方法 | 路径 | 说明 |
|---|---|---|
| GET/POST/DELETE | `/v1/friends` | 列表 / 添加 / 删除 |
| GET | `/v1/users` | 搜人 |
| GET | `/v1/profiles` | 批量资料 |
| GET/POST | `/v1/friend-requests` | 申请列表 / 通过拒绝 |
| GET/POST/DELETE | `/v1/blocks` | 黑名单 |
| POST | `/v1/remark` | 备注 |
| GET/POST | `/v1/friend-tags` | 标签 |

### 9.5 群

| 方法 | 路径 | 说明 |
|---|---|---|
| GET/POST | `/v1/groups` | 我的群 / 建群 |
| GET | `/v1/group` | 群详情 |
| POST | `/v1/group-invite` | 拉人 |
| POST | `/v1/group-kick` | 踢人 |
| POST | `/v1/group-update` | 改名 / 公告 / 头像 / 验证开关等 |
| POST | `/v1/group-leave` | 退群 |
| POST | `/v1/group-dissolve` | 解散 |
| POST | `/v1/group-transfer` | 转让群主 |
| POST | `/v1/group-mute-all` | 全员禁言 |
| POST | `/v1/group-member` | 成员角色 / 群昵称 / 个人禁言 |
| GET/POST | `/v1/group-join-requests` | 入群申请 / 审批 |
| POST | `/v1/group-join` | 申请入群 |
| GET/POST | `/v1/group-invite-link` | 邀请链接 |
| GET | `/v1/group-invite.png` | 群二维码图 |
| POST | `/v1/group-join-invite` | 凭邀请入群 |

### 9.6 实时

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/v1/ws` | WebSocket 升级 |

Gateway 另有内存限流、CORS。发消息、已读、撤回、正在输入走 WSS，不走以上 REST。

---

## 10. im-core RPC

`proto/im/v1/core.proto` 服务 `IMCore`。Gateway 转 HTTP/WSS 到这些 RPC，Web 不直连 gRPC。

| RPC | 能力 |
|---|---|
| `Send` | 发消息，幂等 ACK，单聊/群扇出路由 |
| `Sync` / `Watermark` | inbox 按 seq 补齐 |
| `ListConversations` / `GetTimeline` | 会话列表、时间线 |
| `AddFriend` / `ListFriends` / `RemoveFriend` | 好友 |
| `LookupUser` / `SearchUsers` | 查人 / 搜人 |
| `Register` / `VerifyPassword` | 注册 / 验密 |
| `GetProfile` / `UpdateProfile` / `GetProfiles` | 资料 |
| `CreateGroup` / `InviteGroup` / `KickGroup` / `GetGroup` / `UpdateGroup` | 群 CRUD |
| `LeaveGroup` / `DissolveGroup` / `TransferOwner` | 退群 / 解散 / 转让 |
| `SetMember` | 管理员、群昵称、个人禁言 |
| `ListJoinRequests` / `RequestJoin` / `DecideJoin` | 入群申请 |
| `CreateGroupInvite` / `JoinByInvite` | 邀请链接 |
| `SetGroupMuteAll` | 全员禁言 |
| `Recall` | 撤回 |
| `MarkRead` / `GetReadState` | 已读 / 标未读（seq=0） |
| `FanoutTyping` | 正在输入扇出 |
| `SetMute` / `ListMutes` / `SetPin` | 免打扰、置顶 |
| `RequestFriend` / `AcceptFriend` / `DeclineFriend` / `ListFriendRequests` | 好友申请 |
| `BlockUser` / `UnblockUser` / `ListBlocks` | 黑名单 |
| `SetRemark` | 备注 |
| `HideConversation` | 隐藏会话 |
| `SearchMessages` | 搜聊天记录 |
| `SetFriendTags` / `ListFriendTags` | 标签 |
| `SetPublicKey` / `GetPublicKeys` | E2EE 公钥 |
| `ConsumeEphemeral` | 阅后即焚销毁 |
| `AddSticker` / `ListStickers` | 贴纸 |
| `DeleteMessage` / `ClearConversation` | 删消息 / 清空 |
| `ReactMessage` | 反应 |
| `AddFavorite` / `ListFavorites` / `DeleteFavorite` | 收藏 |
| `SetDraft` | 草稿 |
| `PinChatMessage` / `GetPinnedMessage` | 聊天内置顶 |
| `ReportMessage` | 举报 |
| `GetSettings` / `SetSettings` | 设置 |

存储实现：`im-core/memory.go`（测试）与 MySQL（`*_mysql.go`）。会话时间线正文只存一份；inbox 存指针。

---

## 11. 基础设施（Compose）

| 服务 | 端口 | 作用 |
|---|---|---|
| mysql | 3306 | 账号、会话、消息、好友、群 |
| redis | 6379 | 在线路由、扫码会话、网关连接索引 |
| minio | 9001（控制台 9002） | 图片 / 视频 / 文件对象 |
| im-core | 9000 | gRPC 业务 |
| gateway | 8080 | HTTP + WSS + 静态 `web/` |

`make up` = `docker compose up --build -d`。前端是 volume（`./web:/app/web`），改静态资源刷新即可（需 bump `index.html` 里 `app.css` / `app.js` 的 `?v=`）。后端改动要 `--build`。

---

## 12. 已知限制（已有功能的边界）

- 历史语音没有 transcript 时，浏览器无法从已上传文件补转文字（无服务端 STT）。
- 视频封面截帧受跨域限制，可能没有海报。
- E2EE 只覆盖文本，图片/文件/语音仍明文对象存储。
- 匿名群不展示「正在输入」。
- 文件传输助手是系统单聊，不是真实用户资料。
- P2 水平扩展、P3 RTC / 朋友圈 / 支付 / 小程序 / 视频号：明确不做。
