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
    pins: {},
    refresh: "",
    isLeader: false,
    tabCh: null,
    tabId: "",
    leaderAt: 0,
    online: {},
    requests: { incoming: [], outgoing: [] },
    blocks: [],
    hasMore: false,
    loadingMore: false,
    searchQ: "",
    readInfo: null,
    presenceTimer: null,
    forwarding: null,
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
      body: JSON.stringify({ refresh_token: state.refresh, device_id: "web" }),
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

  function friendlyHttp(status, text) {
    const t = String(text || "");
    if (status === 429 || /too many/i.test(t)) return "操作过于频繁，请稍后再试";
    if (/blocked/i.test(t)) return "已拉黑，无法发送";
    return t || "请求失败";
  }

  function toast(msg) {
    const el = $("toast");
    if (!el) {
      alert(msg);
      return;
    }
    el.textContent = msg;
    el.classList.remove("hidden");
    clearTimeout(state.toastTimer);
    state.toastTimer = setTimeout(() => el.classList.add("hidden"), 2600);
  }

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
    return typeof f === "string" ? f : f.uid || "";
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

  function avatarHTML(url, name, uid) {
    const letter = String(name || uid || "?").slice(0, 1).toUpperCase();
    const face = url
      ? `<img class="avatar" src="${escapeHtml(url)}" alt="" />`
      : `<span class="avatar letter">${escapeHtml(letter)}</span>`;
    const on = uid && state.online[uid] ? " on" : "";
    return `<div class="avatar-wrap">${face}<span class="presence${on}"></span></div>`;
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
    if (on) fitDraft();
  }

  function fitDraft() {
    const el = $("draft");
    if (!el) return;
    el.style.height = "36px";
    el.style.height = Math.min(el.scrollHeight, 72) + "px";
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
        const badge = c.unread && Number(c.unread) > 0 ? `<span class="badge">${c.unread}</span>` : "";
        const peer = field(c, "peerUid", "peer_uid") || "";
        const title = convTitle(c);
        const uid = isGroup(c.cid) ? "" : peer;
        return `<div class="row${active}${pinCls}" data-peer="${peer}" data-cid="${c.cid}">
          ${avatarHTML(convAvatar(c), title, uid)}
          <div class="row-main"><div class="row-title">${escapeHtml(title)}</div><div class="row-sub">${escapeHtml(c.lastText || c.last_text || "")}</div></div>${badge}
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
          <div class="row-main"><div class="row-title">${escapeHtml(name)}</div><div class="row-sub">${escapeHtml(uid)}</div></div>
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
  }

  function canRecall(m) {
    const mine = field(m, "fromUid", "from_uid") === state.uid;
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
    return html.replace(/@([A-Za-z0-9._@+-]{1,64})/g, '<span class="mention">@$1</span>');
  }

  function quoteBlock(m) {
    const qtext = (m.payload && (m.payload.quoteText || m.payload.quote_text)) || "";
    const qid = field(m, "quoteMsgId", "quote_msg_id");
    if (!qtext && !qid) return "";
    return `<div class="quote-card">${escapeHtml(qtext || "引用消息")}</div>`;
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

  function lastMineSeq() {
    let seq = 0;
    for (const m of state.messages) {
      if (field(m, "fromUid", "from_uid") !== state.uid) continue;
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
        const mine = field(m, "fromUid", "from_uid") === state.uid;
        const recalled = m.recalled || (m.payload && (m.payload.type === "RECALL" || m.payload.type === 2));
        if (isSystemMsg(m) && !recalled) {
          return `<div class="msg-row system"><div class="bubble system">${escapeHtml((m.payload && m.payload.text) || "")}</div></div>`;
        }
        const st = m.status ? " " + m.status : "";
        const recCls = recalled ? " recalled" : "";
        const id = field(m, "msgId", "msg_id") || "";
        const from = field(m, "fromUid", "from_uid") || "";
        const seq = Number(field(m, "convSeq", "conv_seq") || 0);
        const who = group && !mine ? `<div class="meta">${escapeHtml(from)}</div>` : "";
        const quote = quoteBlock(m);
        const body = recalled ? "已撤回一条消息" : renderBody(m) + (recalled ? "" : linkCard(m));
        const recallBtn = canRecall(m)
          ? `<button type="button" class="recall-btn ok" data-id="${id}">撤回</button>`
          : "";
        const fwdBtn =
          !recalled && id
            ? `<button type="button" class="act-btn" data-act="fwd" data-id="${id}">转发</button>`
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
          seq &&
          state.readInfo &&
          Number(state.readInfo.seq) === seq &&
          state.readInfo.count > 0
            ? `<div class="read-mark">${state.readInfo.count} 人已读</div>`
            : "";
        return `<div class="msg-row${mine ? " me" : " peer"}"><div class="bubble${mine ? " me" : " peer"}${st}${recCls}" data-id="${id}">${who}${quote}${recalled ? escapeHtml(body) : body}${fwdBtn}${recallBtn}${read}${gRead}</div></div>`;
      })
      .join("");
    box.querySelectorAll(".recall-btn").forEach((btn) => {
      btn.onclick = (e) => {
        e.stopPropagation();
        recall(btn.dataset.id);
      };
    });
    box.querySelectorAll("[data-act=fwd]").forEach((btn) => {
      btn.onclick = (e) => {
        e.stopPropagation();
        openForward(btn.dataset.id);
      };
    });
    box.querySelectorAll("img.thumb").forEach((img) => {
      img.onclick = (e) => {
        e.stopPropagation();
        openLightbox(img.dataset.full || img.src);
      };
    });
    box.querySelectorAll(".bubble:not(.system)").forEach((el) => {
      el.ondblclick = () => {
        if (!el.dataset.id) return;
        state.quote = { id: el.dataset.id, preview: el.textContent.slice(0, 80) };
        $("quote-text").textContent = "引用：" + state.quote.preview;
        $("quote-bar").classList.remove("hidden");
      };
    });
    if (stick) box.scrollTop = box.scrollHeight;
    else box.scrollTop = box.scrollHeight - prevH + prevTop;
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
  }

  async function loadFriends() {
    const data = await api("/v1/friends");
    state.friends = data.friends || [];
    renderLists();
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
      alert(err.message);
    }
  }

  async function removeFriend(peer) {
    if (!peer || !confirm("删除好友 " + peer + "？")) return;
    try {
      await api("/v1/friends", { method: "DELETE", body: JSON.stringify({ peer_uid: peer }) });
      await loadFriends();
      await loadConvs();
    } catch (err) {
      alert(err.message);
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
      alert(err.message);
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
    if (state.group) (state.group.members || []).forEach((m) => m.uid && uids.add(m.uid));
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
    const seq = lastMineSeq();
    if (!seq) return;
    try {
      const data = await api(
        "/v1/read-state?cid=" + encodeURIComponent(state.activeCid) + "&seq=" + seq
      );
      const count = Number(field(data, "readCount", "read_count") || 0);
      const prev = state.readInfo;
      state.readInfo = { seq, count, readers: data.readerUids || data.reader_uids || [] };
      if (!prev || prev.seq !== seq || prev.count !== count) {
        if (count > 0 || (prev && prev.count > 0)) renderMsgs({ stick: false });
      }
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
      const isOwner = owner === state.uid;
      $("transfer-btn").classList.toggle("hidden", !isOwner);
      $("dissolve-btn").classList.toggle("hidden", !isOwner);
      let names = {};
      try {
        const pr = await api("/v1/profiles?uids=" + encodeURIComponent(members.map((m) => m.uid).join(",")));
        (pr.users || []).forEach((u) => {
          names[u.uid] = field(u, "displayName", "display_name") || u.uid;
        });
      } catch (_) {}
      $("member-chips").innerHTML = members
        .map((m) => {
          const kick = isOwner && m.uid !== state.uid ? " kick" : "";
          const label = names[m.uid] || m.uid;
          return `<span class="chip${kick}" data-uid="${m.uid}">${escapeHtml(label)}${m.role === "owner" ? " ·群主" : ""}</span>`;
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
    state.hasMore = false;
    state.searchQ = "";
    state.readInfo = null;
    if ($("chat-search")) $("chat-search").value = "";
    $("chat-search-bar").classList.remove("hidden");
    const conv = state.convs.find((c) => c.cid === cid);
    if (isGroup(cid)) {
      $("chat-title").textContent = (conv && conv.title) || "群聊";
      $("chat-sub").textContent = cid;
    } else {
      $("chat-title").textContent = (conv && convTitle(conv)) || peer;
      updateChatHeader();
    }
    $("mute-btn").classList.remove("hidden");
    $("pin-btn").classList.remove("hidden");
    $("hide-btn").classList.remove("hidden");
    $("remark-btn").classList.toggle("hidden", isGroup(cid));
    $("block-btn").classList.toggle("hidden", isGroup(cid));
    $("block-btn").textContent = isBlocked(peer) ? "取消拉黑" : "拉黑";
    $("mute-btn").textContent = isMuted(cid) ? "已免打扰" : "免打扰";
    $("pin-btn").textContent = isPinned(cid) ? "取消置顶" : "置顶";
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
    const q = state.searchQ ? "&q=" + encodeURIComponent(state.searchQ) : "";
    const data = await api("/v1/timeline?cid=" + encodeURIComponent(state.activeCid) + "&limit=50" + q);
    state.messages = data.messages || [];
    state.hasMore = !state.searchQ && !!(data.hasMore || data.has_more);
    const pending = state.outbox.filter((m) => m.cid === state.activeCid && m.status !== "acked");
    if (!state.searchQ) state.messages = state.messages.concat(pending);
    renderMsgs();
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
        state.messages = older.concat(state.messages);
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
      state.tabCh.postMessage({ type: "send", env: env });
      return;
    }
    throw new Error("offline");
  }

  function broadcastFrame(env) {
    if (state.tabCh && state.isLeader) {
      state.tabCh.postMessage({ type: "frame", env: env });
    }
  }

  function startWSElection() {
    state.tabId = uuid();
    if (!("BroadcastChannel" in window)) {
      state.isLeader = true;
      connect();
      return;
    }
    const ch = new BroadcastChannel("surge-ws");
    state.tabCh = ch;
    ch.onmessage = (e) => {
      const m = e.data || {};
      if (m.type === "leader" && m.id && m.id !== state.tabId) {
        state.leaderAt = Date.now();
        if (state.isLeader && String(m.id) < String(state.tabId)) {
          yieldLeader();
        }
      }
      if (m.type === "elect" && state.isLeader) {
        ch.postMessage({ type: "leader", id: state.tabId });
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
    ch.postMessage({ type: "elect", id: state.tabId });
    setTimeout(() => {
      if (Date.now() - state.leaderAt < 600) {
        state.isLeader = false;
        setConn("跟随标签页", true);
        return;
      }
      becomeLeader();
    }, 400);
    setInterval(() => {
      if (state.isLeader) ch.postMessage({ type: "leader", id: state.tabId });
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
    clearInterval(state.hb);
    if (state.ws) {
      state.ws.onclose = null;
      try {
        state.ws.close();
      } catch (_) {}
      state.ws = null;
    }
    setConn("跟随标签页", true);
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
      loadConvs();
      refreshGroupRead();
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
      const item = state.outbox.find((m) => m.clientMsgId === id);
      if (item) item.status = "fail";
      const msg = state.messages.find((m) => m.clientMsgId === id);
      if (msg) msg.status = "fail";
      renderMsgs({ stick: false });
      const code = Number(env.error.code || 0);
      const raw = env.error.message || "";
      if (code === 429 || /too many/i.test(raw)) {
        toast("发送过于频繁，请稍后再试");
        setConn("发送过于频繁", false);
        return;
      }
      if (/blocked/i.test(raw)) {
        toast("已拉黑，无法发送");
        setConn(raw, false);
        return;
      }
      if (raw) setConn(raw, false);
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
        refreshGroupRead();
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
    if (state.ws) {
      state.ws.onclose = null;
      try {
        state.ws.close();
      } catch (_) {}
      state.ws = null;
    }
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
        const env = JSON.parse(e.data);
        onFrame(env);
        broadcastFrame(env);
      } catch (err) {
        console.error(err);
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
      if (m.status === "acked") continue;
      try {
        const send = {
          clientMsgId: m.clientMsgId,
          payload: m.payload && m.payload.type ? m.payload : { type: "TEXT", text: m.text },
        };
        if (send.payload && typeof send.payload.text === "string") send.payload.text = wellFormed(send.payload.text);
        if (m.cid) send.cid = m.cid;
        if (m.peerUid) send.peerUid = m.peerUid;
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
    $("me").textContent = state.uid;
    loadMuted();
    loadPins();
    $("login").classList.add("hidden");
    $("confirm-qr").classList.add("hidden");
    $("app").classList.remove("hidden");
    if (window.Notification && Notification.permission === "default") {
      Notification.requestPermission().catch(() => {});
    }
    state.lastSyncSeq = Number((await kvGet(state.uid + ":seq")) || 0);
    state.outbox = (await kvGet(state.uid + ":outbox")) || [];
    await loadFriends();
    await loadRequests();
    await loadBlocks();
    await loadConvs();
    startPresencePoll();
    startWSElection();
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
    const re = /@([A-Za-z0-9._@+-]{1,64})/g;
    let m;
    while ((m = re.exec(text || ""))) {
      if (out.indexOf(m[1]) < 0) out.push(m[1]);
    }
    return out;
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
    const peerUid = dest && dest.peerUid !== undefined ? dest.peerUid : isGroup(cid) ? "" : state.activePeer;
    if (!isGroup(cid) && isBlocked(peerUid || state.activePeer)) {
      toast("已拉黑，无法发送");
      return;
    }
    if (typeof payload.text === "string") payload.text = wellFormed(payload.text);
    if (!(dest && dest.forward)) {
      payload.mentionUids = mentionUidsOf(payload.text || "");
      payload = await attachLinkPreview(payload);
    }
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
      if (item.peerUid) send.peerUid = item.peerUid;
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
        body: JSON.stringify({ uid, password, device_id: "web" }),
      });
    } else {
      data = await api("/v1/auth/dev-login", {
        method: "POST",
        body: JSON.stringify({ uid, device_id: "web" }),
      });
    }
    await sessionEnter(data.uid, data.access_token, data.refresh_token);
  }

  async function register(uid, password) {
    const data = await api("/v1/auth/register", {
      method: "POST",
      body: JSON.stringify({ uid, password, device_id: "web" }),
    });
    await sessionEnter(data.uid, data.access_token, data.refresh_token);
  }

  $("login-btn").onclick = () => {
    const uid = $("login-uid").value.trim();
    const password = $("login-pass").value;
    if (!uid) return;
    enter(uid, password).catch((e) => alert(e.message));
  };
  $("register-btn").onclick = () => {
    const uid = $("login-uid").value.trim();
    const password = $("login-pass").value;
    if (!uid || !password) {
      alert("注册需要 uid 和至少 6 位密码");
      return;
    }
    register(uid, password).catch((e) => alert(e.message));
  };
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
    btn.onclick = () => {
      state.tab = btn.dataset.tab;
      document.querySelectorAll(".rail-btn[data-tab]").forEach((b) => {
        const on = b === btn;
        b.classList.toggle("bg-white/10", on);
        b.classList.toggle("text-white", on);
        b.classList.toggle("text-zinc-400", !on);
      });
      $("pane-chats").classList.toggle("hidden", state.tab !== "chats");
      $("pane-contacts").classList.toggle("hidden", state.tab !== "contacts");
    };
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
      alert(err.message);
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
      alert(err.message);
    }
  };

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
    }
  }

  $("group-avatar-btn").onclick = () => $("group-avatar-file").click();
  $("group-avatar-file").onchange = () => {
    const f = $("group-avatar-file").files && $("group-avatar-file").files[0];
    $("group-avatar-file").value = "";
    if (!f) return;
    uploadAvatar(f, true).catch((err) => alert(err.message));
  };

  $("me").onclick = async () => {
    const name = prompt("显示名", state.uid);
    if (name === null) return;
    try {
      await api("/v1/me", { method: "POST", body: JSON.stringify({ display_name: name.trim() }) });
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
    fitDraft();
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
  $("mute-btn").onclick = async () => {
    if (!state.activeCid) return;
    const next = !isMuted(state.activeCid);
    try {
      await api("/v1/mute", { method: "POST", body: JSON.stringify({ cid: state.activeCid, muted: next }) });
      if (next) state.muted[state.activeCid] = true;
      else delete state.muted[state.activeCid];
      $("mute-btn").textContent = next ? "已免打扰" : "免打扰";
      await loadConvs();
    } catch (err) {
      alert(err.message);
    }
  };
  $("pin-btn").onclick = async () => {
    if (!state.activeCid) return;
    const next = !isPinned(state.activeCid);
    try {
      await api("/v1/pin", { method: "POST", body: JSON.stringify({ cid: state.activeCid, pinned: next }) });
      if (next) state.pins[state.activeCid] = true;
      else delete state.pins[state.activeCid];
      $("pin-btn").textContent = next ? "取消置顶" : "置顶";
      await loadConvs();
    } catch (err) {
      alert(err.message);
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
      $("hide-btn").classList.add("hidden");
      $("pin-btn").classList.add("hidden");
      $("mute-btn").classList.add("hidden");
      $("remark-btn").classList.add("hidden");
      $("block-btn").classList.add("hidden");
      setComposerEnabled(false);
      renderMsgs();
      await loadConvs();
    } catch (err) {
      alert(err.message);
    }
  };
  $("remark-btn").onclick = async () => {
    if (!state.activePeer) return;
    const cur = prompt("好友备注", state.activePeer);
    if (cur === null) return;
    try {
      await api("/v1/remark", { method: "POST", body: JSON.stringify({ peer_uid: state.activePeer, remark: cur.trim() }) });
      await loadFriends();
      await loadConvs();
      const conv = state.convs.find((c) => c.cid === state.activeCid);
      if (conv) $("chat-title").textContent = convTitle(conv);
    } catch (err) {
      alert(err.message);
    }
  };
  $("block-btn").onclick = async () => {
    if (!state.activePeer) return;
    const blocked = isBlocked(state.activePeer);
    if (!blocked && !confirm("拉黑 " + state.activePeer + "？拉黑后双方无法发消息")) return;
    await setBlocked(state.activePeer, !blocked);
    $("block-btn").textContent = isBlocked(state.activePeer) ? "取消拉黑" : "拉黑";
    lockComposer();
    updateChatHeader();
  };
  $("leave-btn").onclick = async () => {
    if (!state.activeCid) return;
    const owner = state.group && (state.group.ownerUid || state.group.owner_uid);
    if (owner === state.uid) {
      alert("群主退群须先转让群主");
      return;
    }
    if (!confirm("确定退出群聊？")) return;
    try {
      await api("/v1/group-leave", { method: "POST", body: JSON.stringify({ cid: state.activeCid }) });
      $("group-bar").classList.add("hidden");
      state.activeCid = "";
      state.messages = [];
      $("chat-title").textContent = "选择一个会话";
      $("chat-sub").textContent = "";
      setComposerEnabled(false);
      renderMsgs();
      await loadConvs();
    } catch (err) {
      alert(err.message);
    }
  };
  $("transfer-btn").onclick = async () => {
    if (!state.activeCid || !state.group) return;
    const members = (state.group.members || []).filter((m) => m.uid !== state.uid).map((m) => m.uid);
    const uid = prompt("转让给成员 uid\n" + members.join("、"), members[0] || "");
    if (!uid) return;
    try {
      await api("/v1/group-transfer", { method: "POST", body: JSON.stringify({ cid: state.activeCid, uid: uid.trim() }) });
      await refreshGroup();
    } catch (err) {
      alert(err.message);
    }
  };
  $("dissolve-btn").onclick = async () => {
    if (!state.activeCid) return;
    if (!confirm("解散群聊？此操作不可恢复")) return;
    try {
      await api("/v1/group-dissolve", { method: "POST", body: JSON.stringify({ cid: state.activeCid }) });
      $("group-bar").classList.add("hidden");
      state.activeCid = "";
      state.messages = [];
      $("chat-title").textContent = "选择一个会话";
      $("chat-sub").textContent = "";
      setComposerEnabled(false);
      renderMsgs();
      await loadConvs();
    } catch (err) {
      alert(err.message);
    }
  };
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
  function renderEmojiBox() {
    const box = $("emoji-box");
    if (!box || box.dataset.ready) return;
    box.textContent = "";
    EMOJIS.forEach((e) => {
      const btn = document.createElement("button");
      btn.type = "button";
      btn.textContent = e;
      btn.onclick = () => insertDraft(e);
      box.appendChild(btn);
    });
    box.dataset.ready = "1";
  }
  $("emoji-btn").onclick = () => {
    renderEmojiBox();
    $("emoji-box").classList.toggle("hidden");
  };
  document.addEventListener("click", (e) => {
    const box = $("emoji-box");
    if (!box || box.classList.contains("hidden")) return;
    if (e.target === $("emoji-btn") || box.contains(e.target)) return;
    box.classList.add("hidden");
  });

  function openLightbox(src) {
    if (!src) return;
    $("lightbox-img").src = src;
    $("lightbox").classList.remove("hidden");
  }
  $("lightbox").onclick = () => $("lightbox").classList.add("hidden");

  function openForward(msgId) {
    const src = state.messages.find((m) => field(m, "msgId", "msg_id") === msgId);
    if (!src || !src.payload) return;
    state.forwarding = src;
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
        const payload = JSON.parse(JSON.stringify(src.payload));
        $("forward-box").classList.add("hidden");
        try {
          await sendPayload(payload, { cid: row.dataset.cid, peerUid: row.dataset.peer, forward: true });
          toast("已转发");
        } catch (err) {
          alert(err.message);
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

  let chatSearchTimer = 0;
  $("chat-search").addEventListener("input", () => {
    const q = $("chat-search").value.trim();
    clearTimeout(chatSearchTimer);
    chatSearchTimer = setTimeout(async () => {
      state.searchQ = q;
      try {
        await reloadTimeline();
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
      .catch((e) => alert(e.message));
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
    const members = (state.group.members || []).filter((m) => m.uid !== state.uid && m.uid.toLowerCase().indexOf(q) === 0);
    if (!members.length) {
      box.classList.add("hidden");
      return;
    }
    box.innerHTML = members
      .map((m) => `<div class="row" data-uid="${m.uid}">${escapeHtml(m.uid)}</div>`)
      .join("");
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

  const params = new URLSearchParams(location.search);
  state.pendingTicket = params.get("ticket") || "";

  const savedUid = sessionStorage.getItem("surge_uid");
  const savedTok = sessionStorage.getItem("surge_token");
  const savedRefresh = sessionStorage.getItem("surge_refresh") || "";
  if (savedUid && savedTok) {
    state.uid = savedUid;
    state.token = savedTok;
    state.refresh = savedRefresh;
    $("me").textContent = savedUid;
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
      state.outbox = v || [];
    });
    loadFriends()
      .then(loadRequests)
      .then(loadBlocks)
      .then(loadConvs)
      .then(() => {
        startPresencePoll();
        startWSElection();
        maybeApproveTicket();
      })
      .catch((e) => alert(e.message));
  } else {
    startQR();
  }
})();
