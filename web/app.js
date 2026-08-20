(() => {
  const $ = (id) => document.getElementById(id);
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
        return `<div class="row${active}" data-peer="${c.peerUid}" data-cid="${c.cid}">
          <div><div class="row-title">${c.peerUid}</div><div class="row-sub">${c.lastText || ""}</div></div>${badge}
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
        return `<div class="row${active}" data-peer="${uid}"><div class="row-title">${uid}</div></div>`;
      })
      .join("");
    fEl.querySelectorAll(".row").forEach((row) => {
      row.onclick = () => openChat(row.dataset.peer, cidOf(state.uid, row.dataset.peer));
    });
  }

  function renderMsgs() {
    const box = $("msgs");
    box.innerHTML = state.messages
      .map((m) => {
        const mine = m.fromUid === state.uid || m.from_uid === state.uid;
        const st = m.status ? " " + m.status : "";
        const text = (m.payload && m.payload.text) || m.text || "";
        return `<div class="bubble${mine ? " me" : " peer"}${st}">${escapeHtml(text)}</div>`;
      })
      .join("");
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

  async function openChat(peer, cid) {
    state.activePeer = peer;
    state.activeCid = cid;
    $("chat-title").textContent = peer;
    $("chat-sub").textContent = cid;
    $("draft").disabled = false;
    $("send-form").querySelector("button").disabled = false;
    const data = await api("/v1/timeline?cid=" + encodeURIComponent(cid) + "&limit=200");
    state.messages = data.messages || [];
    const pending = state.outbox.filter((m) => m.cid === cid && m.status !== "acked");
    state.messages = state.messages.concat(pending);
    renderLists();
    renderMsgs();
    await loadConvs();
  }

  function sendFrame(body) {
    if (!state.ws || state.ws.readyState !== WebSocket.OPEN) throw new Error("offline");
    const env = Object.assign({ requestId: String(state.reqId++) }, body);
    state.ws.send(JSON.stringify(env));
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
      }
      kvSet(state.uid + ":outbox", state.outbox.filter((m) => m.status !== "acked"));
      const msg = state.messages.find((m) => m.clientMsgId === id);
      if (msg) msg.status = "";
      renderMsgs();
      loadConvs();
      return;
    }
    if (env.push) {
      ingest(env.push);
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
    const from = ev.fromUid || ev.from_uid;
    const seq = Number(ev.syncSeq || ev.sync_seq || 0);
    if (seq > state.lastSyncSeq) state.lastSyncSeq = seq;
    if (cid === state.activeCid) {
      if (!state.messages.some((m) => m.msgId === ev.msgId || m.msg_id === ev.msgId)) {
        state.messages.push({
          msgId: ev.msgId || ev.msg_id,
          fromUid: from,
          payload: ev.payload,
        });
        renderMsgs();
      }
    }
    loadConvs();
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
        sendFrame({
          send: {
            clientMsgId: m.clientMsgId,
            peerUid: m.peerUid,
            payload: { type: "TEXT", text: m.text },
          },
        });
      } catch (_) {
        break;
      }
    }
  }

  async function enter(uid) {
    const data = await api("/v1/auth/dev-login", {
      method: "POST",
      body: JSON.stringify({ uid, device_id: "web" }),
    });
    state.uid = data.uid;
    state.token = data.access_token;
    sessionStorage.setItem("surge_uid", state.uid);
    sessionStorage.setItem("surge_token", state.token);
    $("me").textContent = state.uid;
    $("login").classList.add("hidden");
    $("app").classList.remove("hidden");
    state.lastSyncSeq = Number((await kvGet(state.uid + ":seq")) || 0);
    state.outbox = (await kvGet(state.uid + ":outbox")) || [];
    await loadFriends();
    await loadConvs();
    connect();
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

  $("send-form").onsubmit = async (e) => {
    e.preventDefault();
    const text = $("draft").value.trim();
    if (!text || !state.activePeer) return;
    const item = {
      clientMsgId: uuid(),
      peerUid: state.activePeer,
      cid: state.activeCid,
      fromUid: state.uid,
      text,
      payload: { text },
      status: "pending",
    };
    state.outbox.push(item);
    state.messages.push(item);
    await kvSet(state.uid + ":outbox", state.outbox);
    $("draft").value = "";
    renderMsgs();
    try {
      sendFrame({
        send: {
          clientMsgId: item.clientMsgId,
          peerUid: item.peerUid,
          payload: { type: "TEXT", text },
        },
      });
    } catch (err) {
      item.status = "fail";
      renderMsgs();
    }
  };

  $("draft").addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      $("send-form").requestSubmit();
    }
  });

  const savedUid = sessionStorage.getItem("surge_uid");
  const savedTok = sessionStorage.getItem("surge_token");
  if (savedUid && savedTok) {
    state.uid = savedUid;
    state.token = savedTok;
    $("me").textContent = savedUid;
    $("login").classList.add("hidden");
    $("app").classList.remove("hidden");
    kvGet(savedUid + ":seq").then((v) => {
      state.lastSyncSeq = Number(v || 0);
    });
    kvGet(savedUid + ":outbox").then((v) => {
      state.outbox = v || [];
    });
    loadFriends().then(loadConvs).then(connect).catch((e) => alert(e.message));
  }
})();
