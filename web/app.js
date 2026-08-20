(() => {
  const $ = (id) => document.getElementById(id);
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

  function api(path, opts = {}) {
    const headers = Object.assign({ "Content-Type": "application/json" }, opts.headers || {});
    if (state.token) headers.Authorization = "Bearer " + state.token;
    return fetch(path, Object.assign({}, opts, { headers })).then(async (r) => {
      const text = await r.text();
      if (!r.ok) throw new Error(text || r.statusText);
      return text ? JSON.parse(text) : {};
    });
  }

  function uuid() {
    return crypto.randomUUID ? crypto.randomUUID() : String(Date.now()) + Math.random();
  }

  function cidOf(a, b) {
    return a < b ? "p2p:" + a + ":" + b : "p2p:" + b + ":" + a;
  }

  function isGroup(cid) {
    return String(cid || "").startsWith("grp:");
  }

  function field(obj, camel, snake) {
    if (!obj) return undefined;
    return obj[camel] !== undefined ? obj[camel] : obj[snake];
  }

  function convTitle(c) {
    if (!c) return "";
    if (c.kind === "group" || isGroup(c.cid)) return c.title || "群聊";
    return field(c, "peerUid", "peer_uid") || c.cid;
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
  }

  function setConn(text, on) {
    const el = $("conn-state");
    el.textContent = text;
    el.classList.toggle("on", !!on);
  }

  function renderLists() {
    const convEl = $("conv-list");
    convEl.innerHTML = state.convs
      .map((c) => {
        const active = c.cid === state.activeCid ? " active" : "";
        const badge = c.unread && Number(c.unread) > 0 ? `<span class="badge">${c.unread}</span>` : "";
        const peer = field(c, "peerUid", "peer_uid") || "";
        return `<div class="row${active}" data-peer="${peer}" data-cid="${c.cid}">
          <div><div class="row-title">${escapeHtml(convTitle(c))}</div><div class="row-sub">${escapeHtml(c.lastText || c.last_text || "")}</div></div>${badge}
        </div>`;
      })
      .join("");
    convEl.querySelectorAll(".row").forEach((row) => {
      row.onclick = () => openChat(row.dataset.peer, row.dataset.cid);
    });

    const fEl = $("friend-list");
    fEl.innerHTML = state.friends
      .map((f) => {
        const uid = f.uid || f;
        const active = uid === state.activePeer ? " active" : "";
        return `<div class="row${active}" data-peer="${uid}"><div class="row-title">${escapeHtml(uid)}</div></div>`;
      })
      .join("");
    fEl.querySelectorAll(".row").forEach((row) => {
      row.onclick = () => openChat(row.dataset.peer, cidOf(state.uid, row.dataset.peer));
    });
  }

  function canRecall(m) {
    const mine = field(m, "fromUid", "from_uid") === state.uid;
    const recalled = m.recalled || (m.payload && (m.payload.type === "RECALL" || m.payload.type === 2));
    const created = Number(field(m, "createdAtMs", "created_at_ms") || 0);
    const id = field(m, "msgId", "msg_id");
    return mine && !recalled && id && created && Date.now() - created < RECALL_MS;
  }

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

  function saveMuted() {
    localStorage.setItem(muteKey(), JSON.stringify(state.muted));
  }

  function payloadType(p) {
    if (!p || p.type === undefined || p.type === null) return "";
    return String(p.type);
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

  function linkify(s) {
    return escapeHtml(s).replace(/(https?:\/\/[^\s<]+)/g, '<a href="$1" target="_blank" rel="noopener">$1</a>');
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
      return `<a href="${escapeHtml(href)}" target="_blank"><img class="thumb" src="${escapeHtml(src)}" alt="" /></a>${cap}`;
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

  function lastMineSeq() {
    let seq = 0;
    for (const m of state.messages) {
      if (field(m, "fromUid", "from_uid") !== state.uid) continue;
      const s = Number(field(m, "convSeq", "conv_seq") || 0);
      if (s > seq) seq = s;
    }
    return seq;
  }

  function renderMsgs() {
    const box = $("msgs");
    const lastMine = lastMineSeq();
    box.innerHTML = state.messages
      .map((m) => {
        const mine = field(m, "fromUid", "from_uid") === state.uid;
        const recalled = m.recalled || (m.payload && (m.payload.type === "RECALL" || m.payload.type === 2));
        if (isSystemMsg(m) && !recalled) {
          return `<div class="bubble system">${escapeHtml((m.payload && m.payload.text) || "")}</div>`;
        }
        const st = m.status ? " " + m.status : "";
        const recCls = recalled ? " recalled" : "";
        const id = field(m, "msgId", "msg_id") || "";
        const from = field(m, "fromUid", "from_uid") || "";
        const seq = Number(field(m, "convSeq", "conv_seq") || 0);
        const who = isGroup(state.activeCid) && !mine ? `<div class="meta">${escapeHtml(from)}</div>` : "";
        const qid = field(m, "quoteMsgId", "quote_msg_id");
        const quote = qid ? `<div class="meta">引用 ${escapeHtml(qid.slice(0, 8))}…</div>` : "";
        const body = recalled ? "已撤回一条消息" : renderBody(m);
        const recallBtn = canRecall(m)
          ? `<button type="button" class="recall-btn ok" data-id="${id}">撤回</button>`
          : "";
        const read =
          mine &&
          !isGroup(state.activeCid) &&
          seq &&
          seq === lastMine &&
          state.peerReadSeq >= seq
            ? `<div class="read-mark">已读</div>`
            : "";
        return `<div class="bubble${mine ? " me" : " peer"}${st}${recCls}" data-id="${id}">${who}${quote}${recalled ? escapeHtml(body) : body}${recallBtn}${read}</div>`;
      })
      .join("");
    box.querySelectorAll(".recall-btn").forEach((btn) => {
      btn.onclick = (e) => {
        e.stopPropagation();
        recall(btn.dataset.id);
      };
    });
    box.querySelectorAll(".bubble:not(.system)").forEach((el) => {
      el.ondblclick = () => {
        if (!el.dataset.id) return;
        state.quote = { id: el.dataset.id, preview: el.textContent.slice(0, 40) };
        $("quote-text").textContent = "引用：" + state.quote.preview;
        $("quote-bar").classList.remove("hidden");
      };
    });
    box.scrollTop = box.scrollHeight;
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
    renderLists();
  }

  async function loadFriends() {
    const data = await api("/v1/friends");
    state.friends = data.friends || [];
    renderLists();
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
      state.group = null;
      $("member-chips").innerHTML = "";
      return;
    }
    bar.classList.remove("hidden");
    try {
      const g = await api("/v1/group?cid=" + encodeURIComponent(state.activeCid));
      state.group = g;
      $("chat-title").textContent = g.name || "群聊";
      const members = g.members || [];
      $("chat-sub").textContent = members.map((m) => m.uid).join("、");
      const owner = g.ownerUid || g.owner_uid;
      $("member-chips").innerHTML = members
        .map((m) => {
          const kick = owner === state.uid && m.uid !== state.uid ? " kick" : "";
          return `<span class="chip${kick}" data-uid="${m.uid}">${escapeHtml(m.uid)}${m.role === "owner" ? " ·群主" : ""}</span>`;
        })
        .join("");
      $("member-chips").querySelectorAll(".chip.kick").forEach((el) => {
        el.onclick = async () => {
          if (!confirm("将 " + el.dataset.uid + " 移出群聊？")) return;
          try {
            await api("/v1/group-kick", {
              method: "POST",
              body: JSON.stringify({ cid: state.activeCid, uid: el.dataset.uid }),
            });
            await refreshGroup();
            await loadConvs();
          } catch (err) {
            alert(err.message);
          }
        };
      });
    } catch (err) {
      $("chat-sub").textContent = err.message;
    }
  }

  async function openChat(peer, cid) {
    if (state.activeCid && state.activeCid !== cid) saveDraft();
    state.activePeer = peer || "";
    state.activeCid = cid;
    state.peerReadSeq = 0;
    $("chat-title").textContent = isGroup(cid) ? "群聊" : peer;
    $("chat-sub").textContent = cid;
    $("draft").disabled = false;
    $("send-form").querySelector("button").disabled = false;
    $("attach-btn").disabled = false;
    $("mute-btn").classList.remove("hidden");
    $("mute-btn").textContent = isMuted(cid) ? "已免打扰" : "免打扰";
    loadDraft(cid);
    const data = await api("/v1/timeline?cid=" + encodeURIComponent(cid) + "&limit=200");
    state.messages = data.messages || [];
    const pending = state.outbox.filter((m) => m.cid === cid && m.status !== "acked");
    state.messages = state.messages.concat(pending);
    renderLists();
    renderMsgs();
    await refreshGroup();
    markRead();
    await loadConvs();
  }

  function sendFrame(body) {
    if (!state.ws || state.ws.readyState !== WebSocket.OPEN) throw new Error("offline");
    const env = Object.assign({ requestId: String(state.reqId++) }, body);
    state.ws.send(JSON.stringify(env));
  }

  function applyRecall(cid, msgId) {
    const hit = (m) => field(m, "msgId", "msg_id") === msgId;
    state.messages.filter(hit).forEach((m) => {
      m.recalled = true;
      m.payload = { type: "RECALL", text: "" };
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
    const from = field(ev, "fromUid", "from_uid");
    if (from === state.uid) return;
    if (isMuted(ev.cid)) return;
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
      loadConvs();
      return;
    }
    if (env.push) {
      ingest(env.push);
      return;
    }
    if (env.recalled) {
      applyRecall(env.recalled.cid, env.recalled.msgId || env.recalled.msg_id);
      return;
    }
    if (env.typing) {
      if (env.typing.cid === state.activeCid) showTyping(field(env.typing, "fromUid", "from_uid"));
      return;
    }
    if (env.read) {
      if (env.read.cid === state.activeCid) {
        state.peerReadSeq = Number(env.read.convSeq || env.read.conv_seq || 0);
        renderMsgs();
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
      const item = state.outbox.find((m) => m.clientMsgId === id);
      if (item) item.status = "fail";
      const msg = state.messages.find((m) => m.clientMsgId === id);
      if (msg) msg.status = "fail";
      renderMsgs();
      if (env.error.message) setConn(env.error.message, false);
    }
  }

  function ingest(ev) {
    const cid = ev.cid;
    const from = field(ev, "fromUid", "from_uid");
    const seq = Number(ev.syncSeq || ev.sync_seq || 0);
    const msgId = field(ev, "msgId", "msg_id");
    if (seq > state.lastSyncSeq) state.lastSyncSeq = seq;
    if (cid === state.activeCid) {
      if (!state.messages.some((m) => field(m, "msgId", "msg_id") === msgId)) {
        state.messages.push({
          msgId,
          fromUid: from,
          payload: ev.payload,
          convSeq: Number(ev.convSeq || ev.conv_seq || 0),
          createdAtMs: Number(ev.createdAtMs || ev.created_at_ms || Date.now()),
        });
        renderMsgs();
        markRead();
      }
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
      alert(err.message);
    }
  }

  function connect() {
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(proto + "//" + location.host + "/v1/ws");
    state.ws = ws;
    setConn("连接中…", false);
    ws.onopen = () => {
      setConn("已连接", true);
      sendFrame({ auth: { accessToken: state.token, deviceId: "web" } });
    };
    ws.onmessage = (e) => {
      try {
        onFrame(JSON.parse(e.data));
      } catch (err) {
        console.error(err);
      }
    };
    ws.onclose = () => {
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
      if (m.status === "acked") continue;
      try {
        const send = {
          clientMsgId: m.clientMsgId,
          payload: m.payload && m.payload.type ? m.payload : { type: "TEXT", text: m.text },
        };
        if (m.cid) send.cid = m.cid;
        if (m.peerUid) send.peerUid = m.peerUid;
        if (m.quoteMsgId) send.quoteMsgId = m.quoteMsgId;
        sendFrame({ send });
      } catch (_) {
        break;
      }
    }
  }

  async function sessionEnter(uid, token) {
    state.uid = uid;
    state.token = token;
    sessionStorage.setItem("surge_uid", state.uid);
    sessionStorage.setItem("surge_token", state.token);
    $("me").textContent = state.uid;
    loadMuted();
    $("login").classList.add("hidden");
    $("confirm-qr").classList.add("hidden");
    $("app").classList.remove("hidden");
    if (window.Notification && Notification.permission === "default") {
      Notification.requestPermission().catch(() => {});
    }
    state.lastSyncSeq = Number((await kvGet(state.uid + ":seq")) || 0);
    state.outbox = (await kvGet(state.uid + ":outbox")) || [];
    await loadFriends();
    await loadConvs();
    connect();
    maybeApproveTicket();
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
            await sessionEnter(st.uid, st.access_token);
          }
        } catch (_) {}
      }, 1000);
    } catch (err) {
      $("qr-hint").textContent = "二维码加载失败，可改用上方 uid 登录";
      console.error(err);
    }
  }

  async function sendPayload(payload) {
    if (!state.activeCid) return;
    const item = {
      clientMsgId: uuid(),
      peerUid: isGroup(state.activeCid) ? "" : state.activePeer,
      cid: state.activeCid,
      fromUid: state.uid,
      text: payload.text || "",
      payload,
      quoteMsgId: state.quote ? state.quote.id : "",
      status: "pending",
      createdAtMs: Date.now(),
    };
    state.outbox.push(item);
    state.messages.push(item);
    await kvSet(state.uid + ":outbox", state.outbox);
    try {
      const send = {
        clientMsgId: item.clientMsgId,
        cid: item.cid,
        payload,
      };
      if (item.peerUid) send.peerUid = item.peerUid;
      if (item.quoteMsgId) send.quoteMsgId = item.quoteMsgId;
      sendFrame({ send });
    } catch (err) {
      item.status = "fail";
    }
    state.quote = null;
    $("quote-bar").classList.add("hidden");
    renderMsgs();
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
  }

  async function enter(uid) {
    const data = await api("/v1/auth/dev-login", {
      method: "POST",
      body: JSON.stringify({ uid, device_id: "web" }),
    });
    await sessionEnter(data.uid, data.access_token);
  }

  $("login-btn").onclick = () => {
    const uid = $("login-uid").value.trim();
    if (!uid) return;
    enter(uid).catch((e) => alert(e.message));
  };
  $("login-uid").addEventListener("keydown", (e) => {
    if (e.key === "Enter") $("login-btn").click();
  });
  $("logout").onclick = () => {
    sessionStorage.clear();
    location.reload();
  };

  document.querySelectorAll(".rail-btn[data-tab]").forEach((btn) => {
    btn.onclick = () => {
      state.tab = btn.dataset.tab;
      document.querySelectorAll(".rail-btn[data-tab]").forEach((b) => b.classList.toggle("active", b === btn));
      $("pane-chats").classList.toggle("hidden", state.tab !== "chats");
      $("pane-contacts").classList.toggle("hidden", state.tab !== "contacts");
    };
  });

  $("add-form").onsubmit = async (e) => {
    e.preventDefault();
    const peer = $("add-uid").value.trim();
    if (!peer || peer === state.uid) return;
    try {
      await api("/v1/friends", { method: "POST", body: JSON.stringify({ peer_uid: peer }) });
      $("add-uid").value = "";
      await loadFriends();
    } catch (err) {
      alert(err.message);
    }
  };

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
      alert(err.message);
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
      await refreshGroup();
      await loadConvs();
    } catch (err) {
      alert(err.message);
    }
  };

  $("send-form").onsubmit = async (e) => {
    e.preventDefault();
    const text = $("draft").value.trim();
    if (!text || !state.activeCid) return;
    $("draft").value = "";
    localStorage.removeItem(draftKey());
    await sendPayload({ type: "TEXT", text });
  };

  $("attach-btn").onclick = () => $("file").click();
  $("file").onchange = () => {
    const f = $("file").files && $("file").files[0];
    $("file").value = "";
    if (!f) return;
    uploadFile(f).catch((err) => {
      $("upload-progress").classList.add("hidden");
      alert(err.message);
    });
  };
  $("mute-btn").onclick = () => {
    if (!state.activeCid) return;
    if (isMuted(state.activeCid)) delete state.muted[state.activeCid];
    else state.muted[state.activeCid] = true;
    saveMuted();
    $("mute-btn").textContent = isMuted(state.activeCid) ? "已免打扰" : "免打扰";
  };
  $("quote-clear").onclick = () => {
    state.quote = null;
    $("quote-bar").classList.add("hidden");
  };
  $("qr-approve").onclick = () => {
    api("/v1/auth/qr/approve", { method: "POST", body: JSON.stringify({ ticket: state.pendingTicket }) })
      .then(() => {
        $("confirm-qr").classList.add("hidden");
        history.replaceState({}, "", "/");
        state.pendingTicket = "";
      })
      .catch((e) => alert(e.message));
  };
  $("qr-deny").onclick = () => {
    $("confirm-qr").classList.add("hidden");
    history.replaceState({}, "", "/");
    state.pendingTicket = "";
  };

  $("draft").addEventListener("input", () => {
    saveDraft();
    if (!state.activeCid) return;
    const now = Date.now();
    if (now - state.lastTypingAt < 2000) return;
    state.lastTypingAt = now;
    try {
      sendFrame({ typing: { cid: state.activeCid } });
    } catch (_) {}
  });

  $("draft").addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      $("send-form").requestSubmit();
    }
  });

  const params = new URLSearchParams(location.search);
  state.pendingTicket = params.get("ticket") || "";

  const savedUid = sessionStorage.getItem("surge_uid");
  const savedTok = sessionStorage.getItem("surge_token");
  if (savedUid && savedTok) {
    state.uid = savedUid;
    state.token = savedTok;
    $("me").textContent = savedUid;
    loadMuted();
    $("login").classList.add("hidden");
    $("app").classList.remove("hidden");
    if (window.Notification && Notification.permission === "default") {
      Notification.requestPermission().catch(() => {});
    }
    kvGet(savedUid + ":seq").then((v) => {
      state.lastSyncSeq = Number(v || 0);
    });
    kvGet(savedUid + ":outbox").then((v) => {
      state.outbox = v || [];
    });
    loadFriends()
      .then(loadConvs)
      .then(connect)
      .then(maybeApproveTicket)
      .catch((e) => alert(e.message));
  } else {
    startQR();
  }
})();
