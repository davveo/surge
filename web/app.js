(() => {
  const $ = (id) => document.getElementById(id);
  function toggleHidden(id, hidden) {
    const n = $(id);
    if (n) n.classList.toggle("hidden", hidden);
  }
  function setSwitch(id, on) {
    const n = $(id);
    if (n) n.classList.toggle("on", !!on);
  }
  function setChatSide(open) {
    const side = $("chat-side");
    if (!side) return;
    if (!state.activeCid) open = false;
    side.classList.toggle("hidden", !open);
    const btn = $("chat-menu-btn");
    if (btn) btn.classList.toggle("on", !!open);
    const app = $("app");
    if (app) app.classList.toggle("side-open", !!open);
  }
  function syncSideSwitches() {
    setSwitch("mute-switch", isMuted(state.activeCid));
    setSwitch("pin-switch", isPinned(state.activeCid));
    setSwitch("e2ee-switch", state.e2eeOn);
    const f = state.friends.find((x) => friendUid(x) === state.activePeer) || {};
    if ($("side-remark-val")) $("side-remark-val").textContent = f.remark || "";
    if ($("side-tag-val")) $("side-tag-val").textContent = (f.tags || []).join("、");
    if ($("block-btn")) $("block-btn").textContent = isBlocked(state.activePeer) ? "移出黑名单" : "加入黑名单";
  }
  function renderSidePeer() {
    const el = $("side-peer-card");
    if (!el) return;
    if (!state.activePeer) {
      el.innerHTML = "";
      return;
    }
    const name = nickOf(state.activePeer) || state.activePeer;
    el.innerHTML = avatarHTML(avatarOf(state.activePeer), name, state.activePeer) +
      `<div class="side-peer-name">${escapeHtml(name)}</div>`;
  }
  function onClick(id, fn) {
    const n = $(id);
    if (n) n.onclick = fn;
  }
  const RECALL_MS = 2 * 60 * 1000;
  const state = {
    uid: "",
    token: "",
    tab: "chats",
    ws: null,
    reqId: 1,
    lastSyncSeq: 0,
    convs: [],
    friends: [],
    activePeer: "",
    activeCid: "",
    messages: [],
    outbox: [],
    hb: null,
    group: null,
    peerReadSeq: 0,
    lastTypingAt: 0,
    typingTimer: null,
    quote: null,
    qrTimer: null,
    pendingTicket: "",
    muted: {},
    pins: {},
    refresh: "",
    isLeader: false,
    tabCh: null,
    tabId: "",
    leaderAt: 0,
    wsElectTimer: null,
    online: {},
    requests: { incoming: [], outgoing: [] },
    blocks: [],
    hasMore: false,
    loadingMore: false,
    searchQ: "",
    readCursors: {},
    e2eeOn: false,
    presenceTimer: null,
    forwarding: null,
    profiles: {},
    me: null,
    tagGroups: [],
    selecting: false,
    selected: {},
    highlightId: "",
    addPeer: "",
  };

  const dbp = new Promise((resolve, reject) => {
    const req = indexedDB.open("surge-p0", 1);
    req.onupgradeneeded = () => req.result.createObjectStore("kv");
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });

  async function kvGet(key) {
    const db = await dbp;
    return new Promise((resolve, reject) => {
      const q = db.transaction("kv").objectStore("kv").get(key);
      q.onsuccess = () => resolve(q.result);
      q.onerror = () => reject(q.error);
    });
  }

  async function kvSet(key, val) {
    const db = await dbp;
    return new Promise((resolve, reject) => {
      const q = db.transaction("kv", "readwrite").objectStore("kv").put(val, key);
      q.onsuccess = () => resolve();
      q.onerror = () => reject(q.error);
    });
  }

  function api(path, opts = {}, retry = true) {
    const headers = Object.assign({ "Content-Type": "application/json" }, opts.headers || {});
    if (state.token) headers.Authorization = "Bearer " + state.token;
    return fetch(path, Object.assign({}, opts, { headers })).then(async (r) => {
      const text = await r.text();
      if (r.status === 401 && retry && state.refresh) {
        await refreshTokens();
        return api(path, opts, false);
      }
      if (r.status === 429) throw new Error("操作过于频繁，请稍后再试");
      if (!r.ok) throw new Error(friendlyHttp(r.status, text));
      return text ? JSON.parse(text) : {};
    });
  }

  async function refreshTokens() {
    const r = await fetch("/v1/auth/refresh", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ refresh_token: state.refresh, device_id: deviceId() }),
    });
    const text = await r.text();
    if (!r.ok) throw new Error(text || "refresh failed");
    const data = JSON.parse(text);
    state.token = data.access_token;
    if (data.refresh_token) state.refresh = data.refresh_token;
    sessionStorage.setItem("surge_token", state.token);
    if (state.refresh) sessionStorage.setItem("surge_refresh", state.refresh);
  }

  function uuid() {
    return crypto.randomUUID ? crypto.randomUUID() : String(Date.now()) + Math.random();
  }
  function deviceId() {
    let id = localStorage.getItem("surge:device");
    if (!id) {
      id = uuid();
      localStorage.setItem("surge:device", id);
    }
    return id;
  }

  function cidOf(a, b) {
    return a < b ? "p2p:" + a + ":" + b : "p2p:" + b + ":" + a;
  }

  function peerFromCid(cid, self) {
    const id = String(cid || "");
    if (!id.startsWith("p2p:") || !self) return "";
    const rest = id.slice(4);
    const i = rest.indexOf(":");
    if (i <= 0 || rest.indexOf(":", i + 1) >= 0) return "";
    const left = rest.slice(0, i);
    const right = rest.slice(i + 1);
    if (left === self) return right;
    if (right === self) return left;
    return "";
  }

  function isGroup(cid) {
    return String(cid || "").startsWith("grp:");
  }

  function field(obj, camel, snake) {
    if (!obj) return undefined;
    if (obj[camel] !== undefined) return obj[camel];
    if (snake && obj[snake] !== undefined) return obj[snake];
    if (camel) {
      const pascal = camel.charAt(0).toUpperCase() + camel.slice(1);
      if (obj[pascal] !== undefined) return obj[pascal];
    }
    return undefined;
  }

  function uidOf(obj) {
    if (!obj) return "";
    if (typeof obj === "string") return obj.trim();
    return String(obj.uid || obj.Uid || obj.UID || obj.fromUid || obj.from_uid || obj.FromUid || "").trim();
  }

  function senderOf(obj) {
    if (!obj) return "";
    return String(obj.fromUid || obj.from_uid || obj.FromUid || "").trim();
  }

  function friendlyHttp(status, text) {
    const t = String(text || "");
    if (status === 429 || /too many/i.test(t)) return "操作过于频繁，请稍后再试";
    if (/blocked/i.test(t)) return "已拉黑，无法发送";
    return t || "请求失败";
  }

  function friendlyRpc(raw) {
    const t = String(raw || "");
    if (/unsupported payload type/i.test(t)) return "消息格式异常，已停止重发";
    if (/empty text/i.test(t)) return "不能发送空消息";
    if (/add friend first/i.test(t)) return "请先加好友再发消息";
    if (/not friends/i.test(t)) return "请先加好友再发消息";
    if (/user blocked|blocked/i.test(t)) return "已拉黑，无法发送";
    if (/peer_uid does not match cid/i.test(t)) return "会话对象不匹配，请重新打开聊天后再发";
    return t.replace(/^rpc error: code = \w+ desc = (invalid argument: )?/i, "") || "发送失败";
  }

  function toast(msg) {
    const el = $("toast");
    if (!el) {
      dlgAlert(msg);
      return;
    }
    el.textContent = msg;
    el.classList.remove("hidden");
    clearTimeout(state.toastTimer);
    state.toastTimer = setTimeout(() => el.classList.add("hidden"), 2600);
  }

  const modalState = { resolve: null, mode: "alert" };
  function closeDialog(result) {
    const box = $("modal");
    if (box) box.classList.add("hidden");
    const fn = modalState.resolve;
    modalState.resolve = null;
    if (fn) fn(result);
  }
  function dlgOpen(opts) {
    return new Promise((resolve) => {
      if (modalState.resolve) closeDialog(modalState.mode === "confirm" ? false : null);
      modalState.resolve = resolve;
      modalState.mode = opts.mode || "alert";
      const title = $("modal-title");
      const body = $("modal-body");
      const input = $("modal-input");
      const cancel = $("modal-cancel");
      const ok = $("modal-ok");
      title.textContent = opts.title || (modalState.mode === "prompt" ? "请输入" : modalState.mode === "confirm" ? "确认" : "提示");
      body.textContent = opts.message || "";
      body.classList.toggle("hidden", !opts.message);
      if (modalState.mode === "prompt") {
        input.classList.remove("hidden");
        input.value = opts.value == null ? "" : String(opts.value);
        input.placeholder = opts.placeholder || "";
      } else {
        input.classList.add("hidden");
        input.value = "";
      }
      cancel.classList.toggle("hidden", modalState.mode === "alert");
      ok.textContent = opts.ok || (modalState.mode === "alert" ? "知道了" : "确定");
      ok.className = opts.danger ? "btn-danger" : "btn-primary";
      $("modal").classList.remove("hidden");
      setTimeout(() => {
        if (modalState.mode === "prompt") input.focus();
        else ok.focus();
      }, 30);
    });
  }
  function dlgAlert(message, title) {
    return dlgOpen({ mode: "alert", message, title });
  }
  function dlgConfirm(message, title, danger) {
    return dlgOpen({ mode: "confirm", message, title, danger, ok: danger ? "确认" : "确定" });
  }
  function dlgPrompt(message, value, title) {
    return dlgOpen({ mode: "prompt", message, value, title });
  }
  $("modal-ok").onclick = () => {
    if (modalState.mode === "confirm") closeDialog(true);
    else if (modalState.mode === "prompt") closeDialog($("modal-input").value);
    else closeDialog(true);
  };
  $("modal-cancel").onclick = () => closeDialog(modalState.mode === "confirm" ? false : null);
  $("modal").onclick = (e) => {
    if (e.target !== $("modal")) return;
    closeDialog(modalState.mode === "confirm" ? false : modalState.mode === "prompt" ? null : true);
  };
  $("modal-input").addEventListener("keydown", (e) => {
    if (e.key === "Enter") $("modal-ok").click();
    if (e.key === "Escape") $("modal-cancel").click();
  });
  document.addEventListener("keydown", (e) => {
    if (e.key !== "Escape") return;
    if ($("modal") && !$("modal").classList.contains("hidden")) {
      $("modal-cancel").click();
      if ($("modal-cancel").classList.contains("hidden")) $("modal-ok").click();
    }
  });

  function peerProfile(c) {
    return field(c, "peerProfile", "peer_profile") || {};
  }

  function convTitle(c) {
    if (!c) return "";
    if (c.kind === "group" || isGroup(c.cid)) return c.title || "群聊";
    const p = peerProfile(c);
    return field(p, "displayName", "display_name") || field(c, "peerUid", "peer_uid") || c.cid;
  }

  function convAvatar(c) {
    if (!c) return "";
    const p = peerProfile(c);
    return field(p, "avatarUrl", "avatar_url") || "";
  }

  function friendUid(f) {
    return uidOf(f);
  }

  function friendName(f) {
    const uid = friendUid(f);
    if (typeof f === "string") return uid;
    return field(f, "displayName", "display_name") || f.remark || uid;
  }

  function friendAvatar(f) {
    if (typeof f === "string") return "";
    return field(f, "avatarUrl", "avatar_url") || "";
  }

  function avatarColor(uid) {
    const colors = ["#07c160", "#3c8cff", "#fa9d3b", "#f56c6c", "#9b59b6", "#1abc9c", "#5c6bc0"];
    let h = 0;
    const s = String(uid || "");
    for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0;
    return colors[h % colors.length];
  }

  function avatarHTML(url, name, uid, extra) {
    const letter = String(name || uid || "?").slice(0, 1).toUpperCase();
    const face = url
      ? `<img class="avatar" src="${escapeHtml(url)}" alt="" />`
      : `<span class="avatar letter" style="background:${avatarColor(uid || name)}">${escapeHtml(letter)}</span>`;
    const on = uid && state.online[uid] ? " on" : "";
    return `<div class="avatar-wrap">${face}<span class="presence${on}"></span>${extra || ""}</div>`;
  }

  function formatConvTime(ms) {
    const t = Number(ms || 0);
    if (!t) return "";
    const d = new Date(t);
    const now = new Date();
    const pad = (n) => String(n).padStart(2, "0");
    if (d.toDateString() === now.toDateString()) return pad(d.getHours()) + ":" + pad(d.getMinutes());
    const yest = new Date(now);
    yest.setDate(now.getDate() - 1);
    if (d.toDateString() === yest.toDateString()) return "昨天";
    if (now - d < 7 * 86400000) return ["周日", "周一", "周二", "周三", "周四", "周五", "周六"][d.getDay()];
    return pad(d.getMonth() + 1) + "/" + pad(d.getDate());
  }

  function rememberProfiles(users) {
    (users || []).forEach((u) => {
      const uid = uidOf(u);
      if (!uid) return;
      const prev = state.profiles[uid] || {};
      state.profiles[uid] = Object.assign({}, prev, u, { uid });
      if (uid === state.uid) state.me = state.profiles[uid];
    });
  }

  function profileOf(uid) {
    if (!uid) return {};
    if (state.profiles[uid]) return state.profiles[uid];
    if (uid === state.uid && state.me) return state.me;
    const f = state.friends.find((x) => friendUid(x) === uid);
    if (f && typeof f === "object") return f;
    const conv = state.convs.find((c) => field(c, "peerUid", "peer_uid") === uid);
    if (conv) return peerProfile(conv);
    return {};
  }

  function nickOf(uid) {
    if (!uid) return "";
    if (uid !== state.uid) {
      const f = state.friends.find((x) => friendUid(x) === uid);
      if (f && f.remark) return f.remark;
    }
    const p = profileOf(uid);
    return field(p, "displayName", "display_name") || uid;
  }

  function memberNick(uid) {
    const m = ((state.group && state.group.members) || []).find((x) => uidOf(x) === uid);
    return (m && (m.nickname || m.Nickname)) || nickOf(uid) || uid;
  }

  function isGroupManager() {
    const g = state.group;
    if (!g) return false;
    if ((g.ownerUid || g.owner_uid) === state.uid) return true;
    const me = (g.members || []).find((m) => uidOf(m) === state.uid);
    return !!(me && me.role === "admin");
  }

  function avatarOf(uid) {
    return field(profileOf(uid), "avatarUrl", "avatar_url") || "";
  }

  function showMe() {
    const el = $("me");
    if (!el) return;
    const name = nickOf(state.uid) || state.uid;
    el.title = name;
    el.innerHTML = avatarHTML(avatarOf(state.uid), name, state.uid);
  }

  async function loadMe() {
    try {
      const me = await api("/v1/me");
      if (me && uidOf(me)) {
        rememberProfiles([me]);
      }
      showMe();
    } catch (_) {
      showMe();
    }
  }

  async function ensureProfiles(uids) {
    const missing = [...new Set((uids || []).filter(Boolean))].filter((u) => !state.profiles[u]);
    if (!missing.length) return;
    try {
      const pr = await api("/v1/profiles?uids=" + encodeURIComponent(missing.join(",")));
      rememberProfiles(pr.users);
      showMe();
    } catch (_) {}
  }

  async function hydrateMsgProfiles() {
    const uids = state.messages.map((m) => senderOf(m));
    uids.push(state.uid, state.activePeer);
    if (state.group) (state.group.members || []).forEach((m) => uids.push(uidOf(m)));
    await ensureProfiles(uids);
  }

  function isBlocked(uid) {
    return !!uid && state.blocks.indexOf(uid) >= 0;
  }

  function draftKey(cid) {
    return "surge:draft:" + state.uid + ":" + (cid || state.activeCid);
  }

  function saveDraft() {
    if (!state.uid || !state.activeCid) return;
    localStorage.setItem(draftKey(), $("draft").value);
  }

  function loadDraft(cid) {
    $("draft").value = localStorage.getItem(draftKey(cid)) || "";
    fitDraft();
  }

  function setConn(text, on) {
    const el = $("conn-state");
    el.textContent = text;
    el.classList.toggle("text-wechat", !!on);
    el.classList.toggle("text-zinc-500", !on);
  }

  function setComposerEnabled(on) {
    $("draft").disabled = !on;
    $("send-btn").disabled = !on;
    $("attach-btn").disabled = !on;
    if ($("emoji-btn")) $("emoji-btn").disabled = !on;
    if ($("shot-btn")) $("shot-btn").disabled = !on;
    if ($("rec-btn")) $("rec-btn").disabled = !on;
    if (on) {
      fitDraft();
      syncBurnUI();
    }
  }

  function isEphemeral(m) {
    const p = m && m.payload;
    return !!(p && (p.ephemeral || p.Ephemeral));
  }

  function syncBurnUI() {
    const box = $("burn-toggle");
    const lab = $("burn-label");
    const on = !!(box && box.checked);
    if (lab) lab.classList.toggle("burn-on", on);
    const draft = $("draft");
    if (draft) draft.placeholder = on ? "阅后即焚消息…" : "";
  }

  function fitDraft() {
    const el = $("draft");
    if (!el) return;
    el.style.height = "";
  }

  function renderLists() {
    const convEl = $("conv-list");
    const sorted = state.convs.slice().sort((a, b) => {
      const pa = isPinned(a.cid) ? 1 : 0;
      const pb = isPinned(b.cid) ? 1 : 0;
      if (pa !== pb) return pb - pa;
      return Number(b.updatedAtMs || b.updated_at_ms || 0) - Number(a.updatedAtMs || a.updated_at_ms || 0);
    });
    convEl.innerHTML = sorted
      .map((c) => {
        const active = c.cid === state.activeCid ? " active" : "";
        const pinCls = isPinned(c.cid) ? " pinned" : "";
        const muted = isMuted(c.cid);
        const unreadN = Number(c.unread || 0);
        const badge =
          unreadN > 0
            ? `<span class="badge${muted ? " muted-badge" : ""}">${unreadN > 99 ? "99+" : unreadN}</span>`
            : "";
        const peer = field(c, "peerUid", "peer_uid") || "";
        const title = convTitle(c);
        const uid = isGroup(c.cid) ? "" : peer;
        const time = formatConvTime(c.updatedAtMs || c.updated_at_ms);
        return `<div class="row${active}${pinCls}${muted ? " muted" : ""}" data-peer="${peer}" data-cid="${c.cid}">
          ${avatarHTML(convAvatar(c), title, uid, badge)}
          <div class="row-main"><div class="row-head"><div class="row-title">${escapeHtml(title)}</div><div class="row-time">${escapeHtml(time)}</div></div><div class="row-sub">${escapeHtml(c.lastText || c.last_text || "")}</div></div>
        </div>`;
      })
      .join("");
    convEl.querySelectorAll(".row").forEach((row) => {
      row.onclick = () => openChat(row.dataset.peer, row.dataset.cid);
    });

    const fEl = $("friend-list");
    fEl.innerHTML = state.friends
      .map((f) => {
        const uid = friendUid(f);
        const name = friendName(f);
        const active = uid === state.activePeer ? " active" : "";
        return `<div class="row${active}" data-peer="${uid}">
          ${avatarHTML(friendAvatar(f), name, uid)}
          <div class="row-main"><div class="row-title">${escapeHtml(name)}</div><div class="row-sub">${escapeHtml(uid)}${(f.tags || []).length ? " · " + escapeHtml((f.tags || []).join(" / ")) : ""}</div></div>
          <div class="row-actions"><button type="button" class="danger" data-act="del">删除</button></div>
        </div>`;
      })
      .join("");
    fEl.querySelectorAll(".row").forEach((row) => {
      row.onclick = () => openChat(row.dataset.peer, cidOf(state.uid, row.dataset.peer));
      const del = row.querySelector("[data-act=del]");
      if (del) {
        del.onclick = (e) => {
          e.stopPropagation();
          removeFriend(row.dataset.peer);
        };
      }
    });
    renderRequests();
    renderBlocks();
    renderTagGroups();
  }

  function renderTagGroups() {
    const el = $("tag-groups");
    if (!el) return;
    const groups = state.tagGroups || [];
    if (!groups.length) {
      el.innerHTML = "";
      return;
    }
    el.innerHTML = groups
      .map((g) => {
        const uids = g.uids || [];
        const rows = uids
          .map((uid) => {
            const f = state.friends.find((x) => friendUid(x) === uid);
            const name = f ? friendName(f) : nickOf(uid) || uid;
            return `<div class="row" data-peer="${escapeHtml(uid)}">${avatarHTML(avatarOf(uid), name, uid)}<div class="row-main"><div class="row-title">${escapeHtml(name)}</div></div></div>`;
          })
          .join("");
        return `<div class="tag-head">${escapeHtml(g.name || "")} · ${uids.length}</div>${rows}`;
      })
      .join("");
    el.querySelectorAll(".row[data-peer]").forEach((row) => {
      row.onclick = () => openChat(row.dataset.peer, cidOf(state.uid, row.dataset.peer));
    });
  }

  function canRecall(m) {
    const mine = senderOf(m) === state.uid;
    const recalled = m.recalled || (m.payload && (m.payload.type === "RECALL" || m.payload.type === 2));
    const created = Number(field(m, "createdAtMs", "created_at_ms") || 0);
    const id = field(m, "msgId", "msg_id");
    return mine && !recalled && id && created && Date.now() - created < RECALL_MS;
  }

  function pinKey() {
    return "surge:pin:" + state.uid;
  }

  function isPinned(cid) {
    return !!state.pins[cid];
  }

  function loadPins() {
    state.pins = {};
  }

  function savePins() {}

  function muteKey() {
    return "surge:mute:" + state.uid;
  }

  function isMuted(cid) {
    return !!state.muted[cid];
  }

  function loadMuted() {
    try {
      state.muted = JSON.parse(localStorage.getItem(muteKey()) || "{}");
    } catch (_) {
      state.muted = {};
    }
  }

  function payloadType(p) {
    if (!p || p.type === undefined || p.type === null) return "";
    return String(p.type);
  }

  const PAYLOAD_TYPE_NUM = {
    TYPE_UNSPECIFIED: 0,
    TEXT: 1,
    RECALL: 2,
    SYSTEM: 3,
    IMAGE: 4,
    FILE: 5,
    STICKER: 4,
    EMOJI: 4,
    AUDIO: 5,
    VOICE: 5,
  };

  const PAYLOAD_TYPE_NAME = { 1: "TEXT", 3: "SYSTEM", 4: "IMAGE", 5: "FILE" };

  function encodePayloadType(t) {
    if (typeof t === "number" && isFinite(t)) return t;
    const s = String(t || "").trim();
    if (!s) return 0;
    if (/^\d+$/.test(s)) return Number(s);
    const n = PAYLOAD_TYPE_NUM[s.toUpperCase()];
    return n === undefined ? 0 : n;
  }

  function wireMedia(media) {
    if (!media || typeof media !== "object") return null;
    const objectKey = media.objectKey || media.object_key || "";
    if (!objectKey) return null;
    const out = { objectKey };
    const thumbKey = media.thumbKey || media.thumb_key;
    if (thumbKey) out.thumbKey = thumbKey;
    const contentType = media.contentType || media.content_type;
    if (contentType) out.contentType = contentType;
    if (media.filename) out.filename = media.filename;
    if (media.size) out.size = media.size;
    if (media.width) out.width = media.width;
    if (media.height) out.height = media.height;
    if (media.url) out.url = media.url;
    const thumbUrl = media.thumbUrl || media.thumb_url;
    if (thumbUrl) out.thumbUrl = thumbUrl;
    return out;
  }

  // Canonical protojson Payload: numeric Type (1=TEXT…) and camelCase fields only.
  // String "1" / unknown names like STICKER are dropped by protojson.DiscardUnknown.
  function wirePayload(p) {
    if (!p || typeof p !== "object") {
      return { type: "TEXT", text: typeof p === "string" ? p : "" };
    }
    let type = encodePayloadType(p.type);
    const media = wireMedia(p.media);
    const stickerId = p.stickerId || p.sticker_id || "";
    if (!type) {
      if (media) {
        const ct = String(media.contentType || "");
        if (stickerId || ct.indexOf("image/") === 0) type = 4;
        else type = 5;
      } else {
        type = 1;
      }
    }
    if (type === 2) type = 1;
    const out = { type: PAYLOAD_TYPE_NAME[type] || "TEXT" };
    if (p.text) out.text = p.text;
    if (media) out.media = media;
    const link = p.link;
    if (link && (link.url || link.URL)) {
      out.link = {
        url: link.url || link.URL,
        title: link.title || "",
        description: link.description || "",
        image: link.image || "",
      };
    }
    const mentions = p.mentionUids || p.mention_uids;
    if (mentions && mentions.length) out.mentionUids = mentions;
    const quoteText = p.quoteText || p.quote_text;
    if (quoteText) out.quoteText = quoteText;
    if (p.ephemeral || p.Ephemeral) out.ephemeral = true;
    if (p.e2ee || p.e2Ee) out.e2ee = true;
    if (stickerId) out.stickerId = stickerId;
    return out;
  }

  function isSystemMsg(m) {
    const t = payloadType(m.payload);
    return t === "SYSTEM" || t === "3";
  }

  function isImageMsg(m) {
    const t = payloadType(m.payload);
    return t === "IMAGE" || t === "4";
  }

  function isFileMsg(m) {
    const t = payloadType(m.payload);
    return t === "FILE" || t === "5";
  }

  function isAudioMsg(m) {
    if (!isFileMsg(m)) return false;
    const media = mediaOf(m.payload);
    const ct = String(media.contentType || media.content_type || "");
    const name = String(media.filename || "");
    return ct.indexOf("audio/") === 0 || /\.(m4a|mp3|wav|ogg|aac|webm)$/i.test(name);
  }

  function linkify(s) {
    const html = escapeHtml(s).replace(/(https?:\/\/[^\s<]+)/g, '<a href="$1" target="_blank" rel="noopener">$1</a>');
    return html.replace(/@所有人/g, '<span class="mention">@所有人</span>').replace(/@([A-Za-z0-9._@+-]{1,64})/g, '<span class="mention">@$1</span>');
  }

  function quoteBlock(m) {
    const qtext = (m.payload && (m.payload.quoteText || m.payload.quote_text)) || "";
    const qid = field(m, "quoteMsgId", "quote_msg_id");
    if (!qtext && !qid) return "";
    if (!qtext && !qid) return "";
    return `<div class="quote-card" data-qid="${escapeHtml(qid || "")}">${escapeHtml(qtext || "引用消息")}</div>`;
  }

  function linkCard(m) {
    const link = (m.payload && m.payload.link) || null;
    if (!link || !(link.url || link.URL)) return "";
    const url = link.url || link.URL;
    const title = link.title || url;
    const desc = link.description || "";
    const img = link.image || "";
    return `<a class="link-card" href="${escapeHtml(url)}" target="_blank" rel="noopener">${
      img ? `<img src="${escapeHtml(img)}" alt="" />` : ""
    }<div class="t">${escapeHtml(title)}</div>${desc ? `<div class="d">${escapeHtml(desc)}</div>` : ""}</a>`;
  }

  function mediaOf(p) {
    return (p && p.media) || {};
  }

  function mediaURL(m) {
    const media = mediaOf(m.payload);
    return media.url || media.thumbUrl || media.thumb_url || "";
  }

  function renderBody(m) {
    if (isImageMsg(m)) {
      const media = mediaOf(m.payload);
      const src = media.thumbUrl || media.thumb_url || media.url || "";
      const href = media.url || src;
      const cap = m.payload && m.payload.text ? `<div>${linkify(m.payload.text)}</div>` : "";
      if (!src) return escapeHtml("[图片]");
      return `<img class="thumb" data-full="${escapeHtml(href)}" src="${escapeHtml(src)}" alt="" />${cap}`;
    }
    if (isAudioMsg(m)) {
      const media = mediaOf(m.payload);
      const href = media.url || "";
      const name = media.filename || "语音";
      if (!href) return escapeHtml("[" + name + "]");
      return `<div>${escapeHtml(name)}</div><audio controls preload="none" src="${escapeHtml(href)}"></audio>`;
    }
    if (isFileMsg(m)) {
      const media = mediaOf(m.payload);
      const name = media.filename || "文件";
      const href = media.url || "#";
      return `<a class="file-link" href="${escapeHtml(href)}" target="_blank">${escapeHtml(name)}</a>`;
    }
    const text = (m.payload && m.payload.text) || m.text || "";
    return linkify(text);
  }

  function groupReadCount(seq) {
    let n = 0;
    const cursors = state.readCursors || {};
    Object.keys(cursors).forEach((uid) => {
      if (uid === state.uid) return;
      if (Number(cursors[uid]) >= seq) n++;
    });
    return n;
  }

  function lastMineSeq() {
    let seq = 0;
    for (const m of state.messages) {
      if (senderOf(m) !== state.uid) continue;
      const s = Number(field(m, "convSeq", "conv_seq") || 0);
      if (s > seq) seq = s;
    }
    return seq;
  }

  function renderMsgs(opts) {
    const box = $("msgs");
    const stick = !opts || opts.stick !== false;
    const prevH = box.scrollHeight;
    const prevTop = box.scrollTop;
    const lastMine = lastMineSeq();
    const group = isGroup(state.activeCid);
    box.innerHTML = state.messages
      .map((m) => {
        const mine = senderOf(m) === state.uid;
        const recalled = m.recalled || (m.payload && (m.payload.type === "RECALL" || m.payload.type === 2));
        const burned = recalled && ((m.payload && m.payload.text) === "已销毁");
        if ((isSystemMsg(m) && !recalled) || recalled) {
          const sysText = recalled
            ? burned
              ? "已销毁"
              : "已撤回一条消息"
            : (m.payload && m.payload.text) || "";
          return `<div class="msg-row system"><span class="sys-notice">${escapeHtml(sysText)}</span></div>`;
        }
        const st = m.status ? " " + m.status : "";
        const recCls = recalled ? " recalled" : "";
        const id = field(m, "msgId", "msg_id") || "";
        const from = senderOf(m);
        const seq = Number(field(m, "convSeq", "conv_seq") || 0);
        const nick = memberNick(from) || from;
        const who = `<div class="msg-nick" title="${escapeHtml(from)}">${escapeHtml(nick)}${
          group && from && nick !== from ? `<span class="msg-uid">${escapeHtml(from)}</span>` : ""
        }</div>`;
        const face = avatarHTML(avatarOf(from), nick, from);
        const quote = quoteBlock(m);
        const eph = !recalled && isEphemeral(m);
        const burnHint = eph
          ? `<div class="burn-tag">${
              mine
                ? "阅后即焚 · 对方查看后销毁"
                : m._burnLeft
                  ? "阅后即焚 · " + m._burnLeft + "s 后销毁"
                  : "阅后即焚"
            }</div>`
          : "";
        const body = recalled ? (burned ? "已销毁" : "已撤回一条消息") : renderBody(m) + (recalled ? "" : linkCard(m));
        const hl = state.highlightId && id && state.highlightId === id ? " hl" : "";
        const failDot = m.status === "fail" ? `<button type="button" class="fail-dot" data-cid="${escapeHtml(m.clientMsgId || "")}">!</button>` : "";
        const check = state.selecting && id
          ? `<input type="checkbox" class="msg-check" data-id="${id}" ${state.selected[id] ? "checked" : ""} />`
          : "";
        const read =
          mine &&
          !group &&
          seq &&
          seq === lastMine &&
          state.peerReadSeq >= seq
            ? `<div class="read-mark">已读</div>`
            : "";
        const gRead =
          mine &&
          group &&
          !recalled &&
          seq
            ? (function () {
                const n = groupReadCount(seq);
                return n > 0 ? `<div class="read-mark" data-seq="${seq}">${n} 人已读</div>` : "";
              })()
            : "";
        return `<div class="msg-row${mine ? " me" : " peer"}${group ? " grp" : ""}">${check}${mine ? "" : face}<div class="msg-col">${who}<div class="bubble${mine ? " me" : " peer"}${st}${recCls}${eph ? " burn" : ""}${hl}" data-id="${id}" data-seq="${seq}">${failDot}${quote}${recalled ? escapeHtml(body) : body}${burnHint}${read}${gRead}</div></div>${mine ? face : ""}</div>`;
      })
      .join("");
    box.querySelectorAll("img.thumb").forEach((img) => {
      img.onclick = (e) => {
        e.stopPropagation();
        openLightbox(img.dataset.full || img.src);
      };
    });
    box.querySelectorAll(".quote-card").forEach((el) => {
      el.onclick = (e) => {
        e.stopPropagation();
        const id = el.dataset.qid;
        if (id) jumpToMessage(state.activeCid, id, 0, state.activePeer);
      };
    });
    box.querySelectorAll(".fail-dot").forEach((btn) => {
      btn.onclick = (e) => {
        e.stopPropagation();
        retryOrDrop(btn.dataset.cid);
      };
    });
    box.querySelectorAll(".read-mark[data-seq]").forEach((el) => {
      el.onclick = (e) => {
        e.stopPropagation();
        showReaders(Number(el.dataset.seq));
      };
    });
    box.querySelectorAll(".msg-check").forEach((el) => {
      el.onclick = (e) => e.stopPropagation();
      el.onchange = () => {
        if (el.checked) state.selected[el.dataset.id] = true;
        else delete state.selected[el.dataset.id];
        if ($("select-count")) $("select-count").textContent = "已选 " + Object.keys(state.selected).length;
      };
    });
    box.querySelectorAll(".bubble:not(.system)").forEach((el) => {
      el.ondblclick = () => quoteMsg(el.dataset.id);
      el.oncontextmenu = (e) => {
        e.preventDefault();
        showMsgMenu(e.clientX, e.clientY, el.dataset.id);
      };
    });
    if (stick) box.scrollTop = box.scrollHeight;
    else box.scrollTop = box.scrollHeight - prevH + prevTop;
  }

  function quoteMsg(id) {
    if (!id) return;
    const m = state.messages.find((x) => field(x, "msgId", "msg_id") === id);
    const preview = ((m && m.payload && m.payload.text) || "消息").slice(0, 80);
    state.quote = { id, preview };
    $("quote-text").textContent = "引用：" + preview;
    $("quote-bar").classList.remove("hidden");
  }

  function hideMsgMenu() {
    const el = $("msg-menu");
    if (el) el.classList.add("hidden");
  }

  function showMsgMenu(x, y, msgId) {
    const m = state.messages.find((x) => field(x, "msgId", "msg_id") === msgId);
    if (!m) return;
    const recalled = m.recalled;
    const items = [];
    const text = (m.payload && m.payload.text) || "";
    if (text) items.push(["复制", () => navigator.clipboard.writeText(text).then(() => toast("已复制")).catch(() => {})]);
    if (!recalled) items.push(["引用", () => quoteMsg(msgId)]);
    if (!recalled) items.push(["转发", () => openForward(msgId)]);
    if (!recalled) items.push(["多选", () => enterSelect(msgId)]);
    if (canRecall(m)) items.push(["撤回", () => recall(msgId)]);
    items.push(["删除", () => deleteForMe(msgId)]);
    const menu = $("msg-menu");
    if (!menu) return;
    menu.innerHTML = items.map((it, i) => `<button type="button" data-i="${i}">${it[0]}</button>`).join("");
    menu.querySelectorAll("button").forEach((btn) => {
      btn.onclick = () => {
        hideMsgMenu();
        items[Number(btn.dataset.i)][1]();
      };
    });
    menu.classList.remove("hidden");
    const w = menu.offsetWidth || 132;
    const h = menu.offsetHeight || 120;
    menu.style.left = Math.min(x, window.innerWidth - w - 8) + "px";
    menu.style.top = Math.min(y, window.innerHeight - h - 8) + "px";
  }

  function enterSelect(msgId) {
    state.selecting = true;
    state.selected = {};
    if (msgId) state.selected[msgId] = true;
    toggleHidden("select-bar", false);
    if ($("select-count")) $("select-count").textContent = "已选 " + Object.keys(state.selected).length;
    renderMsgs({ stick: false });
  }

  function exitSelect() {
    state.selecting = false;
    state.selected = {};
    toggleHidden("select-bar", true);
    renderMsgs({ stick: false });
  }

  async function deleteForMe(msgId) {
    if (!msgId || !state.activeCid) return;
    try {
      await api("/v1/message-delete", { method: "POST", body: JSON.stringify({ cid: state.activeCid, msg_id: msgId }) });
      state.messages = state.messages.filter((m) => field(m, "msgId", "msg_id") !== msgId);
      renderMsgs({ stick: false });
    } catch (err) {
      dlgAlert(err.message);
    }
  }

  async function retryOrDrop(clientMsgId) {
    const item = state.outbox.find((m) => m.clientMsgId === clientMsgId);
    if (!item) return;
    if (!(await dlgConfirm("重新发送这条消息？", "发送失败"))) return;
    item.status = "pending";
    item.dead = false;
    const msg = state.messages.find((m) => m.clientMsgId === clientMsgId);
    if (msg) msg.status = "pending";
    renderMsgs({ stick: false });
    try {
      await flushOutbox();
    } catch (err) {
      dlgAlert(err.message);
    }
  }

  function highlightMsg(msgId) {
    state.highlightId = msgId;
    renderMsgs({ stick: false });
    const el = document.querySelector(`.bubble[data-id="${CSS.escape ? CSS.escape(msgId) : msgId}"]`);
    if (el) el.scrollIntoView({ block: "center" });
    setTimeout(() => {
      if (state.highlightId === msgId) {
        state.highlightId = "";
        renderMsgs({ stick: false });
      }
    }, 1600);
  }

  async function jumpToMessage(cid, msgId, convSeq, peer) {
    if (!cid) return;
    if (state.activeCid !== cid) await openChat(peer || "", cid);
    convSeq = Number(convSeq || 0);
    const has = () => state.messages.some((m) => field(m, "msgId", "msg_id") === msgId);
    let guard = 0;
    while (!has() && state.hasMore && guard++ < 24) {
      await loadOlder();
    }
    if (!has() && convSeq) {
      try {
        const after = convSeq > 1 ? convSeq - 1 : 0;
        const data = await api("/v1/timeline?cid=" + encodeURIComponent(cid) + "&after=" + after + "&limit=40");
        const extra = data.messages || [];
        extra.forEach((m) => {
          const id = field(m, "msgId", "msg_id");
          if (id && !state.messages.some((x) => field(x, "msgId", "msg_id") === id)) state.messages.push(m);
        });
        state.messages.sort((a, b) => Number(field(a, "convSeq", "conv_seq") || 0) - Number(field(b, "convSeq", "conv_seq") || 0));
      } catch (_) {}
    }
    if (has()) highlightMsg(msgId);
    else toast("找不到这条消息");
  }

  async function showReaders(seq) {
    try {
      const data = await api("/v1/read-state?cid=" + encodeURIComponent(state.activeCid) + "&seq=" + seq);
      const uids = data.readerUids || data.reader_uids || [];
      const box = $("readers-list");
      box.innerHTML = uids.length
        ? uids.map((u) => `<div class="row">${avatarHTML(avatarOf(u), memberNick(u), u)}<div class="row-main"><div class="row-title">${escapeHtml(memberNick(u))}</div></div></div>`).join("")
        : `<div class="row-sub">暂无已读</div>`;
      $("readers-box").classList.remove("hidden");
    } catch (err) {
      toast(err.message);
    }
  }

  function escapeHtml(s) {
    return String(s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;");
  }

  async function loadConvs() {
    const data = await api("/v1/conversations");
    state.convs = data.conversations || [];
    state.muted = {};
    state.pins = {};
    state.convs.forEach((c) => {
      if (c.muted) state.muted[c.cid] = true;
      if (c.pinned) state.pins[c.cid] = true;
    });
    renderLists();
    await catchUpActiveChat();
  }

  async function catchUpActiveChat() {
    if (!state.activeCid || state.catchingUp) return;
    const conv = state.convs.find((c) => c.cid === state.activeCid);
    if (!conv) return;
    const lastId = String(field(conv, "lastMsgId", "last_msg_id") || "");
    const lastSeq = Number(conv.lastConvSeq || conv.last_conv_seq || 0);
    let maxSeq = 0;
    let hasLast = !lastId;
    state.messages.forEach((m) => {
      const s = Number(field(m, "convSeq", "conv_seq") || 0);
      if (s > maxSeq) maxSeq = s;
      if (lastId && String(field(m, "msgId", "msg_id") || "") === lastId) hasLast = true;
    });
    if (hasLast && (!lastSeq || lastSeq <= maxSeq)) return;
    state.catchingUp = true;
    try {
      await reloadTimeline();
      markRead();
    } finally {
      state.catchingUp = false;
    }
  }

  async function loadFriends() {
    const data = await api("/v1/friends");
    state.friends = data.friends || [];
    rememberProfiles(state.friends.filter((f) => typeof f === "object"));
    await loadTagGroups();
    renderLists();
  }

  async function loadTagGroups() {
    try {
      const data = await api("/v1/friend-tags");
      state.tagGroups = data.tags || [];
    } catch (_) {
      state.tagGroups = [];
    }
  }

  async function loadRequests() {
    try {
      const data = await api("/v1/friend-requests");
      state.requests = {
        incoming: data.incoming || [],
        outgoing: data.outgoing || [],
      };
    } catch (_) {
      state.requests = { incoming: [], outgoing: [] };
    }
    renderRequests();
  }

  async function loadBlocks() {
    try {
      const data = await api("/v1/blocks");
      state.blocks = data.uids || [];
    } catch (_) {
      state.blocks = [];
    }
    renderBlocks();
    lockComposer();
  }

  function renderRequests() {
    const el = $("req-list");
    if (!el) return;
    const incoming = state.requests.incoming || [];
    const outgoing = state.requests.outgoing || [];
    if (!incoming.length && !outgoing.length) {
      el.innerHTML = `<div class="row"><div class="row-sub">暂无申请</div></div>`;
      return;
    }
    el.innerHTML =
      incoming
        .map((r) => {
          const from = field(r, "fromUid", "from_uid");
          return `<div class="row" data-uid="${from}">
            ${avatarHTML("", from, from)}
            <div class="row-main"><div class="row-title">${escapeHtml(from)}</div><div class="row-sub">请求加为好友</div></div>
            <div class="row-actions">
              <button type="button" data-act="accept">通过</button>
              <button type="button" class="danger" data-act="decline">拒绝</button>
            </div>
          </div>`;
        })
        .join("") +
      outgoing
        .map((r) => {
          const to = field(r, "toUid", "to_uid");
          return `<div class="row" data-uid="${to}">
            ${avatarHTML("", to, to)}
            <div class="row-main"><div class="row-title">${escapeHtml(to)}</div><div class="row-sub">等待验证</div></div>
          </div>`;
        })
        .join("");
    el.querySelectorAll("[data-act]").forEach((btn) => {
      btn.onclick = (e) => {
        e.stopPropagation();
        handleRequest(btn.closest(".row").dataset.uid, btn.dataset.act);
      };
    });
  }

  function renderBlocks() {
    const el = $("block-list");
    if (!el) return;
    if (!state.blocks.length) {
      el.innerHTML = `<div class="row"><div class="row-sub">空</div></div>`;
      return;
    }
    el.innerHTML = state.blocks
      .map(
        (uid) => `<div class="row" data-uid="${uid}">
          ${avatarHTML("", uid, uid)}
          <div class="row-main"><div class="row-title">${escapeHtml(uid)}</div></div>
          <div class="row-actions"><button type="button" data-act="unblock">解除</button></div>
        </div>`
      )
      .join("");
    el.querySelectorAll("[data-act=unblock]").forEach((btn) => {
      btn.onclick = (e) => {
        e.stopPropagation();
        setBlocked(btn.closest(".row").dataset.uid, false);
      };
    });
  }

  async function handleRequest(peer, action) {
    try {
      await api("/v1/friend-requests", {
        method: "POST",
        body: JSON.stringify({ peer_uid: peer, action }),
      });
      await loadRequests();
      await loadFriends();
      await loadConvs();
    } catch (err) {
      dlgAlert(err.message);
    }
  }

  async function removeFriend(peer) {
    if (!peer) return;
    if (!(await dlgConfirm("删除后双方会话仍保留，但无法再发起聊天。", "删除好友 " + nickOf(peer) + "？", true))) return;
    try {
      await api("/v1/friends", { method: "DELETE", body: JSON.stringify({ peer_uid: peer }) });
      await loadFriends();
      await loadConvs();
    } catch (err) {
      dlgAlert(err.message);
    }
  }

  async function setBlocked(peer, blocked) {
    try {
      await api("/v1/blocks", {
        method: "POST",
        body: JSON.stringify({ peer_uid: peer, unblock: !blocked }),
      });
      await loadBlocks();
      await loadFriends();
      await loadConvs();
    } catch (err) {
      dlgAlert(err.message);
    }
  }

  async function refreshPresence() {
    const uids = new Set();
    state.friends.forEach((f) => {
      const uid = friendUid(f);
      if (uid) uids.add(uid);
    });
    state.convs.forEach((c) => {
      const p = field(c, "peerUid", "peer_uid");
      if (p) uids.add(p);
    });
    if (state.activePeer) uids.add(state.activePeer);
    if (state.group) (state.group.members || []).forEach((m) => {
      const id = uidOf(m);
      if (id) uids.add(id);
    });
    if (!uids.size) return;
    try {
      const data = await api("/v1/presence?uids=" + encodeURIComponent([...uids].join(",")));
      state.online = data.online || {};
      renderLists();
      updateChatHeader();
    } catch (_) {}
  }

  function startPresencePoll() {
    clearInterval(state.presenceTimer);
    refreshPresence();
    state.presenceTimer = setInterval(refreshPresence, 15000);
  }

  function lockComposer() {
    if (!state.activeCid) {
      setComposerEnabled(false);
      return;
    }
    const blocked = !isGroup(state.activeCid) && isBlocked(state.activePeer);
    setComposerEnabled(!blocked);
  }

  function updateChatHeader() {
    if (!state.activeCid) return;
    if (isGroup(state.activeCid)) return;
    const on = state.online[state.activePeer];
    const sub = $("chat-sub");
    if (isBlocked(state.activePeer)) sub.textContent = "已拉黑，无法发送";
    else sub.textContent = (on ? "在线 · " : "离线 · ") + (state.activePeer || "");
  }

  async function refreshGroupRead() {
    if (!isGroup(state.activeCid)) return;
    try {
      const data = await api("/v1/read-state?cid=" + encodeURIComponent(state.activeCid));
      const next = {};
      (data.cursors || []).forEach((c) => {
        next[c.uid] = Number(c.convSeq || c.conv_seq || 0);
      });
      state.readCursors = next;
      renderMsgs({ stick: false });
    } catch (_) {}
  }

  function markRead() {
    if (!state.activeCid) return;
    let max = 0;
    for (const m of state.messages) {
      const s = Number(field(m, "convSeq", "conv_seq") || 0);
      if (s > max) max = s;
    }
    if (!max) return;
    try {
      sendFrame({ read: { cid: state.activeCid, convSeq: String(max) } });
    } catch (_) {}
  }

  async function refreshGroup() {
    const bar = $("group-bar");
    if (!isGroup(state.activeCid)) {
      bar.classList.add("hidden");
      toggleHidden("side-group-admin", true);
      toggleHidden("side-direct", !state.activeCid);
      state.group = null;
      $("member-chips").innerHTML = "";
      renderSidePeer();
      return;
    }
    bar.classList.remove("hidden");
    toggleHidden("side-direct", true);
    toggleHidden("side-group-admin", false);
    try {
      const g = await api("/v1/group?cid=" + encodeURIComponent(state.activeCid));
      state.group = g;
      const members = g.members || [];
      $("chat-title").textContent = (g.name || "群聊") + " (" + members.length + ")";
      $("chat-sub").textContent = "";
      if ($("side-group-name")) $("side-group-name").textContent = g.name || "";
      const avEl = $("side-group-avatar");
      if (avEl) {
        const url = field(g, "avatarUrl", "avatar_url") || "";
        avEl.innerHTML = url ? `<img class="avatar" src="${escapeHtml(url)}" alt="" />` : "";
      }
      const owner = g.ownerUid || g.owner_uid;
      const isOwner = owner === state.uid;
      const manager = isGroupManager();
      if ($("side-announce")) $("side-announce").textContent = g.announcement || "未设置";
      const me = members.find((m) => uidOf(m) === state.uid) || {};
      if ($("side-my-nick")) $("side-my-nick").textContent = me.nickname || me.Nickname || nickOf(state.uid) || state.uid;
      setSwitch("join-approval-switch", !!(g.joinApproval || g.join_approval));
      toggleHidden("join-approval-btn", !isOwner);
      toggleHidden("transfer-btn", !isOwner);
      toggleHidden("dissolve-btn", !isOwner);
      if ($("mute-all-btn")) {
        $("mute-all-btn").classList.toggle("hidden", !manager);
        const mutedAll = !!(g.mutedAll || g.muted_all);
        $("mute-all-btn").textContent = mutedAll ? "解除禁言" : "全员禁言";
      }
      await renderJoinReqs(manager);
      let names = {};
      try {
        const pr = await api("/v1/profiles?uids=" + encodeURIComponent(members.map((m) => uidOf(m)).filter(Boolean).join(",")));
        (pr.users || []).forEach((u) => {
          const id = uidOf(u);
          names[id] = field(u, "displayName", "display_name") || id;
        });
        rememberProfiles(pr.users);
      } catch (_) {}
      const addTile = `<button type="button" class="side-member add" id="side-add-btn"><span class="side-add-plus">+</span><span class="side-member-name">添加</span></button>`;
      $("member-chips").innerHTML = members
        .map((m) => {
          const id = uidOf(m);
          const kick = manager && id && id !== state.uid && (m.role !== "owner") ? " kick" : "";
          const label = (m.nickname || m.Nickname) || names[id] || nickOf(id) || id;
          const badge = m.role === "owner" ? " ·群主" : m.role === "admin" ? " ·管理" : "";
          return `<div class="side-member${kick}" data-uid="${escapeHtml(id)}" data-name="${escapeHtml(label)}" data-role="${escapeHtml(m.role || "")}">${avatarHTML(avatarOf(id), label, id)}<span class="side-member-name">${escapeHtml(label)}${badge}</span></div>`;
        })
        .join("") + addTile;
      $("member-chips").querySelectorAll(".side-member:not(.add)").forEach((el) => {
        el.onclick = () => onMemberClick(el.dataset.uid, el.dataset.role);
      });
      const addBtn = $("side-add-btn");
      if (addBtn) {
        addBtn.onclick = () => {
          const box = $("side-invite");
          if (!box) return;
          const show = box.classList.contains("hidden");
          box.classList.toggle("hidden", !show);
          if (show && $("invite-uid")) $("invite-uid").focus();
        };
      }
      if ($("member-search") && $("member-search").value) {
        $("member-search").dispatchEvent(new Event("input"));
      }
    } catch (err) {
      $("chat-sub").textContent = err.message;
    }
  }

  async function renderJoinReqs(manager) {
    const wrap = $("join-req-wrap");
    const list = $("join-req-list");
    if (!wrap || !list) return;
    if (!manager) {
      wrap.classList.add("hidden");
      return;
    }
    try {
      const data = await api("/v1/group-join-requests?cid=" + encodeURIComponent(state.activeCid));
      const reqs = data.requests || [];
      wrap.classList.toggle("hidden", !reqs.length);
      list.innerHTML = reqs
        .map((r) => {
          const uid = r.uid;
          return `<div class="row" data-uid="${escapeHtml(uid)}">${avatarHTML(avatarOf(uid), nickOf(uid), uid)}<div class="row-main"><div class="row-title">${escapeHtml(nickOf(uid) || uid)}</div></div><div class="row-actions"><button type="button" data-act="ok">通过</button><button type="button" class="danger" data-act="no">拒绝</button></div></div>`;
        })
        .join("");
      list.querySelectorAll(".row").forEach((row) => {
        row.querySelectorAll("button").forEach((btn) => {
          btn.onclick = async (e) => {
            e.stopPropagation();
            try {
              await api("/v1/group-join-requests", {
                method: "POST",
                body: JSON.stringify({ cid: state.activeCid, uid: row.dataset.uid, accept: btn.dataset.act === "ok" }),
              });
              await refreshGroup();
              await loadConvs();
            } catch (err) {
              dlgAlert(err.message);
            }
          };
        });
      });
    } catch (_) {
      wrap.classList.add("hidden");
    }
  }

  async function onMemberClick(uid, role) {
    if (!uid) return;
    if (uid === state.uid) {
      if ($("mynick-row")) $("mynick-row").click();
      return;
    }
    if (!isGroupManager()) return;
    const owner = state.group && (state.group.ownerUid || state.group.owner_uid);
    const isOwner = owner === state.uid;
    const m = (state.group.members || []).find((x) => uidOf(x) === uid) || {};
    const muted = !!(m.muted || m.Muted);
    const items = [];
    items.push([muted ? "解除禁言" : "禁言", async () => {
      await api("/v1/group-member", { method: "POST", body: JSON.stringify({ cid: state.activeCid, uid, muted: !muted }) });
      await refreshGroup();
    }]);
    if (isOwner && role !== "owner") {
      const next = role === "admin" ? "member" : "admin";
      items.push([next === "admin" ? "设为管理员" : "取消管理员", async () => {
        await api("/v1/group-member", { method: "POST", body: JSON.stringify({ cid: state.activeCid, uid, role: next }) });
        await refreshGroup();
      }]);
    }
    if (role !== "owner") {
      items.push(["移出本群", async () => {
        if (!(await dlgConfirm("将把该成员移出本群。", "移出 " + memberNick(uid), true))) return;
        await api("/v1/group-kick", { method: "POST", body: JSON.stringify({ cid: state.activeCid, uid }) });
        await refreshGroup();
        await loadConvs();
      }]);
    }
    const menu = $("msg-menu");
    menu.innerHTML = items.map((it, i) => `<button type="button" data-i="${i}">${it[0]}</button>`).join("");
    menu.querySelectorAll("button").forEach((btn) => {
      btn.onclick = async () => {
        hideMsgMenu();
        try {
          await items[Number(btn.dataset.i)][1]();
        } catch (err) {
          dlgAlert(err.message);
        }
      };
    });
    menu.classList.remove("hidden");
    menu.style.left = "60%";
    menu.style.top = "120px";
  }

  async function openChat(peer, cid) {
    if (state.activeCid && state.activeCid !== cid) saveDraft();
    if (!isGroup(cid)) {
      const derived = peerFromCid(cid, state.uid);
      if (derived) peer = derived;
      else if (!peer || peer === state.uid) {
        const row = state.convs.find((c) => c.cid === cid);
        peer = field(row || {}, "peerUid", "peer_uid") || peer || "";
      }
      if (peer === state.uid) peer = "";
    } else {
      peer = "";
    }
    state.activePeer = peer || "";
    state.activeCid = cid;
    state.peerReadSeq = 0;
    state.hasMore = false;
    state.searchQ = "";
    state.readCursors = {};
    if ($("chat-search")) $("chat-search").value = "";
    $("chat-search-bar").classList.add("hidden");
    const conv = state.convs.find((c) => c.cid === cid);
    if (isGroup(cid)) {
      $("chat-title").textContent = (conv && conv.title) || "群聊";
      $("chat-sub").textContent = "";
    } else {
      $("chat-title").textContent = (conv && convTitle(conv)) || peer;
      updateChatHeader();
    }
    toggleHidden("side-shared", false);
    toggleHidden("chat-search-toggle", false);
    toggleHidden("mute-btn", false);
    toggleHidden("pin-btn", false);
    toggleHidden("hide-btn", false);
    toggleHidden("remark-btn", isGroup(cid));
    toggleHidden("tag-btn", isGroup(cid));
    toggleHidden("e2ee-btn", isGroup(cid));
    toggleHidden("block-btn", isGroup(cid));
    toggleHidden("qr-card-btn", isGroup(cid));
    toggleHidden("e2ee-fp", isGroup(cid) || !state.e2eeOn);
    toggleHidden("side-direct", isGroup(cid));
    toggleHidden("side-group-admin", !isGroup(cid));
    if (!isGroup(cid)) renderSidePeer();
    syncSideSwitches();
    if (!isGroup(cid) && state.e2eeOn) refreshE2eeFp();
    lockComposer();
    loadDraft(cid);
    await reloadTimeline();
    renderLists();
    await refreshGroup();
    markRead();
    await loadConvs();
    refreshPresence();
    refreshGroupRead();
  }

  async function reloadTimeline() {
    if (!state.activeCid) return;
    const cid = state.activeCid;
    const q = state.searchQ ? "&q=" + encodeURIComponent(state.searchQ) : "";
    const data = await api("/v1/timeline?cid=" + encodeURIComponent(cid) + "&limit=50" + q);
    if (state.activeCid !== cid) return;
    const fetched = data.messages || [];
    const seen = {};
    fetched.forEach((m) => {
      const id = String(field(m, "msgId", "msg_id") || "");
      if (id) seen[id] = true;
    });
    const live = state.messages.filter((m) => {
      if (m.cid && m.cid !== cid) return false;
      const id = String(field(m, "msgId", "msg_id") || "");
      if (id) return !seen[id];
      return !!(m.clientMsgId && m.status && m.status !== "acked");
    });
    state.messages = fetched.concat(live);
    state.messages.sort((a, b) => {
      const sa = Number(field(a, "convSeq", "conv_seq") || 0);
      const sb = Number(field(b, "convSeq", "conv_seq") || 0);
      if (sa && sb && sa !== sb) return sa - sb;
      return Number(field(a, "createdAtMs", "created_at_ms") || 0) - Number(field(b, "createdAtMs", "created_at_ms") || 0);
    });
    for (const m of state.messages) {
      await decodeIncoming(m);
    }
    state.hasMore = !state.searchQ && !!(data.hasMore || data.has_more);
    const pending = state.outbox.filter((m) => m.cid === cid && m.status !== "acked");
    pending.forEach((p) => {
      if (!state.messages.some((m) => m.clientMsgId && m.clientMsgId === p.clientMsgId)) state.messages.push(p);
    });
    await hydrateMsgProfiles();
    renderMsgs();
    watchEphemeral();
  }

  async function loadOlder() {
    if (!state.activeCid || state.loadingMore || !state.hasMore || state.searchQ) return;
    const first = state.messages[0];
    const before = Number(field(first, "convSeq", "conv_seq") || 0);
    if (!before) return;
    state.loadingMore = true;
    try {
      const data = await api(
        "/v1/timeline?cid=" + encodeURIComponent(state.activeCid) + "&before=" + before + "&limit=50"
      );
      const older = data.messages || [];
      state.hasMore = !!(data.hasMore || data.has_more);
      if (!older.length) state.hasMore = false;
      if (older.length) {
        for (const m of older) await decodeIncoming(m);
        state.messages = older.concat(state.messages);
        await hydrateMsgProfiles();
        renderMsgs({ stick: false });
      }
    } catch (_) {
      state.hasMore = false;
    }
    state.loadingMore = false;
  }

  function sendFrame(body) {
    const env = Object.assign({ requestId: String(state.reqId++) }, body);
    if (state.isLeader && state.ws && state.ws.readyState === WebSocket.OPEN) {
      state.ws.send(JSON.stringify(env));
      return;
    }
    if (state.tabCh && !state.isLeader) {
      state.tabCh.postMessage({ type: "send", env: env, uid: state.uid });
      return;
    }
    throw new Error("offline");
  }

  function broadcastFrame(env) {
    if (state.tabCh && state.isLeader) {
      state.tabCh.postMessage({ type: "frame", env: env, uid: state.uid });
    }
  }

  function closeTabChannel() {
    if (state.wsElectTimer) {
      clearInterval(state.wsElectTimer);
      state.wsElectTimer = null;
    }
    if (state.tabCh) {
      try {
        state.tabCh.close();
      } catch (_) {}
      state.tabCh = null;
    }
  }

  function disconnectWS() {
    clearInterval(state.hb);
    state.hb = null;
    if (state.ws) {
      state.ws.onclose = null;
      try {
        state.ws.close();
      } catch (_) {}
      state.ws = null;
    }
  }

  function startWSElection() {
    disconnectWS();
    closeTabChannel();
    state.isLeader = false;
    state.leaderAt = 0;
    state.tabId = uuid();
    if (!("BroadcastChannel" in window) || !state.uid) {
      state.isLeader = true;
      connect();
      return;
    }
    // Per-uid channel: a shared "surge-ws" would let u5's send ride u1's socket.
    const uid = state.uid;
    const ch = new BroadcastChannel("surge-ws:" + uid);
    state.tabCh = ch;
    ch.onmessage = (e) => {
      const m = e.data || {};
      if (m.uid && m.uid !== state.uid) return;
      if (m.type === "leader" && m.id && m.id !== state.tabId) {
        state.leaderAt = Date.now();
        if (state.isLeader && String(m.id) < String(state.tabId)) {
          yieldLeader();
        }
      }
      if (m.type === "elect" && state.isLeader) {
        ch.postMessage({ type: "leader", id: state.tabId, uid: state.uid });
      }
      if (m.type === "frame" && !state.isLeader && m.env) {
        onFrame(m.env);
      }
      if (m.type === "send" && state.isLeader && m.env && state.ws && state.ws.readyState === WebSocket.OPEN) {
        try {
          state.ws.send(JSON.stringify(m.env));
        } catch (_) {}
      }
    };
    ch.postMessage({ type: "elect", id: state.tabId, uid: uid });
    setTimeout(() => {
      if (state.uid !== uid || state.tabCh !== ch) return;
      if (Date.now() - state.leaderAt < 600) {
        state.isLeader = false;
        setConn("跟随标签页", true);
        return;
      }
      becomeLeader();
    }, 400);
    state.wsElectTimer = setInterval(() => {
      if (state.uid !== uid || state.tabCh !== ch) return;
      if (state.isLeader) ch.postMessage({ type: "leader", id: state.tabId, uid: state.uid });
      else if (Date.now() - state.leaderAt > 2000) becomeLeader();
    }, 800);
  }

  function becomeLeader() {
    if (state.isLeader && state.ws) return;
    state.isLeader = true;
    connect();
  }

  function yieldLeader() {
    state.isLeader = false;
    disconnectWS();
    setConn("跟随标签页", true);
  }

  function applyRecall(cid, msgId, burned) {
    const hit = (m) => field(m, "msgId", "msg_id") === msgId;
    state.messages.filter(hit).forEach((m) => {
      const was = burned || isEphemeral(m);
      m.recalled = true;
      m._burnLeft = 0;
      m.payload = { type: "RECALL", text: was ? "已销毁" : "" };
    });
    if (cid === state.activeCid) renderMsgs();
    loadConvs();
  }

  function showTyping(from) {
    if (from === state.uid) return;
    const el = $("typing");
    el.textContent = isGroup(state.activeCid) ? from + " 正在输入…" : "对方正在输入…";
    el.classList.remove("hidden");
    clearTimeout(state.typingTimer);
    state.typingTimer = setTimeout(() => el.classList.add("hidden"), 3000);
  }

  function notifyIncoming(ev) {
    const from = senderOf(ev);
    if (from === state.uid) return;
    const mentions = (ev.payload && (ev.payload.mentionUids || ev.payload.mention_uids)) || [];
    const mentioned = mentions.indexOf(state.uid) >= 0 || ((ev.payload && ev.payload.text) || "").indexOf("@" + state.uid) >= 0;
    if (isMuted(ev.cid) && !mentioned) return;
    if (!document.hidden) return;
    if (!window.Notification || Notification.permission !== "granted") return;
    const text = (ev.payload && ev.payload.text) || "";
    new Notification(from || "新消息", { body: text.slice(0, 80) });
  }

  function onFrame(env) {
    if (env.authOk || env.auth_ok) {
      const ok = env.authOk || env.auth_ok;
      state.lastSyncSeq = Number(ok.lastSyncSeq || ok.last_sync_seq || 0);
      kvSet(state.uid + ":seq", state.lastSyncSeq);
      sendFrame({ sync: { lastSyncSeq: String(state.lastSyncSeq), limit: 200 } });
      flushOutbox();
      return;
    }
    if (env.ack) {
      const id = env.ack.clientMsgId || env.ack.client_msg_id;
      const item = state.outbox.find((m) => m.clientMsgId === id);
      if (item) {
        item.status = "acked";
        item.msgId = env.ack.msgId || env.ack.msg_id;
        item.convSeq = Number(env.ack.convSeq || env.ack.conv_seq || 0);
        item.createdAtMs = Number(env.ack.createdAtMs || env.ack.created_at_ms || Date.now());
      }
      kvSet(state.uid + ":outbox", state.outbox.filter((m) => m.status !== "acked"));
      const msg = state.messages.find((m) => m.clientMsgId === id);
      if (msg) {
        msg.status = "";
        msg.msgId = env.ack.msgId || env.ack.msg_id;
        msg.convSeq = Number(env.ack.convSeq || env.ack.conv_seq || 0);
        msg.createdAtMs = Number(env.ack.createdAtMs || env.ack.created_at_ms || Date.now());
      }
      renderMsgs();
      watchEphemeral();
      loadConvs();
      refreshGroupRead();
      return;
    }
    const pushed = env.push || env.Push;
    if (pushed) {
      ingest(pushed);
      return;
    }
    if (env.recalled) {
      applyRecall(env.recalled.cid, env.recalled.msgId || env.recalled.msg_id);
      return;
    }
    if (env.typing) {
      if (env.typing.cid === state.activeCid) showTyping(senderOf(env.typing));
      return;
    }
    if (env.read) {
      if (env.read.cid === state.activeCid) {
        state.peerReadSeq = Number(env.read.convSeq || env.read.conv_seq || 0);
        renderMsgs({ stick: false });
        refreshGroupRead();
      }
      return;
    }
    if (env.syncResp || env.sync_resp) {
      const sr = env.syncResp || env.sync_resp;
      (sr.events || []).forEach(ingest);
      state.lastSyncSeq = Number(sr.lastSyncSeq || sr.last_sync_seq || state.lastSyncSeq);
      kvSet(state.uid + ":seq", state.lastSyncSeq);
      if (sr.hasMore) sendFrame({ sync: { lastSyncSeq: String(state.lastSyncSeq), limit: 200 } });
      return;
    }
    if (env.error) {
      const id = env.error.clientMsgId || env.error.client_msg_id;
      const raw = env.error.message || "";
      const item = state.outbox.find((m) => m.clientMsgId === id);
      if (item) {
        item.status = "fail";
        if (/unsupported payload type/i.test(raw)) item.dead = true;
      }
      const msg = state.messages.find((m) => m.clientMsgId === id);
      if (msg) msg.status = "fail";
      renderMsgs({ stick: false });
      kvSet(state.uid + ":outbox", state.outbox.filter((m) => m.status !== "acked" && !m.dead));
      const code = Number(env.error.code || 0);
      if (code === 429 || /too many/i.test(raw)) {
        toast("发送过于频繁，请稍后再试");
        setConn("发送过于频繁", false);
        return;
      }
      if (/blocked/i.test(raw)) {
        toast("已拉黑，无法发送");
        setConn("已连接", true);
        return;
      }
      if (/unsupported payload type/i.test(raw)) {
        toast("有一条草稿格式异常，已跳过");
        setConn("已连接", true);
        return;
      }
      if (raw) {
        toast(friendlyRpc(raw));
        setConn("已连接", true);
      }
    }
  }

  function isRosterPush(ev) {
    return String((ev && ev.cid) || "").indexOf("sys:") === 0;
  }

  function applyRoster(ev) {
    const raw = String((ev.payload && ev.payload.text) || "").trim();
    const i = raw.indexOf(" ");
    const kind = i < 0 ? raw : raw.slice(0, i);
    const extra = i < 0 ? "" : raw.slice(i + 1);
    const from = senderOf(ev);
    const name = nickOf(from) || from;
    const key = kind + ":" + from + ":" + extra;
    const now = Date.now();
    if (state.rosterSeen && state.rosterSeen[key] && now - state.rosterSeen[key] < 2500) {
      loadFriends();
      loadRequests();
      loadConvs();
      return;
    }
    state.rosterSeen = state.rosterSeen || {};
    state.rosterSeen[key] = now;
    loadFriends();
    loadRequests();
    loadConvs();
    if (kind === "friend_request") {
      toast(name + " 请求添加你为好友");
      setTab("contacts");
      return;
    }
    if (kind === "friend_accept") {
      toast(name + " 已添加你为好友");
      return;
    }
    if (kind === "friend_remove") {
      toast(name + " 已将你删除");
      return;
    }
    if (kind === "friend_decline") {
      toast(name + " 拒绝了好友申请");
      return;
    }
    if (kind === "group_invite") {
      toast(name + " 邀请你加入群聊");
      setTab("chats");
      return;
    }
    if (kind === "group_join_request") {
      toast(name + " 申请加入群聊");
      if (state.activeCid) refreshGroup();
      return;
    }
    if (kind === "group_kick") {
      toast("你已被移出群聊");
      if (extra && extra === state.activeCid) {
        state.activeCid = "";
        state.activePeer = "";
        state.messages = [];
        $("chat-title").textContent = "选择一个会话";
        $("chat-sub").textContent = "";
        renderMsgs();
      }
      setTab("chats");
      return;
    }
    if (kind === "group_dissolve") {
      toast("群聊已解散");
      if (extra && extra === state.activeCid) {
        state.activeCid = "";
        state.activePeer = "";
        state.messages = [];
        $("chat-title").textContent = "选择一个会话";
        $("chat-sub").textContent = "";
        renderMsgs();
      }
      setTab("chats");
    }
  }

  function ingest(ev) {
    if (isRosterPush(ev)) {
      applyRoster(ev);
      return;
    }
    const cid = String(field(ev, "cid", "cid") || ev.cid || "").trim();
    const from = senderOf(ev);
    const seq = Number(ev.syncSeq || ev.sync_seq || 0);
    const msgId = String(field(ev, "msgId", "msg_id") || "").trim();
    if (seq > state.lastSyncSeq) state.lastSyncSeq = seq;
    if (cid && cid === state.activeCid) {
      const dup = msgId && state.messages.some((m) => String(field(m, "msgId", "msg_id") || "") === msgId);
      if (!dup) {
        const row = {
          msgId,
          cid,
          fromUid: from,
          payload: ev.payload,
          convSeq: Number(ev.convSeq || ev.conv_seq || 0),
          createdAtMs: Number(ev.createdAtMs || ev.created_at_ms || Date.now()),
        };
        state.messages.push(row);
        renderMsgs();
        decodeIncoming(row).then(async () => {
          if (from) await ensureProfiles([from]);
          renderMsgs();
          watchEphemeral();
        });
        refreshGroupRead();
      }
      markRead();
    }
    notifyIncoming(ev);
    loadConvs();
  }

  function recall(msgId) {
    if (!msgId || !state.activeCid) return;
    try {
      sendFrame({ recall: { cid: state.activeCid, msgId } });
      applyRecall(state.activeCid, msgId);
    } catch (err) {
      dlgAlert(err.message);
    }
  }

  function connect() {
    disconnectWS();
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(proto + "//" + location.host + "/v1/ws");
    state.ws = ws;
    setConn("连接中…", false);
    ws.onopen = () => {
      setConn("已连接", true);
      sendFrame({ auth: { accessToken: state.token, deviceId: deviceId() } });
    };
    ws.onmessage = (e) => {
      const handle = (text) => {
        try {
          const env = JSON.parse(text);
          onFrame(env);
          broadcastFrame(env);
        } catch (err) {
          console.error(err);
        }
      };
      if (typeof e.data === "string") {
        handle(e.data);
        return;
      }
      if (e.data && typeof e.data.text === "function") {
        e.data.text().then(handle).catch(() => {});
      }
    };
    ws.onclose = () => {
      if (!state.isLeader) return;
      setConn("重连中", false);
      clearInterval(state.hb);
      setTimeout(connect, 1500);
    };
    clearInterval(state.hb);
    state.hb = setInterval(() => {
      try {
        sendFrame({ heartbeat: {} });
      } catch (_) {}
    }, 30000);
  }

  async function flushOutbox() {
    for (const m of state.outbox) {
      if (m.status === "acked" || m.status === "fail" || m.dead) continue;
      try {
        const send = {
          clientMsgId: m.clientMsgId,
          payload: wirePayload(m.payload || { type: "TEXT", text: m.text }),
        };
        if (send.payload && typeof send.payload.text === "string") send.payload.text = wellFormed(send.payload.text);
        if (m.cid) send.cid = m.cid;
        else if (m.peerUid) send.peerUid = m.peerUid;
        if (m.quoteMsgId) send.quoteMsgId = m.quoteMsgId;
        sendFrame({ send });
      } catch (_) {
        break;
      }
    }
  }

  async function sessionEnter(uid, token, refresh) {
    state.uid = uid;
    state.token = token;
    state.refresh = refresh || "";
    sessionStorage.setItem("surge_uid", state.uid);
    sessionStorage.setItem("surge_token", state.token);
    if (state.refresh) sessionStorage.setItem("surge_refresh", state.refresh);
    await loadMe();
    loadMuted();
    loadPins();
    $("login").classList.add("hidden");
    $("confirm-qr").classList.add("hidden");
    $("app").classList.remove("hidden");
    if (window.Notification && Notification.permission === "default") {
      Notification.requestPermission().catch(() => {});
    }
    state.lastSyncSeq = Number((await kvGet(state.uid + ":seq")) || 0);
    state.outbox = ((await kvGet(state.uid + ":outbox")) || []).filter((m) => !m.dead);
    await loadFriends();
    await loadRequests();
    await loadBlocks();
    await loadConvs();
    startPresencePoll();
    startWSElection();
    maybeApproveTicket();
    e2eePair().catch(() => {});
    maybeAddFriendFromURL();
  }

  function maybeAddFriendFromURL() {
    const add = state.addPeer || new URLSearchParams(location.search).get("add") || "";
    if (!add || add === state.uid || !state.token) return;
    state.addPeer = "";
    history.replaceState({}, "", "/");
    api("/v1/friend-requests", { method: "POST", body: JSON.stringify({ peer_uid: add }) })
      .then(() => {
        toast("已发送好友申请：" + add);
        return loadRequests();
      })
      .catch((err) => toast(err.message || "加好友失败"));
  }

  function maybeApproveTicket() {
    if (!state.pendingTicket || !state.token) return;
    $("confirm-qr").classList.remove("hidden");
  }

  async function startQR() {
    try {
      const d = await fetch("/v1/auth/qr/new", { method: "POST" }).then(async (r) => {
        const t = await r.text();
        if (!r.ok) throw new Error(t);
        return JSON.parse(t);
      });
      $("qr-img").src = d.png + (d.png.indexOf("?") >= 0 ? "&" : "?") + "t=" + Date.now();
      $("qr-hint").textContent = "已登录窗口打开本页扫码，或点确认登录";
      state.qrTicket = d.ticket;
      clearInterval(state.qrTimer);
      state.qrTimer = setInterval(async () => {
        try {
          const st = await fetch("/v1/auth/qr/status?ticket=" + encodeURIComponent(state.qrTicket)).then((r) => r.json());
          if (st.status === "approved" && st.access_token) {
            clearInterval(state.qrTimer);
            await sessionEnter(st.uid, st.access_token, st.refresh_token);
          }
        } catch (_) {}
      }, 1000);
    } catch (err) {
      $("qr-hint").textContent = "二维码加载失败，可改用上方 uid 登录";
      console.error(err);
    }
  }

  function mentionUidsOf(text) {
    const out = [];
    if (/@所有人\b/.test(text || "") && isGroup(state.activeCid) && state.group) {
      (state.group.members || []).forEach((m) => {
        const id = uidOf(m);
        if (id && id !== state.uid) out.push(id);
      });
    }
    const re = /@([A-Za-z0-9._@+-]{1,64})/g;
    let m;
    while ((m = re.exec(text || ""))) {
      if (out.indexOf(m[1]) < 0) out.push(m[1]);
    }
    return out;
  }

  function b64(buf) {
    return btoa(String.fromCharCode.apply(null, new Uint8Array(buf)));
  }
  function unb64(s) {
    const bin = atob(s);
    const out = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
    return out;
  }
  async function e2eePair() {
    const raw = await kvGet(state.uid + ":e2ee");
    if (raw && raw.pub && raw.priv) {
      return {
        pub: raw.pub,
        priv: await crypto.subtle.importKey("jwk", raw.priv, { name: "ECDH", namedCurve: "P-256" }, true, ["deriveBits"]),
      };
    }
    const kp = await crypto.subtle.generateKey({ name: "ECDH", namedCurve: "P-256" }, true, ["deriveBits"]);
    const pubBuf = await crypto.subtle.exportKey("raw", kp.publicKey);
    const privJwk = await crypto.subtle.exportKey("jwk", kp.privateKey);
    const pub = b64(pubBuf);
    await kvSet(state.uid + ":e2ee", { pub, priv: privJwk });
    await api("/v1/e2ee/keys", { method: "POST", body: JSON.stringify({ public_key: pub }) });
    return { pub, priv: kp.privateKey };
  }
  async function peerPubKey(uid) {
    const data = await api("/v1/e2ee/keys?uids=" + encodeURIComponent(uid));
    const u = (data.users || []).find((x) => x.uid === uid);
    const k = u && (u.publicKey || u.public_key);
    if (!k) throw new Error("对方未开启加密");
    return crypto.subtle.importKey("raw", unb64(k), { name: "ECDH", namedCurve: "P-256" }, true, []);
  }
  async function aesFromPeer(peerUid) {
    const mine = await e2eePair();
    const theirs = await peerPubKey(peerUid);
    const bits = await crypto.subtle.deriveBits({ name: "ECDH", public: theirs }, mine.priv, 256);
    return crypto.subtle.importKey("raw", bits, { name: "AES-GCM" }, false, ["encrypt", "decrypt"]);
  }
  async function encryptPayload(payload, peerUid) {
    const key = await aesFromPeer(peerUid);
    const iv = crypto.getRandomValues(new Uint8Array(12));
    const ct = await crypto.subtle.encrypt({ name: "AES-GCM", iv }, key, new TextEncoder().encode(payload.text || ""));
    payload.text = b64(iv) + "." + b64(ct);
    payload.e2ee = true;
    return payload;
  }
  async function decodeIncoming(m) {
    const p = m.payload;
    if (!p || !(p.e2ee || p.e2Ee)) return m;
    const from = senderOf(m);
    const peer = from === state.uid ? peerFromCid(field(m, "cid", "cid") || state.activeCid, state.uid) || state.activePeer : from;
    try {
      const key = await aesFromPeer(peer);
      const parts = String(p.text || "").split(".");
      if (parts.length !== 2) throw new Error("bad cipher");
      const pt = await crypto.subtle.decrypt({ name: "AES-GCM", iv: unb64(parts[0]) }, key, unb64(parts[1]));
      p.text = new TextDecoder().decode(pt);
      p.e2ee = false;
    } catch (_) {
      p.text = "[无法解密]";
    }
    return m;
  }
  const BURN_SECS = 5;
  function watchEphemeral() {
    state.messages.forEach((m) => {
      if (m._burnQueued || m.recalled) return;
      if (!isEphemeral(m)) return;
      if (senderOf(m) === state.uid) return;
      const id = field(m, "msgId", "msg_id");
      if (!id) return;
      m._burnQueued = true;
      m._burnLeft = BURN_SECS;
      renderMsgs({ stick: false });
      const tick = setInterval(() => {
        if (m.recalled) {
          clearInterval(tick);
          return;
        }
        m._burnLeft = Math.max(0, (m._burnLeft || 1) - 1);
        if (m._burnLeft <= 0) {
          clearInterval(tick);
          api("/v1/ephemeral/consume", { method: "POST", body: JSON.stringify({ cid: state.activeCid, msg_id: id }) })
            .then(() => applyRecall(state.activeCid, id, true))
            .catch(() => {
              m._burnQueued = false;
            });
        }
        if (cidOfActive(m)) renderMsgs({ stick: false });
      }, 1000);
    });
  }
  function cidOfActive(m) {
    return !m.recalled && state.messages.indexOf(m) >= 0;
  }

  async function attachLinkPreview(payload) {
    const text = payload.text || "";
    const m = text.match(/https?:\/\/[^\s]+/);
    if (!m) return payload;
    try {
      payload.link = await api("/v1/link-preview", { method: "POST", body: JSON.stringify({ url: m[0] }) });
    } catch (_) {}
    return payload;
  }

  async function sendPayload(payload, dest) {
    const cid = dest && dest.cid ? dest.cid : state.activeCid;
    if (!cid) return;
    const fallback = dest && dest.peerUid !== undefined ? dest.peerUid : state.activePeer;
    const peerUid = isGroup(cid) ? "" : peerFromCid(cid, state.uid) || fallback || "";
    if (!isGroup(cid) && isBlocked(peerUid || state.activePeer)) {
      toast("已拉黑，无法发送");
      return;
    }
    if (typeof payload.text === "string") payload.text = wellFormed(payload.text);
    if (!(dest && dest.forward)) {
      payload.mentionUids = mentionUidsOf(payload.text || "");
      payload = await attachLinkPreview(payload);
      if ($("burn-toggle") && $("burn-toggle").checked) payload.ephemeral = true;
      if (state.e2eeOn && !isGroup(cid) && payload.text && !payload.e2ee) {
        if (!peerUid || peerUid === state.uid) {
          toast("无法加密：会话对象无效，请重新打开聊天后再发");
          return;
        }
        try {
          payload = await encryptPayload(payload, peerUid);
        } catch (err) {
          toast("加密失败：" + (err.message || err));
          return;
        }
      }
    }
    payload = wirePayload(payload);
    const item = {
      clientMsgId: uuid(),
      peerUid: isGroup(cid) ? "" : peerUid,
      cid,
      fromUid: state.uid,
      text: payload.text || "",
      payload,
      quoteMsgId: dest && dest.forward ? "" : state.quote ? state.quote.id : "",
      status: "pending",
      createdAtMs: Date.now(),
    };
    state.outbox.push(item);
    if (cid === state.activeCid) state.messages.push(item);
    await kvSet(state.uid + ":outbox", state.outbox);
    try {
      const send = {
        clientMsgId: item.clientMsgId,
        cid: item.cid,
        payload,
      };
      if (!item.cid && item.peerUid) send.peerUid = item.peerUid;
      if (item.quoteMsgId) send.quoteMsgId = item.quoteMsgId;
      sendFrame({ send });
    } catch (err) {
      item.status = "fail";
      toast(err.message || "发送失败");
    }
    if (!(dest && dest.forward)) {
      state.quote = null;
      $("quote-bar").classList.add("hidden");
    }
    if (cid === state.activeCid) renderMsgs();
  }

  async function uploadFile(file) {
    const presign = await api("/v1/media/presign", {
      method: "POST",
      body: JSON.stringify({ filename: file.name, content_type: file.type, size: file.size }),
    });
    $("upload-progress").classList.remove("hidden");
    await new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      xhr.open("PUT", presign.put_url);
      xhr.upload.onprogress = (e) => {
        if (!e.lengthComputable) return;
        $("upload-bar").style.width = Math.round((e.loaded / e.total) * 100) + "%";
      };
      xhr.onload = () => (xhr.status >= 200 && xhr.status < 300 ? resolve() : reject(new Error("upload " + xhr.status)));
      xhr.onerror = () => reject(new Error("upload failed"));
      xhr.send(file);
    });
    const done = await api("/v1/media/complete", {
      method: "POST",
      body: JSON.stringify({ object_key: presign.object_key, filename: file.name }),
    });
    $("upload-progress").classList.add("hidden");
    $("upload-bar").style.width = "0";
    const image = (file.type || "").indexOf("image/") === 0;
    await sendPayload({
      type: image ? "IMAGE" : "FILE",
      text: $("draft").value.trim(),
      media: {
        objectKey: done.object_key,
        thumbKey: done.thumb_key,
        contentType: done.content_type,
        filename: file.name,
        size: done.size,
        width: done.width,
        height: done.height,
        url: done.get_url,
        thumbUrl: done.thumb_url,
      },
    });
    $("draft").value = "";
    localStorage.removeItem(draftKey());
    fitDraft();
  }

  async function enter(uid, password) {
    let data;
    if (password) {
      data = await api("/v1/auth/login", {
        method: "POST",
        body: JSON.stringify({ uid, password, device_id: deviceId() }),
      });
    } else {
      data = await api("/v1/auth/dev-login", {
        method: "POST",
        body: JSON.stringify({ uid, device_id: deviceId() }),
      });
    }
    await sessionEnter(data.uid, data.access_token, data.refresh_token);
  }

  async function register(uid, password) {
    const data = await api("/v1/auth/register", {
      method: "POST",
      body: JSON.stringify({
        uid,
        password,
        email: $("login-email") ? $("login-email").value.trim() : "",
        phone: $("login-phone") ? $("login-phone").value.trim() : "",
        device_id: deviceId(),
      }),
    });
    await sessionEnter(data.uid, data.access_token, data.refresh_token);
  }

  $("login-btn").onclick = () => {
    const uid = $("login-uid").value.trim();
    const password = $("login-pass").value;
    if (!uid) return;
    enter(uid, password).catch((e) => dlgAlert(e.message));
  };
  $("register-btn").onclick = () => {
    const uid = $("login-uid").value.trim();
    const password = $("login-pass").value;
    const email = $("login-email") ? $("login-email").value.trim() : "";
    const phone = $("login-phone") ? $("login-phone").value.trim() : "";
    if ((!uid && !email && !phone) || !password) {
      dlgAlert("注册需要密码，以及 uid / 邮箱 / 手机号 至少一项");
      return;
    }
    register(uid, password).catch((e) => dlgAlert(e.message));
  };
  if ($("oauth-demo-btn")) {
    $("oauth-demo-btn").onclick = async () => {
      const subject = await dlgPrompt("例如 GitHub 用户名", "", "第三方登录");
      if (!subject) return;
      api("/v1/auth/oauth/demo", {
        method: "POST",
        body: JSON.stringify({ provider: "github", subject, device_id: deviceId() }),
      })
        .then((data) => sessionEnter(data.uid, data.access_token, data.refresh_token))
        .catch((e) => dlgAlert(e.message));
    };
  }
  $("login-uid").addEventListener("keydown", (e) => {
    if (e.key === "Enter") $("login-btn").click();
  });
  $("login-pass").addEventListener("keydown", (e) => {
    if (e.key === "Enter") $("login-btn").click();
  });
  $("logout").onclick = () => {
    sessionStorage.clear();
    location.reload();
  };

  document.querySelectorAll(".rail-btn[data-tab]").forEach((btn) => {
    btn.onclick = () => setTab(btn.dataset.tab);
  });
  function setTab(tab) {
    state.tab = tab;
    document.querySelectorAll(".rail-btn[data-tab]").forEach((b) => {
      b.classList.toggle("on", b.dataset.tab === tab);
    });
    $("pane-chats").classList.toggle("hidden", tab !== "chats");
    $("pane-contacts").classList.toggle("hidden", tab !== "contacts");
  }
  onClick("new-chat-btn", () => {
    setTab("contacts");
    const el = $("add-uid");
    if (el) el.focus();
  });
  onClick("chat-menu-btn", (e) => {
    e.stopPropagation();
    if (!state.activeCid) return;
    const side = $("chat-side");
    setChatSide(side && side.classList.contains("hidden"));
  });
  onClick("chat-search-toggle", () => {
    const bar = $("chat-search-bar");
    if (!bar) return;
    const show = bar.classList.contains("hidden");
    bar.classList.toggle("hidden", !show);
    if (show && $("chat-search")) $("chat-search").focus();
  });

  $("add-form").onsubmit = async (e) => {
    e.preventDefault();
    const peer = $("add-uid").value.trim();
    if (!peer || peer === state.uid) return;
    try {
      await api("/v1/friend-requests", { method: "POST", body: JSON.stringify({ peer_uid: peer }) });
      $("add-uid").value = "";
      $("search-hits").innerHTML = "";
      await loadRequests();
      await loadFriends();
    } catch (err) {
      dlgAlert(err.message);
    }
  };

  let searchTimer = 0;
  $("add-uid").addEventListener("input", () => {
    const q = $("add-uid").value.trim();
    clearTimeout(searchTimer);
    if (!q) {
      $("search-hits").innerHTML = "";
      return;
    }
    searchTimer = setTimeout(async () => {
      try {
        const data = await api("/v1/users?q=" + encodeURIComponent(q));
        const users = data.users || [];
        $("search-hits").innerHTML = users
          .map((u) => {
            const uid = u.uid;
            const name = u.displayName || u.display_name || uid;
            return `<div class="row" data-uid="${uid}">${avatarHTML(u.avatarUrl || u.avatar_url || "", name, uid)}<div class="row-main"><div class="row-title">${escapeHtml(name)}</div><div class="row-sub">${escapeHtml(uid)}</div></div></div>`;
          })
          .join("");
        $("search-hits").querySelectorAll(".row").forEach((row) => {
          row.onclick = () => {
            $("add-uid").value = row.dataset.uid;
          };
        });
      } catch (_) {
        $("search-hits").innerHTML = "";
      }
    }, 200);
  });

  $("group-form").onsubmit = async (e) => {
    e.preventDefault();
    const name = $("group-name").value.trim();
    const members = $("group-members")
      .value.split(/[,，\s]+/)
      .map((s) => s.trim())
      .filter(Boolean);
    if (!name) return;
    try {
      const g = await api("/v1/groups", { method: "POST", body: JSON.stringify({ name, members }) });
      $("group-name").value = "";
      $("group-members").value = "";
      await loadConvs();
      if (g.cid) await openChat("", g.cid);
    } catch (err) {
      dlgAlert(err.message);
    }
  };

  $("invite-btn").onclick = async () => {
    const uid = $("invite-uid").value.trim();
    if (!uid || !state.activeCid) return;
    try {
      await api("/v1/group-invite", {
        method: "POST",
        body: JSON.stringify({ cid: state.activeCid, members: [uid] }),
      });
      $("invite-uid").value = "";
      toggleHidden("side-invite", true);
      await refreshGroup();
      await loadConvs();
    } catch (err) {
      dlgAlert(err.message);
    }
  };

  $("rename-btn").onclick = async () => {
    const name = $("group-rename").value.trim();
    if (!name || !state.activeCid) return;
    try {
      await api("/v1/group-update", {
        method: "POST",
        body: JSON.stringify({ cid: state.activeCid, name }),
      });
      $("group-rename").value = "";
      await refreshGroup();
      await loadConvs();
    } catch (err) {
      dlgAlert(err.message);
    }
  };
  onClick("rename-row", async () => {
    const cur = ($("side-group-name") && $("side-group-name").textContent) || "";
    const name = await dlgPrompt("修改群聊名称", cur, "群聊名称");
    if (name == null || !String(name).trim()) return;
    $("group-rename").value = String(name).trim();
    $("rename-btn").click();
  });
  if ($("member-search")) {
    $("member-search").oninput = () => {
      const q = $("member-search").value.trim().toLowerCase();
      $("member-chips").querySelectorAll(".side-member:not(.add)").forEach((el) => {
        const hit = !q || (el.dataset.name || "").toLowerCase().includes(q) || (el.dataset.uid || "").toLowerCase().includes(q);
        el.classList.toggle("hidden", !hit);
      });
    };
  }

  async function uploadAvatar(file, forGroup) {
    const presign = await api("/v1/media/presign", {
      method: "POST",
      body: JSON.stringify({ filename: file.name, content_type: file.type, size: file.size }),
    });
    await fetch(presign.put_url, { method: "PUT", body: file });
    const done = await api("/v1/media/complete", {
      method: "POST",
      body: JSON.stringify({ object_key: presign.object_key, filename: file.name }),
    });
    const url = done.get_url || done.thumb_url;
    if (forGroup) {
      await api("/v1/group-update", {
        method: "POST",
        body: JSON.stringify({ cid: state.activeCid, avatar_url: url }),
      });
      await refreshGroup();
    } else {
      await api("/v1/me", { method: "POST", body: JSON.stringify({ avatar_url: url }) });
      await loadMe();
      if (state.activeCid) renderMsgs({ stick: false });
    }
  }

  $("group-avatar-btn").onclick = () => $("group-avatar-file").click();
  $("group-avatar-file").onchange = () => {
    const f = $("group-avatar-file").files && $("group-avatar-file").files[0];
    $("group-avatar-file").value = "";
    if (!f) return;
    uploadAvatar(f, true).catch((err) => dlgAlert(err.message));
  };

  $("me").onclick = async () => {
    const name = await dlgPrompt("设置对外显示的名称", nickOf(state.uid) || state.uid, "修改昵称");
    if (name === null) return;
    try {
      await api("/v1/me", { method: "POST", body: JSON.stringify({ display_name: name.trim() }) });
      await loadMe();
      if (state.activeCid) renderMsgs({ stick: false });
    } catch (err) {
      dlgAlert(err.message);
    }
  };

  $("send-form").onsubmit = async (e) => {
    e.preventDefault();
    const text = $("draft").value.trim();
    if (!text || !state.activeCid) return;
    $("draft").value = "";
    localStorage.removeItem(draftKey());
    fitDraft();
    await sendPayload({ type: "TEXT", text });
  };
  if ($("burn-toggle")) $("burn-toggle").onchange = syncBurnUI;

  $("attach-btn").onclick = () => $("file").click();
  if ($("shot-btn")) $("shot-btn").onclick = () => $("file").click();
  if ($("mute-all-btn")) {
    $("mute-all-btn").onclick = async () => {
      if (!state.group) return;
      const cur = !!(state.group.mutedAll || state.group.muted_all);
      try {
        const g = await api("/v1/group-mute-all", {
          method: "POST",
          body: JSON.stringify({ cid: state.activeCid, muted: !cur }),
        });
        state.group = g;
        $("mute-all-btn").textContent = g.mutedAll || g.muted_all ? "解除禁言" : "全员禁言";
      } catch (err) {
        dlgAlert(err.message);
      }
    };
  }
  if ($("tag-btn")) {
    $("tag-btn").onclick = async () => {
      if (!state.activePeer) return;
      const cur = (state.friends.find((f) => friendUid(f) === state.activePeer) || {}).tags || [];
      const raw = await dlgPrompt("多个标签用逗号分隔", cur.join(","), "好友标签");
      if (raw == null) return;
      const tags = raw.split(/[,，]/).map((s) => s.trim()).filter(Boolean);
      try {
        await api("/v1/friend-tags", { method: "POST", body: JSON.stringify({ peer_uid: state.activePeer, tags }) });
        await loadFriends();
        syncSideSwitches();
      } catch (err) {
        dlgAlert(err.message);
      }
    };
  }
  if ($("e2ee-btn")) {
    $("e2ee-btn").onclick = async () => {
      try {
        const peer = peerFromCid(state.activeCid, state.uid) || state.activePeer;
        if (!isGroup(state.activeCid) && (!peer || peer === state.uid)) {
          dlgAlert("当前会话对象无效，请从会话列表重新点开后再开启加密");
          return;
        }
        await e2eePair();
        state.e2eeOn = !state.e2eeOn;
        setSwitch("e2ee-switch", state.e2eeOn);
        toggleHidden("e2ee-fp", !state.e2eeOn);
        if (state.e2eeOn) refreshE2eeFp();
        toast(state.e2eeOn ? "本会话已开启端到端加密（仅文本）" : "已关闭加密");
      } catch (err) {
        dlgAlert(err.message);
      }
    };
  }
  let recMedia = null;
  let recChunks = [];
  if ($("rec-btn")) {
    const recBtn = $("rec-btn");
    recBtn.onpointerdown = async (e) => {
      e.preventDefault();
      if (recBtn.disabled) return;
      try {
        const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
        recChunks = [];
        recMedia = new MediaRecorder(stream);
        recMedia.ondataavailable = (ev) => {
          if (ev.data && ev.data.size) recChunks.push(ev.data);
        };
        recMedia.onstop = async () => {
          stream.getTracks().forEach((t) => t.stop());
          recBtn.classList.remove("rec-on");
          const blob = new Blob(recChunks, { type: recMedia.mimeType || "audio/webm" });
          recMedia = null;
          if (blob.size < 256) return;
          const file = new File([blob], "voice.webm", { type: blob.type || "audio/webm" });
          try {
            await uploadFile(file);
          } catch (err) {
            dlgAlert(err.message);
          }
        };
        recMedia.start();
        recBtn.classList.add("rec-on");
      } catch (err) {
        dlgAlert("无法录音：" + (err.message || err));
      }
    };
    const stopRec = () => {
      if (recMedia && recMedia.state === "recording") recMedia.stop();
    };
    recBtn.onpointerup = stopRec;
    recBtn.onpointerleave = stopRec;
  }
  if ($("global-search")) {
    let gsT = null;
    $("global-search").oninput = () => {
      clearTimeout(gsT);
      gsT = setTimeout(async () => {
        const q = $("global-search").value.trim();
        const box = $("global-hits");
        if (!q) {
          box.classList.add("hidden");
          box.innerHTML = "";
          return;
        }
        try {
          const data = await api("/v1/search?q=" + encodeURIComponent(q));
          const hits = data.hits || [];
          box.classList.remove("hidden");
          box.innerHTML = hits.length
            ? hits
                .map((h) => {
                  const msg = h.message || {};
                  const text = (msg.payload && msg.payload.text) || "";
                  return `<div class="row" data-cid="${h.cid}" data-mid="${escapeHtml(field(msg, "msgId", "msg_id") || "")}" data-seq="${escapeHtml(String(field(msg, "convSeq", "conv_seq") || 0))}"><div class="row-main"><div class="row-title">${escapeHtml(h.title || h.cid)}</div><div class="row-sub">${escapeHtml(text)}</div></div></div>`;
                })
                .join("")
            : `<div class="row"><div class="row-sub">没有匹配的聊天记录</div></div>`;
          box.querySelectorAll(".row[data-cid]").forEach((row) => {
            row.onclick = () => {
              const c = state.convs.find((x) => x.cid === row.dataset.cid);
              jumpToMessage(row.dataset.cid, row.dataset.mid, row.dataset.seq, (c && (c.peerUid || c.peer_uid)) || "");
            };
          });
        } catch (err) {
          toast(err.message);
        }
      }, 250);
    };
  }
  $("file").onchange = () => {
    const f = $("file").files && $("file").files[0];
    $("file").value = "";
    if (!f) return;
    uploadFile(f).catch((err) => {
      $("upload-progress").classList.add("hidden");
      dlgAlert(err.message);
    });
  };
  function takeImageFile(dt) {
    if (!dt || !dt.files) return null;
    for (let i = 0; i < dt.files.length; i++) {
      if ((dt.files[i].type || "").indexOf("image/") === 0) return dt.files[i];
    }
    return dt.files[0] || null;
  }
  document.addEventListener("paste", (e) => {
    if (!state.activeCid || $("draft").disabled) return;
    const f = takeImageFile(e.clipboardData);
    if (!f) return;
    e.preventDefault();
    uploadFile(f).catch((err) => dlgAlert(err.message));
  });
  const dropTarget = document.querySelector(".chat-main") || $("msgs");
  if (dropTarget) {
    dropTarget.addEventListener("dragover", (e) => {
      e.preventDefault();
    });
    dropTarget.addEventListener("drop", (e) => {
      e.preventDefault();
      if (!state.activeCid || $("draft").disabled) return;
      const f = takeImageFile(e.dataTransfer);
      if (f) uploadFile(f).catch((err) => dlgAlert(err.message));
    });
  }
  async function refreshE2eeFp() {
    const el = $("e2ee-fp");
    if (!el || isGroup(state.activeCid) || !state.e2eeOn || !state.activePeer) {
      toggleHidden("e2ee-fp", true);
      return;
    }
    try {
      const mine = await e2eePair();
      const peer = await api("/v1/e2ee/keys?uids=" + encodeURIComponent(state.activePeer));
      const p = (peer.users || [])[0] || {};
      const raw = [mine.pub || "", p.publicKey || p.public_key || ""].sort().join("|");
      const buf = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(raw));
      const hex = Array.from(new Uint8Array(buf))
        .map((b) => b.toString(16).padStart(2, "0"))
        .join("")
        .slice(0, 32);
      el.textContent = "密钥指纹 " + hex.replace(/(.{4})/g, "$1 ").trim();
      toggleHidden("e2ee-fp", false);
    } catch (_) {
      toggleHidden("e2ee-fp", true);
    }
  }
  onClick("announce-row", async () => {
    const cur = (state.group && state.group.announcement) || "";
    if (!isGroupManager()) {
      dlgAlert(cur || "未设置", "群公告");
      return;
    }
    const next = await dlgPrompt("群公告", cur, "编辑群公告");
    if (next === null) return;
    try {
      await api("/v1/group-update", { method: "POST", body: JSON.stringify({ cid: state.activeCid, announcement: next }) });
      await refreshGroup();
    } catch (err) {
      dlgAlert(err.message);
    }
  });
  onClick("mynick-row", async () => {
    const cur = ($("side-my-nick") && $("side-my-nick").textContent) || "";
    const next = await dlgPrompt("我在本群的昵称", cur, "群昵称");
    if (next === null) return;
    try {
      await api("/v1/group-member", { method: "POST", body: JSON.stringify({ cid: state.activeCid, uid: state.uid, nickname: next }) });
      await refreshGroup();
    } catch (err) {
      dlgAlert(err.message);
    }
  });
  onClick("join-approval-btn", async () => {
    if (!state.group) return;
    const next = !(state.group.joinApproval || state.group.join_approval);
    try {
      await api("/v1/group-update", { method: "POST", body: JSON.stringify({ cid: state.activeCid, join_approval: next }) });
      await refreshGroup();
    } catch (err) {
      dlgAlert(err.message);
    }
  });
  onClick("clear-btn", async () => {
    if (!state.activeCid) return;
    if (!(await dlgConfirm("清空后可从云端再拉历史。确定清空？", "清空聊天记录", true))) return;
    try {
      await api("/v1/conversation-clear", { method: "POST", body: JSON.stringify({ cid: state.activeCid }) });
      state.messages = [];
      renderMsgs();
      toast("已清空");
    } catch (err) {
      dlgAlert(err.message);
    }
  });
  onClick("qr-card-btn", () => {
    $("qr-card-img").src = "/v1/me/qr.png?t=" + Date.now();
    $("qr-card-box").classList.remove("hidden");
  });
  onClick("qr-card-close", () => $("qr-card-box").classList.add("hidden"));
  onClick("readers-ok", () => $("readers-box").classList.add("hidden"));
  onClick("devices-ok", () => $("devices-box").classList.add("hidden"));
  onClick("devices-btn", async () => {
    try {
      const data = await api("/v1/devices");
      const list = data.devices || [];
      $("devices-list").innerHTML = list.length
        ? list
            .map((d) => `<div class="row"><div class="row-main"><div class="row-title">${escapeHtml(d.device_id || d.conn_id)}</div><div class="row-sub">${d.self === "1" ? "本机" : d.gateway_id || ""}</div></div>${d.self === "1" ? "" : `<div class="row-actions"><button type="button" class="danger" data-id="${escapeHtml(d.conn_id)}">下线</button></div>`}</div>`)
            .join("")
        : `<div class="row-sub">暂无其他设备</div>`;
      $("devices-list").querySelectorAll("button[data-id]").forEach((btn) => {
        btn.onclick = async () => {
          try {
            await api("/v1/devices", { method: "POST", body: JSON.stringify({ conn_id: btn.dataset.id }) });
            toast("已踢下线");
            $("devices-btn").click();
          } catch (err) {
            dlgAlert(err.message);
          }
        };
      });
      $("devices-box").classList.remove("hidden");
    } catch (err) {
      dlgAlert(err.message);
    }
  });
  onClick("select-cancel", () => exitSelect());
  onClick("select-fwd", () => {
    const ids = Object.keys(state.selected);
    if (!ids.length) return;
    openForward(ids);
  });
      $("mute-btn").onclick = async () => {
    if (!state.activeCid) return;
    const next = !isMuted(state.activeCid);
    try {
      await api("/v1/mute", { method: "POST", body: JSON.stringify({ cid: state.activeCid, muted: next }) });
      if (next) state.muted[state.activeCid] = true;
      else delete state.muted[state.activeCid];
      setSwitch("mute-switch", next);
      await loadConvs();
    } catch (err) {
      dlgAlert(err.message);
    }
  };
  $("pin-btn").onclick = async () => {
    if (!state.activeCid) return;
    const next = !isPinned(state.activeCid);
    try {
      await api("/v1/pin", { method: "POST", body: JSON.stringify({ cid: state.activeCid, pinned: next }) });
      if (next) state.pins[state.activeCid] = true;
      else delete state.pins[state.activeCid];
      setSwitch("pin-switch", next);
      await loadConvs();
    } catch (err) {
      dlgAlert(err.message);
    }
  };
  $("hide-btn").onclick = async () => {
    if (!state.activeCid) return;
    try {
      await api("/v1/conversation-hide", { method: "POST", body: JSON.stringify({ cid: state.activeCid }) });
      state.activeCid = "";
      state.activePeer = "";
      state.messages = [];
      $("chat-title").textContent = "选择一个会话";
      $("chat-sub").textContent = "";
      $("chat-search-bar").classList.add("hidden");
      $("group-bar").classList.add("hidden");
      toggleHidden("side-shared", true);
      toggleHidden("side-direct", true);
      toggleHidden("side-group-admin", true);
      setChatSide(false);
      toggleHidden("chat-search-toggle", true);
      toggleHidden("hide-btn", true);
      toggleHidden("pin-btn", true);
      toggleHidden("mute-btn", true);
      toggleHidden("remark-btn", true);
      toggleHidden("tag-btn", true);
      toggleHidden("e2ee-btn", true);
      toggleHidden("block-btn", true);
      setComposerEnabled(false);
      renderMsgs();
      await loadConvs();
    } catch (err) {
      dlgAlert(err.message);
    }
  };
  onClick("remark-btn", async () => {
    if (!state.activePeer) return;
    const cur = await dlgPrompt("给好友设置备注名", nickOf(state.activePeer) || state.activePeer, "设置备注");
    if (cur === null) return;
    try {
      await api("/v1/remark", { method: "POST", body: JSON.stringify({ peer_uid: state.activePeer, remark: cur.trim() }) });
      await loadFriends();
      await loadConvs();
      const conv = state.convs.find((c) => c.cid === state.activeCid);
      if (conv) $("chat-title").textContent = convTitle(conv);
      renderSidePeer();
      syncSideSwitches();
      renderMsgs({ stick: false });
    } catch (err) {
      dlgAlert(err.message);
    }
  });
  $("block-btn").onclick = async () => {
    if (!state.activePeer) return;
    const blocked = isBlocked(state.activePeer);
    if (!blocked && !(await dlgConfirm("拉黑后双方无法发消息。", "拉黑 " + nickOf(state.activePeer) + "？", true))) return;
    await setBlocked(state.activePeer, !blocked);
    if ($("block-btn")) $("block-btn").textContent = isBlocked(state.activePeer) ? "移出黑名单" : "加入黑名单";
    lockComposer();
    updateChatHeader();
  };
  $("leave-btn").onclick = async () => {
    if (!state.activeCid) return;
    const owner = state.group && (state.group.ownerUid || state.group.owner_uid);
    if (owner === state.uid) {
      dlgAlert("请先把群主转让给其他成员。", "无法退群");
      return;
    }
    if (!(await dlgConfirm("退出后将不再接收此群消息。", "退出群聊", true))) return;
    try {
      await api("/v1/group-leave", { method: "POST", body: JSON.stringify({ cid: state.activeCid }) });
      $("group-bar").classList.add("hidden");
      toggleHidden("side-shared", true);
      toggleHidden("side-group-admin", true);
      state.activeCid = "";
      state.messages = [];
      $("chat-title").textContent = "选择一个会话";
      $("chat-sub").textContent = "";
      setChatSide(false);
      setComposerEnabled(false);
      renderMsgs();
      await loadConvs();
    } catch (err) {
      dlgAlert(err.message);
    }
  };
  $("transfer-btn").onclick = async () => {
    if (!state.activeCid || !state.group) return;
    const members = (state.group.members || []).filter((m) => m.uid !== state.uid).map((m) => m.uid);
    const uid = await dlgPrompt(members.length ? "可选成员：" + members.join("、") : "暂无其他成员", members[0] || "", "转让群主");
    if (!uid) return;
    try {
      await api("/v1/group-transfer", { method: "POST", body: JSON.stringify({ cid: state.activeCid, uid: uid.trim() }) });
      await refreshGroup();
    } catch (err) {
      dlgAlert(err.message);
    }
  };
  onClick("dissolve-btn", async () => {
    if (!state.activeCid) return;
    if (!(await dlgConfirm("解散后群聊将不可恢复。", "解散群聊", true))) return;
    try {
      await api("/v1/group-dissolve", { method: "POST", body: JSON.stringify({ cid: state.activeCid }) });
      $("group-bar").classList.add("hidden");
      toggleHidden("side-shared", true);
      toggleHidden("side-group-admin", true);
      state.activeCid = "";
      state.messages = [];
      $("chat-title").textContent = "选择一个会话";
      $("chat-sub").textContent = "";
      setChatSide(false);
      setComposerEnabled(false);
      renderMsgs();
      await loadConvs();
    } catch (err) {
      dlgAlert(err.message);
    }
  });
  $("quote-clear").onclick = () => {
    state.quote = null;
    $("quote-bar").classList.add("hidden");
  };

  // Whole graphemes only. String.prototype.split("") cuts supplementary-plane
  // emoji into lone UTF-16 surrogates; JSON.stringify then emits \ud83d and
  // protojson fails with: invalid escape code "\",\"men" in string.
  const EMOJIS = ["😀","😃","😄","😁","😆","😅","😂","🤣","😊","😇","🙂","😉","😍","😘","😋","😜","🤔","😐","🙄","😏","😮","😪","😴","😌","😓","😔","🙃","😢","😭","😤","😡","🤯","😳","😷","🥳","🥺","👍","👎","👏","🙏","💪","❤️","🔥","⭐","🎉","✅","❌"];
  function wellFormed(s) {
    if (typeof s !== "string") return s;
    if (typeof s.toWellFormed === "function") return s.toWellFormed();
    let out = "";
    for (let i = 0; i < s.length; i++) {
      const c = s.charCodeAt(i);
      if (c >= 0xd800 && c <= 0xdbff) {
        const d = s.charCodeAt(i + 1);
        if (d >= 0xdc00 && d <= 0xdfff) {
          out += s[i] + s[i + 1];
          i++;
        } else out += "\uFFFD";
      } else if (c >= 0xdc00 && c <= 0xdfff) out += "\uFFFD";
      else out += s[i];
    }
    return out;
  }
  function insertDraft(text) {
    const el = $("draft");
    const start = el.selectionStart || el.value.length;
    const end = el.selectionEnd || start;
    el.value = el.value.slice(0, start) + text + el.value.slice(end);
    el.focus();
    el.selectionStart = el.selectionEnd = start + text.length;
    fitDraft();
    saveDraft();
  }
  const KAWAII = ["(≧▽≦)", "(´・ω・`)", "(づ｡◕‿‿◕｡)づ", "(•̀ᴗ•́)و", "(╯°□°)╯", "¯\\_(ツ)_/¯", "(ง •̀_•́)ง", "(≧ω≦)"];
  function renderEmojiBox() {
    const box = $("emoji-box");
    if (!box) return;
    box.innerHTML = "";
    const tabs = document.createElement("div");
    tabs.className = "emoji-tabs";
    ["表情", "颜文字", "贴纸"].forEach((name, i) => {
      const t = document.createElement("button");
      t.type = "button";
      t.textContent = name;
      t.onclick = (e) => {
        e.stopPropagation();
        fillEmojiPack(box, i);
      };
      tabs.appendChild(t);
    });
    box.appendChild(tabs);
    const inner = document.createElement("div");
    inner.id = "emoji-inner";
    box.appendChild(inner);
    fillEmojiPack(box, 0);
  }
  function fillEmojiPack(box, idx) {
    let inner = box.querySelector("#emoji-inner");
    if (!inner) return;
    inner.innerHTML = "";
    inner.className = idx === 1 ? "kaomoji" : idx === 2 ? "stickers" : "";
    inner.id = "emoji-inner";
    if (idx === 0) {
      EMOJIS.forEach((e) => {
        const btn = document.createElement("button");
        btn.type = "button";
        btn.textContent = e;
        btn.onclick = () => insertDraft(e);
        inner.appendChild(btn);
      });
      return;
    }
    if (idx === 1) {
      KAWAII.forEach((e) => {
        const btn = document.createElement("button");
        btn.type = "button";
        btn.textContent = e;
        btn.onclick = () => insertDraft(e);
        inner.appendChild(btn);
      });
      return;
    }
    api("/v1/stickers")
      .then((data) => {
        (data.stickers || []).forEach((st) => {
          const btn = document.createElement("button");
          btn.type = "button";
          const img = document.createElement("img");
          img.src = st.url;
          img.alt = "sticker";
          img.style.width = "40px";
          img.style.height = "40px";
          btn.appendChild(img);
          btn.onclick = () =>
            sendPayload({
              type: "IMAGE",
              stickerId: st.id,
              media: { objectKey: "sticker/" + st.id, url: st.url, filename: "sticker" },
            });
          inner.appendChild(btn);
        });
        const add = document.createElement("button");
        add.type = "button";
        add.textContent = "+";
        add.onclick = () => {
          const inp = document.createElement("input");
          inp.type = "file";
          inp.accept = "image/*";
          inp.onchange = async () => {
            const f = inp.files && inp.files[0];
            if (!f) return;
            try {
              const presign = await api("/v1/media/presign", {
                method: "POST",
                body: JSON.stringify({ filename: f.name, content_type: f.type, size: f.size }),
              });
              await fetch(presign.put_url, { method: "PUT", body: f });
              const done = await api("/v1/media/complete", {
                method: "POST",
                body: JSON.stringify({ object_key: presign.object_key, filename: f.name }),
              });
              await api("/v1/stickers", { method: "POST", body: JSON.stringify({ url: done.get_url || done.thumb_url, pack: "mine" }) });
              fillEmojiPack(box, 2);
              toast("已收藏贴纸");
            } catch (err) {
              dlgAlert(err.message);
            }
          };
          inp.click();
        };
        inner.appendChild(add);
      })
      .catch(() => {});
  }
  $("emoji-btn").onclick = (e) => {
    e.stopPropagation();
    renderEmojiBox();
    $("emoji-box").classList.toggle("hidden");
  };
  document.addEventListener("click", (e) => {
    const box = $("emoji-box");
    if (box && !box.classList.contains("hidden") && !e.target.closest("#emoji-btn") && !box.contains(e.target)) {
      box.classList.add("hidden");
    }
    const menu = $("msg-menu");
    if (menu && !menu.classList.contains("hidden") && !menu.contains(e.target)) hideMsgMenu();
  });

  function openLightbox(src) {
    if (!src) return;
    $("lightbox-img").src = src;
    $("lightbox").classList.remove("hidden");
  }
  $("lightbox").onclick = () => $("lightbox").classList.add("hidden");

  function openForward(msgIdOrIds) {
    const ids = Array.isArray(msgIdOrIds) ? msgIdOrIds : [msgIdOrIds];
    const srcs = ids.map((id) => state.messages.find((m) => field(m, "msgId", "msg_id") === id)).filter((m) => m && m.payload);
    if (!srcs.length) return;
    state.forwarding = srcs;
    const list = $("forward-list");
    list.innerHTML = state.convs
      .filter((c) => c.cid !== state.activeCid)
      .map((c) => {
        const peer = field(c, "peerUid", "peer_uid") || "";
        return `<div class="row" data-cid="${c.cid}" data-peer="${peer}">${avatarHTML(convAvatar(c), convTitle(c), isGroup(c.cid) ? "" : peer)}<div class="row-main"><div class="row-title">${escapeHtml(convTitle(c))}</div></div></div>`;
      })
      .join("");
    if (!list.innerHTML) list.innerHTML = `<div class="row"><div class="row-sub">没有可转发的会话</div></div>`;
    list.querySelectorAll(".row[data-cid]").forEach((row) => {
      row.onclick = async () => {
        $("forward-box").classList.add("hidden");
        try {
          for (const src of srcs) {
            const payload = JSON.parse(JSON.stringify(src.payload));
            await sendPayload(payload, { cid: row.dataset.cid, peerUid: row.dataset.peer, forward: true });
          }
          toast("已转发");
          exitSelect();
        } catch (err) {
          dlgAlert(err.message);
        }
        state.forwarding = null;
      };
    });
    $("forward-box").classList.remove("hidden");
  }
  $("forward-cancel").onclick = () => {
    state.forwarding = null;
    $("forward-box").classList.add("hidden");
  };
  $("forward-box").onclick = (e) => {
    if (e.target === $("forward-box")) $("forward-cancel").click();
  };

  let chatSearchTimer = 0;
  $("chat-search").addEventListener("input", () => {
    const q = $("chat-search").value.trim();
    clearTimeout(chatSearchTimer);
    chatSearchTimer = setTimeout(async () => {
      if (!q) {
        state.searchQ = "";
        return;
      }
      const hit = state.messages.find((m) => ((m.payload && m.payload.text) || "").indexOf(q) >= 0);
      if (hit) {
        highlightMsg(field(hit, "msgId", "msg_id"));
        return;
      }
      try {
        const data = await api("/v1/timeline?cid=" + encodeURIComponent(state.activeCid) + "&q=" + encodeURIComponent(q) + "&limit=5");
        const m = (data.messages || [])[0];
        if (m) jumpToMessage(state.activeCid, field(m, "msgId", "msg_id"), field(m, "convSeq", "conv_seq"), state.activePeer);
        else toast("没有匹配的聊天记录");
      } catch (err) {
        toast(err.message);
      }
    }, 250);
  });
  $("msgs").addEventListener("scroll", () => {
    if ($("msgs").scrollTop < 48) loadOlder();
  });
  $("qr-approve").onclick = () => {
    api("/v1/auth/qr/approve", { method: "POST", body: JSON.stringify({ ticket: state.pendingTicket }) })
      .then(() => {
        $("confirm-qr").classList.add("hidden");
        history.replaceState({}, "", "/");
        state.pendingTicket = "";
      })
      .catch((e) => dlgAlert(e.message));
  };
  $("qr-deny").onclick = () => {
    $("confirm-qr").classList.add("hidden");
    history.replaceState({}, "", "/");
    state.pendingTicket = "";
  };

  $("draft").addEventListener("input", () => {
    fitDraft();
    saveDraft();
    if (!state.activeCid) return;
    const now = Date.now();
    if (now - state.lastTypingAt >= 2000) {
      state.lastTypingAt = now;
      try {
        sendFrame({ typing: { cid: state.activeCid } });
      } catch (_) {}
    }
    const box = $("mention-box");
    if (!isGroup(state.activeCid) || !state.group) {
      box.classList.add("hidden");
      return;
    }
    const val = $("draft").value;
    const at = val.lastIndexOf("@");
    if (at < 0 || /\s/.test(val.slice(at))) {
      box.classList.add("hidden");
      return;
    }
    const q = val.slice(at + 1).toLowerCase();
    const members = (state.group.members || []).filter((m) => {
      const id = uidOf(m);
      if (!id || id === state.uid) return false;
      const name = memberNick(id).toLowerCase();
      return !q || id.toLowerCase().indexOf(q) === 0 || name.indexOf(q) >= 0;
    });
    const rows = [];
    if (isGroupManager() && (!q || "所有人".indexOf(q) === 0)) {
      rows.push(`<div class="row" data-uid="所有人"><div class="row-title">所有人</div></div>`);
    }
    members.forEach((m) => {
      const id = uidOf(m);
      rows.push(`<div class="row" data-uid="${escapeHtml(id)}">${escapeHtml(memberNick(id))}<span class="row-sub"> ${escapeHtml(id)}</span></div>`);
    });
    if (!rows.length) {
      box.classList.add("hidden");
      return;
    }
    box.innerHTML = rows.join("");
    box.classList.remove("hidden");
    box.querySelectorAll(".row").forEach((row) => {
      row.onclick = () => {
        $("draft").value = val.slice(0, at) + "@" + row.dataset.uid + " ";
        box.classList.add("hidden");
        $("draft").focus();
      };
    });
  });

  $("draft").addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      $("send-form").requestSubmit();
    }
  });

  const oauthRaw = localStorage.getItem("surge:oauth");
  if (oauthRaw) {
    localStorage.removeItem("surge:oauth");
    try {
      const data = JSON.parse(oauthRaw);
      if (data.access_token && data.uid) {
        sessionEnter(data.uid, data.access_token, data.refresh_token).catch((e) => dlgAlert(e.message));
      }
    } catch (_) {}
  }

  const params = new URLSearchParams(location.search);
  state.pendingTicket = params.get("ticket") || "";
  state.addPeer = params.get("add") || "";

  const savedUid = sessionStorage.getItem("surge_uid");
  const savedTok = sessionStorage.getItem("surge_token");
  const savedRefresh = sessionStorage.getItem("surge_refresh") || "";
  if (savedUid && savedTok) {
    state.uid = savedUid;
    state.token = savedTok;
    state.refresh = savedRefresh;
    showMe();
    loadMuted();
    loadPins();
    $("login").classList.add("hidden");
    $("app").classList.remove("hidden");
    if (window.Notification && Notification.permission === "default") {
      Notification.requestPermission().catch(() => {});
    }
    kvGet(savedUid + ":seq").then((v) => {
      state.lastSyncSeq = Number(v || 0);
    });
    kvGet(savedUid + ":outbox").then((v) => {
      state.outbox = (v || []).filter((m) => !m.dead);
    });
    loadMe()
      .then(loadFriends)
      .then(loadRequests)
      .then(loadBlocks)
      .then(loadConvs)
      .then(() => {
        startPresencePoll();
        startWSElection();
        maybeApproveTicket();
        e2eePair().catch(() => {});
        maybeAddFriendFromURL();
      })
      .catch((e) => dlgAlert(e.message));
  } else {
    startQR();
  }
})();
