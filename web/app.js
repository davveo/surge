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
  }
  function syncSideSwitches() {
    setSwitch("mute-switch", isMuted(state.activeCid));
    setSwitch("pin-switch", isPinned(state.activeCid));
    setSwitch("e2ee-switch", state.e2eeOn);
    const f = state.friends.find((x) => friendUid(x) === state.activePeer) || {};
    if ($("side-remark-val")) $("side-remark-val").textContent = f.remark || "";
    if ($("side-tag-val")) $("side-tag-val").textContent = visibleTags(f.tags).join("、");
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
    hiddenConvs: [],
    friends: [],
    activePeer: "",
    activeCid: "",
    messages: [],
    outbox: [],
    hb: null,
    group: null,
    groupCache: {},
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
    ensuringChat: false,
    openingChat: false,
    showHidden: false,
    verifyOpen: false,
    verifyPeer: "",
    verifyStep: "",
    verifyReplyDraft: {},
    settings: { notify_sound: true, notify_preview: true, dark: false, wallpaper: "", dnd_start: "", dnd_end: "" },
    favorites: [],
    chatPin: null,
    draftTimer: 0,
    voiceText: "",
    unreadAtOpen: 0,
    jumpUnread: false,
    searchKind: "all",
    mediaKind: "image",
    typingMap: {},
    enterSend: true,
    sendOriginal: false,
  };

  const FILEHELPER = "filehelper";
  const STAR_TAG = "__star__";

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

  const MID_W_KEY = "surge:mid-w";
  const MID_W_MIN = 180;
  const MID_W_MAX = 480;
  const MID_W_DEFAULT = 250;

  function midMaxW() {
    const app = $("app");
    const basis = app && app.clientWidth ? app.clientWidth : window.innerWidth;
    return Math.min(MID_W_MAX, Math.round(basis * 0.4));
  }

  function clampMidW(w) {
    const n = Number(w);
    if (!Number.isFinite(n)) return MID_W_DEFAULT;
    return Math.max(MID_W_MIN, Math.min(midMaxW(), Math.round(n)));
  }

  function applyMidW(w) {
    const app = $("app");
    if (!app) return clampMidW(w);
    const clamped = clampMidW(w);
    app.style.setProperty("--mid-w", clamped + "px");
    const split = $("mid-split");
    if (split) {
      split.setAttribute("aria-valuenow", String(clamped));
      split.setAttribute("aria-valuemin", String(MID_W_MIN));
      split.setAttribute("aria-valuemax", String(midMaxW()));
    }
    return clamped;
  }

  function initMidSplitter() {
    const app = $("app");
    const split = $("mid-split");
    if (!app || !split) return;
    const saved = localStorage.getItem(MID_W_KEY);
    applyMidW(saved == null ? MID_W_DEFAULT : saved);

    let dragging = false;
    let startX = 0;
    let startW = MID_W_DEFAULT;

    function currentMidW() {
      const mid = app.querySelector(".mid");
      return mid ? mid.getBoundingClientRect().width : MID_W_DEFAULT;
    }

    split.addEventListener("pointerdown", (e) => {
      if (e.button !== 0) return;
      e.preventDefault();
      dragging = true;
      startX = e.clientX;
      startW = currentMidW();
      app.classList.add("mid-resizing");
      split.setPointerCapture(e.pointerId);
    });

    split.addEventListener("pointermove", (e) => {
      if (!dragging) return;
      applyMidW(startW + (e.clientX - startX));
    });

    function endDrag(e) {
      if (!dragging) return;
      dragging = false;
      app.classList.remove("mid-resizing");
      try {
        if (e && e.pointerId != null) split.releasePointerCapture(e.pointerId);
      } catch (_) {}
      const w = applyMidW(currentMidW());
      localStorage.setItem(MID_W_KEY, String(w));
    }

    split.addEventListener("pointerup", endDrag);
    split.addEventListener("pointercancel", endDrag);

    function reclamp() {
      const raw = parseFloat(getComputedStyle(app).getPropertyValue("--mid-w"));
      applyMidW(Number.isFinite(raw) ? raw : currentMidW());
    }
    window.addEventListener("resize", reclamp);
    if (typeof ResizeObserver !== "undefined") {
      new ResizeObserver(reclamp).observe(app);
    }
  }

  function isFileHelper(uid) {
    return uid === FILEHELPER;
  }

  function fileHelperCid() {
    return cidOf(state.uid, FILEHELPER);
  }

  function isStarred(f) {
    return ((f && f.tags) || []).indexOf(STAR_TAG) >= 0;
  }

  function visibleTags(tags) {
    return (tags || []).filter((t) => t && t !== STAR_TAG);
  }

  function nameLetter(name) {
    const ch = String(name || "").trim().charAt(0);
    if (!ch) return "#";
    if (/[A-Za-z]/.test(ch)) return ch.toUpperCase();
    if (/[0-9]/.test(ch)) return "#";
    const keys = "阿八嚓哒妸发旮哈讥咔垃痳拏噢妑七呥扨它穵夕丫帀";
    const letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ";
    for (let i = letters.length - 1; i >= 0; i--) {
      if (ch.localeCompare(keys.charAt(i), "zh-CN") >= 0) return letters.charAt(i);
    }
    return "#";
  }

  function enterSendOn() {
    if (state.enterSend === false) return false;
    try {
      const v = localStorage.getItem("surge:enterSend:" + (state.uid || ""));
      if (v === "0") return false;
    } catch (_) {}
    return true;
  }

  function setEnterSend(on) {
    state.enterSend = !!on;
    try {
      localStorage.setItem("surge:enterSend:" + (state.uid || ""), on ? "1" : "0");
    } catch (_) {}
    const hint = $("send-hint");
    if (hint) hint.textContent = on ? "Enter 发送" : "Ctrl+Enter 发送";
    setSwitch("set-enter-send", on);
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
    if (/not group owner/i.test(t)) return "仅群主可操作";
    if (/not group admin/i.test(t)) return "仅群主或管理员可操作";
    if (/blocked/i.test(t)) return "已拉黑，无法发送";
    if (/cannot kick current device/i.test(t)) return "不能下线当前设备";
    return t || "请求失败";
  }

  function friendlyRpc(raw) {
    const t = String(raw || "");
    if (/unsupported payload type/i.test(t)) return "消息格式异常，已停止重发";
    if (/empty text/i.test(t)) return "不能发送空消息";
    if (/add friend first/i.test(t)) return "请先加好友再发消息";
    if (/not friends/i.test(t)) return "请先加好友再发消息";
    if (/user blocked|blocked/i.test(t)) return "已拉黑，无法发送";
    if (/group muted/i.test(t)) return "全员禁言中，无法发送";
    if (/not group owner/i.test(t)) return "仅群主可操作";
    if (/not group admin/i.test(t)) return "仅群主或管理员可操作";
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

  const modalState = { resolve: null, mode: "alert", multiline: false };
  function promptValue() {
    if (!modalState.multiline) return $("modal-input").value;
    const ta = $("modal-textarea");
    return ta ? ta.value : "";
  }
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
      modalState.multiline = !!(opts.multiline && modalState.mode === "prompt");
      const title = $("modal-title");
      const body = $("modal-body");
      const input = $("modal-input");
      const ta = $("modal-textarea");
      const cancel = $("modal-cancel");
      const ok = $("modal-ok");
      const card = $("modal") && $("modal").querySelector(".modal-card");
      title.textContent = opts.title || (modalState.mode === "prompt" ? "请输入" : modalState.mode === "confirm" ? "确认" : "提示");
      body.textContent = opts.message || "";
      body.classList.toggle("hidden", !opts.message);
      if (card) card.classList.toggle("modal-wide", modalState.multiline);
      if (modalState.mode === "prompt" && modalState.multiline && ta) {
        input.classList.add("hidden");
        input.value = "";
        ta.classList.remove("hidden");
        ta.value = opts.value == null ? "" : String(opts.value);
        ta.placeholder = opts.placeholder || "";
      } else if (modalState.mode === "prompt") {
        if (ta) {
          ta.classList.add("hidden");
          ta.value = "";
        }
        input.classList.remove("hidden");
        input.value = opts.value == null ? "" : String(opts.value);
        input.placeholder = opts.placeholder || "";
      } else {
        input.classList.add("hidden");
        input.value = "";
        if (ta) {
          ta.classList.add("hidden");
          ta.value = "";
        }
      }
      cancel.classList.toggle("hidden", modalState.mode === "alert");
      ok.textContent = opts.ok || (modalState.mode === "alert" ? "知道了" : "确定");
      ok.className = opts.danger ? "btn-danger" : "btn-primary";
      $("modal").classList.remove("hidden");
      setTimeout(() => {
        if (modalState.mode === "prompt" && modalState.multiline && ta) {
          ta.focus();
          ta.setSelectionRange(ta.value.length, ta.value.length);
        } else if (modalState.mode === "prompt") input.focus();
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
  function dlgPromptArea(message, value, title) {
    return dlgOpen({
      mode: "prompt",
      message,
      value,
      title,
      multiline: true,
      placeholder: "输入群公告，留空可清空",
    });
  }
  $("modal-ok").onclick = () => {
    if (modalState.mode === "confirm") closeDialog(true);
    else if (modalState.mode === "prompt") closeDialog(promptValue());
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
  if ($("modal-textarea")) {
    $("modal-textarea").addEventListener("keydown", (e) => {
      if ((e.ctrlKey || e.metaKey) && e.key === "Enter") {
        e.preventDefault();
        $("modal-ok").click();
      }
    });
  }
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
    const peer = field(c, "peerUid", "peer_uid") || "";
    if (isFileHelper(peer)) return "文件传输助手";
    return field(p, "displayName", "display_name") || peer || c.title || c.cid;
  }

  function convAvatar(c) {
    if (!c) return "";
    const fromConv = field(c, "avatarUrl", "avatar_url") || "";
    const fromPeer = field(peerProfile(c), "avatarUrl", "avatar_url") || "";
    if (!isGroup(c.cid)) return fromConv || fromPeer || "";
    const cached = (state.groupCache && state.groupCache[c.cid]) ||
      (state.group && state.group.cid === c.cid ? state.group : null);
    const fromGroup = cached ? (field(cached, "avatarUrl", "avatar_url") || "") : "";
    return fromConv || fromPeer || fromGroup || "";
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
    if (isFileHelper(uid)) return "文件传输助手";
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

  function isGroupOwner() {
    const g = state.group;
    if (!g) return false;
    if ((g.ownerUid || g.owner_uid) === state.uid) return true;
    const me = (g.members || []).find((m) => uidOf(m) === state.uid);
    return !!(me && (me.role === "owner" || me.Role === "owner"));
  }

  function isGroupManager() {
    const g = state.group;
    if (!g) return false;
    if (isGroupOwner()) return true;
    const me = (g.members || []).find((m) => uidOf(m) === state.uid);
    return !!(me && (me.role === "admin" || me.Role === "admin"));
  }

  function groupModeOf(g) {
    const raw = String((g && (field(g, "mode") || g.Mode)) || "").toLowerCase();
    return raw || "normal";
  }

  function convGroupMode(c) {
    if (!c || !isGroup(c.cid)) return "normal";
    if (state.group && (field(state.group, "cid") || "") === c.cid) {
      const m = groupModeOf(state.group);
      if (m) return m;
    }
    const cached = state.groupCache && state.groupCache[c.cid];
    if (cached) return groupModeOf(cached);
    const email = String(field(peerProfile(c), "email") || "");
    if (email.indexOf("grp:") === 0) return email.slice(4) || "normal";
    return "normal";
  }

  function activeGroupMode() {
    const g = activeGroup();
    if (g) return groupModeOf(g);
    const c = (state.convs || []).find((x) => x.cid === state.activeCid);
    return convGroupMode(c);
  }

  function groupModeLabel(mode) {
    switch (mode) {
      case "verify":
        return "验证群";
      case "private":
        return "私密群";
      case "broadcast":
        return "广播群";
      case "anonymous":
        return "匿名群";
      case "ephemeral":
        return "阅后即焚群";
      default:
        return "";
    }
  }

  function anonNick(uid) {
    const s = String(state.activeCid || "") + "|" + String(uid || "");
    let h = 2166136261;
    for (let i = 0; i < s.length; i++) {
      h ^= s.charCodeAt(i);
      h = Math.imul(h, 16777619);
    }
    return "匿名" + ((h >>> 0) % 9000 + 1000);
  }

  function isAnonGroup() {
    return isGroup(state.activeCid) && activeGroupMode() === "anonymous";
  }

  function displayMemberNick(uid) {
    if (isAnonGroup()) return anonNick(uid);
    return memberNick(uid);
  }

  function maskGroupText(text) {
    if (!isAnonGroup() || !text) return text;
    let out = String(text);
    ((state.group && state.group.members) || []).forEach((m) => {
      const id = uidOf(m);
      if (id) out = out.split(id).join(anonNick(id));
    });
    return out;
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
    if (!state.uid || !state.activeCid || !$("draft")) return;
    const text = $("draft").value;
    localStorage.setItem(draftKey(), text);
    clearTimeout(state.draftTimer);
    state.draftTimer = setTimeout(() => {
      api("/v1/drafts", { method: "POST", body: JSON.stringify({ cid: state.activeCid, text }) }).catch(() => {});
    }, 600);
  }

  function loadDraft(cid) {
    const conv = (state.convs || []).find((c) => c.cid === cid);
    const cloud = conv && (conv.draftText || conv.draft_text);
    const local = localStorage.getItem(draftKey(cid)) || "";
    $("draft").value = cloud || local;
    fitDraft();
  }

  function setConn(text, on) {
    const el = $("conn-state");
    if (!el) return;
    el.textContent = text;
    el.title = text;
    el.classList.toggle("on", !!on);
  }

  function setComposerEnabled(on) {
    $("draft").disabled = !on;
    $("send-btn").disabled = !on;
    $("attach-btn").disabled = !on;
    if ($("emoji-btn")) $("emoji-btn").disabled = !on;
    if ($("shot-btn")) $("shot-btn").disabled = !on;
    if ($("rec-btn")) $("rec-btn").disabled = !on;
    if ($("burn-toggle")) $("burn-toggle").disabled = !on;
    const form = $("send-form");
    if (form) form.classList.toggle("composer-locked", !on);
    if (on) {
      fitDraft();
      syncBurnUI();
    }
  }

  function burnMapKey() {
    return "surge:burn:" + state.uid;
  }
  function loadBurnMap() {
    try {
      return JSON.parse(localStorage.getItem(burnMapKey()) || "{}") || {};
    } catch (_) {
      return {};
    }
  }
  function burnForced(cid) {
    cid = cid || state.activeCid;
    return isGroup(cid) && (cid === state.activeCid ? activeGroupMode() : convGroupMode((state.convs || []).find((c) => c.cid === cid))) === "ephemeral";
  }
  function isBurnWanted(cid) {
    cid = cid || state.activeCid;
    if (!cid) return false;
    if (burnForced(cid)) return true;
    return !!loadBurnMap()[cid];
  }
  function setBurnWanted(cid, on) {
    if (!cid || burnForced(cid)) return;
    const map = loadBurnMap();
    if (on) map[cid] = true;
    else delete map[cid];
    localStorage.setItem(burnMapKey(), JSON.stringify(map));
  }

  function isEphemeral(m) {
    const p = m && m.payload;
    return !!(p && (p.ephemeral || p.Ephemeral));
  }

  function syncBurnUI() {
    const box = $("burn-toggle");
    const lab = $("burn-label");
    const cid = state.activeCid;
    const forced = burnForced(cid);
    if (box) {
      box.checked = isBurnWanted(cid);
      box.disabled = forced || !!speakBlockedReason() || !cid;
    }
    const on = isBurnWanted(cid);
    if (lab) lab.classList.toggle("burn-on", on);
    const draft = $("draft");
    if (draft && !speakBlockedReason()) draft.placeholder = on ? "阅后即焚消息…" : "";
  }

  function fitDraft() {
    const el = $("draft");
    if (!el) return;
    el.style.height = "";
  }

  function hiddenKey() {
    return "surge:hidden:" + state.uid;
  }
  function loadHiddenConvs() {
    if (!state.uid) {
      state.hiddenConvs = [];
      return;
    }
    try {
      state.hiddenConvs = JSON.parse(localStorage.getItem(hiddenKey()) || "[]") || [];
    } catch (_) {
      state.hiddenConvs = [];
    }
  }
  function saveHiddenConvs() {
    if (!state.uid) return;
    localStorage.setItem(hiddenKey(), JSON.stringify(state.hiddenConvs || []));
  }
  function rememberHidden(conv) {
    if (!conv || !conv.cid) return;
    const rest = (state.hiddenConvs || []).filter((h) => h.cid !== conv.cid);
    state.hiddenConvs = [conv].concat(rest);
    saveHiddenConvs();
  }
  function forgetHidden(cid) {
    const next = (state.hiddenConvs || []).filter((h) => h.cid !== cid);
    if (next.length === (state.hiddenConvs || []).length) return;
    state.hiddenConvs = next;
    saveHiddenConvs();
  }
  function pruneHiddenConvs() {
    const visible = {};
    (state.convs || []).forEach((c) => {
      if (c && c.cid) visible[c.cid] = true;
    });
    const next = (state.hiddenConvs || []).filter((h) => h && h.cid && !visible[h.cid]);
    if (next.length !== (state.hiddenConvs || []).length) {
      state.hiddenConvs = next;
      saveHiddenConvs();
    }
  }

  function sortedConvs() {
    return state.convs.slice().sort((a, b) => {
      const ha = isFileHelper(field(a, "peerUid", "peer_uid")) ? 1 : 0;
      const hb = isFileHelper(field(b, "peerUid", "peer_uid")) ? 1 : 0;
      if (ha !== hb) return hb - ha;
      const pa = isPinned(a.cid) ? 1 : 0;
      const pb = isPinned(b.cid) ? 1 : 0;
      if (pa !== pb) return pb - pa;
      return Number(b.updatedAtMs || b.updated_at_ms || 0) - Number(a.updatedAtMs || a.updated_at_ms || 0);
    });
  }

  function renderLists() {
    const convEl = $("conv-list");
    const sorted = sortedConvs();
    convEl.innerHTML = sorted
      .map((c) => {
        const active = c.cid === state.activeCid ? " active" : "";
        const pinCls = isPinned(c.cid) ? " pinned" : "";
        const muted = isMuted(c.cid);
        const unreadN = Number(c.unread || 0);
        const mentionN = Number(c.unreadMention || c.unread_mention || 0);
        const badge =
          unreadN > 0
            ? `<span class="badge${muted ? " muted-badge" : ""}${mentionN > 0 ? " mention" : ""}">${unreadN > 99 ? "99+" : unreadN}</span>`
            : mentionN > 0
              ? `<span class="badge mention">@</span>`
              : "";
        const peer = field(c, "peerUid", "peer_uid") || "";
        const title = convTitle(c);
        const modeLab = groupModeLabel(convGroupMode(c));
        const uid = isGroup(c.cid) ? "" : peer;
        const time = formatConvTime(c.updatedAtMs || c.updated_at_ms);
        const draft = c.draftText || c.draft_text || "";
        const sub = mentionN > 0
          ? `<span class="mention-flag">[有人@我]</span> ${escapeHtml(c.lastText || c.last_text || "")}`
          : draft
            ? `[草稿] ${escapeHtml(draft)}`
            : escapeHtml(c.lastText || c.last_text || "");
        return `<div class="row${active}${pinCls}${muted ? " muted" : ""}" data-peer="${peer}" data-cid="${c.cid}">
          ${avatarHTML(convAvatar(c), title, uid, badge)}
          <div class="row-main"><div class="row-head"><div class="row-title">${escapeHtml(title)}${modeLab ? `<span class="mode-tag">${escapeHtml(modeLab)}</span>` : ""}</div><div class="row-time">${escapeHtml(time)}</div></div><div class="row-sub">${sub}</div></div>
        </div>`;
      })
      .join("");
    pruneHiddenConvs();
    const hidden = state.hiddenConvs || [];
    let hiddenHTML = "";
    if (hidden.length) {
      hiddenHTML =
        `<div class="row hidden-toggle" data-act="toggle-hidden">
          <div class="row-main"><div class="row-title">隐藏的会话</div><div class="row-sub">${hidden.length} 个 · ${state.showHidden ? "点击收起" : "点击查看并恢复"}</div></div>
        </div>`;
      if (state.showHidden) {
        hiddenHTML += hidden
          .map((c) => {
            const peer = field(c, "peerUid", "peer_uid") || "";
            const title = convTitle(c) || c.title || peer || "会话";
            const uid = isGroup(c.cid) ? "" : peer;
            return `<div class="row dim" data-peer="${escapeHtml(peer)}" data-cid="${escapeHtml(c.cid)}">
          ${avatarHTML(convAvatar(c), title, uid)}
          <div class="row-main"><div class="row-head"><div class="row-title">${escapeHtml(title)}</div></div><div class="row-sub">点击恢复显示</div></div>
        </div>`;
          })
          .join("");
      }
    }
    convEl.innerHTML = convEl.innerHTML + hiddenHTML;
    convEl.querySelectorAll(".row").forEach((row) => {
      if (row.dataset.act === "toggle-hidden") {
        row.onclick = (e) => {
          e.stopPropagation();
          state.showHidden = !state.showHidden;
          renderLists();
        };
        return;
      }
      row.onclick = () => openChat(row.dataset.peer, row.dataset.cid);
    });

    const fEl = $("friend-list");
    const helperRow = `<div class="row" data-peer="${FILEHELPER}" data-helper="1">
          ${avatarHTML("", "文件传输助手", FILEHELPER)}
          <div class="row-main"><div class="row-title">文件传输助手</div><div class="row-sub">发给自己</div></div>
        </div>`;
    const realFriends = state.friends.filter((f) => !isFileHelper(friendUid(f)));
    const starred = realFriends.filter(isStarred).sort((a, b) => friendName(a).localeCompare(friendName(b), "zh-CN"));
    const rest = realFriends.filter((f) => !isStarred(f)).sort((a, b) => friendName(a).localeCompare(friendName(b), "zh-CN"));
    const groups = {};
    rest.forEach((f) => {
      const L = nameLetter(friendName(f));
      (groups[L] || (groups[L] = [])).push(f);
    });
    const letters = Object.keys(groups).sort((a, b) => (a === "#" ? 1 : b === "#" ? -1 : a.localeCompare(b)));
    const friendRow = (f) => {
      const uid = friendUid(f);
      const name = friendName(f);
      const active = uid === state.activePeer ? " active" : "";
      const tags = visibleTags(f.tags);
      const star = isStarred(f) ? " on" : "";
      return `<div class="row${active}" data-peer="${uid}">
          ${avatarHTML(friendAvatar(f), name, uid)}
          <div class="row-main"><div class="row-title">${escapeHtml(name)}</div><div class="row-sub">${escapeHtml(uid)}${tags.length ? " · " + escapeHtml(tags.join(" / ")) : ""}</div></div>
          <div class="row-actions"><button type="button" class="star-btn${star}" data-act="star" title="星标">★</button><button type="button" class="danger" data-act="del">删除</button></div>
        </div>`;
    };
    let fHTML = helperRow;
    if (starred.length) {
      fHTML += `<div class="letter-head">星标好友</div>` + starred.map(friendRow).join("");
    }
    letters.forEach((L) => {
      fHTML += `<div class="letter-head">${escapeHtml(L)}</div>` + groups[L].map(friendRow).join("");
    });
    fEl.innerHTML = fHTML;
    fEl.querySelectorAll(".row").forEach((row) => {
      row.onclick = () => openChat(row.dataset.peer, cidOf(state.uid, row.dataset.peer));
      const del = row.querySelector("[data-act=del]");
      if (del) {
        del.onclick = (e) => {
          e.stopPropagation();
          removeFriend(row.dataset.peer);
        };
      }
      const starBtn = row.querySelector("[data-act=star]");
      if (starBtn) {
        starBtn.onclick = (e) => {
          e.stopPropagation();
          toggleStar(row.dataset.peer);
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
    VIDEO: 6,
    CARD: 7,
    MERGE: 8,
    STICKER: 4,
    EMOJI: 4,
    AUDIO: 5,
    VOICE: 5,
  };

  const PAYLOAD_TYPE_NAME = { 1: "TEXT", 3: "SYSTEM", 4: "IMAGE", 5: "FILE", 6: "VIDEO", 7: "CARD", 8: "MERGE" };

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
    if (media.transcript) out.transcript = media.transcript;
    if (media.durationMs || media.duration_ms) out.durationMs = media.durationMs || media.duration_ms;
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
        else if (ct.indexOf("video/") === 0) type = 6;
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
    const cardUid = p.cardUid || p.card_uid || "";
    if (cardUid) out.cardUid = cardUid;
    const mergeItems = p.mergeItems || p.merge_items;
    if (mergeItems && mergeItems.length) out.mergeItems = mergeItems;
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

  function isVideoMsg(m) {
    const t = payloadType(m.payload);
    if (t === "VIDEO" || t === "6") return true;
    if (!isFileMsg(m)) return false;
    const media = mediaOf(m.payload);
    const ct = String(media.contentType || media.content_type || "");
    return ct.indexOf("video/") === 0;
  }

  function isCardMsg(m) {
    const t = payloadType(m.payload);
    return t === "CARD" || t === "7" || !!(m.payload && (m.payload.cardUid || m.payload.card_uid));
  }

  function isMergeMsg(m) {
    const t = payloadType(m.payload);
    if (t === "MERGE" || t === "8") return true;
    const items = (m.payload && (m.payload.mergeItems || m.payload.merge_items)) || [];
    return items.length > 0;
  }

  function renderBody(m) {
    if (isVideoMsg(m)) {
      const media = mediaOf(m.payload);
      const href = media.url || "";
      if (!href) return escapeHtml("[视频]");
      return `<video class="msg-video" controls preload="metadata" src="${escapeHtml(href)}"></video>`;
    }
    if (isCardMsg(m)) {
      const uid = (m.payload && (m.payload.cardUid || m.payload.card_uid)) || "";
      const name = (m.payload && m.payload.text) || uid;
      return `<div class="card-msg" data-card="${escapeHtml(uid)}">${avatarHTML(avatarOf(uid), name, uid)}<div class="c-name">${escapeHtml(name)}</div><div class="c-sub">个人名片</div></div>`;
    }
    if (isMergeMsg(m)) {
      const items = (m.payload && (m.payload.mergeItems || m.payload.merge_items)) || [];
      const lines = items.slice(0, 4).map((it) => {
        const from = it.fromUid || it.from_uid || "";
        const name = nickOf(from) || from;
        return `<div class="m-line">${escapeHtml(name + ": " + (it.text || "[消息]"))}</div>`;
      }).join("");
      return `<div class="merge-msg"><div class="c-name">聊天记录</div>${lines}<div class="m-sub">共 ${items.length} 条</div></div>`;
    }
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
      if (!href) return escapeHtml("[语音]");
      const tr = media.transcript || "";
      const dur = Number(media.durationMs || media.duration_ms || 0);
      const sec = dur > 0 ? Math.max(1, Math.round(dur / 1000)) : 0;
      const bars = Array.from({ length: 16 }, (_, i) => `<i style="height:${8 + ((i * 7) % 12)}px"></i>`).join("");
      return `<div class="voice-bar" data-src="${escapeHtml(href)}" data-id="${escapeHtml(field(m, "msgId", "msg_id") || "")}"><button type="button" class="voice-play" aria-label="播放">▶</button><div class="voice-wave">${bars}</div><span class="voice-dur">${sec ? sec + "″" : ""}</span></div><div class="voice-tools"><button type="button" data-act="stt" data-id="${escapeHtml(field(m, "msgId", "msg_id") || "")}">${tr ? "收起文字" : "转文字"}</button>${tr ? `<div class="stt-text">${escapeHtml(tr)}</div>` : ""}</div>`;
    }
    if (isFileMsg(m)) {
      const media = mediaOf(m.payload);
      const name = media.filename || "文件";
      const href = media.url || "#";
      return `<a class="file-link" href="${escapeHtml(href)}" target="_blank">${escapeHtml(name)}</a>`;
    }
    const text = (m.payload && m.payload.text) || m.text || "";
    const html = linkify(text);
    if (String(text).length > 220) {
      return `<div class="long-msg collapsed">${html}<button type="button" class="fold-toggle">展开</button></div>`;
    }
    return html;
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
    const splitAt = unreadSplitIndex();
    box.innerHTML = state.messages
      .map((m, i) => {
        const mine = senderOf(m) === state.uid;
        const recalled = m.recalled || (m.payload && (m.payload.type === "RECALL" || m.payload.type === 2));
        const burned = recalled && ((m.payload && m.payload.text) === "已销毁");
        const split = i === splitAt ? `<div class="unread-split" id="unread-split">以下为新消息</div>` : "";
        if ((isSystemMsg(m) && !recalled) || recalled) {
          const created = Number(field(m, "createdAtMs", "created_at_ms") || 0);
          const canRe = !burned && mine && m._reeditText && created && Date.now() - created < RECALL_MS;
          const sysText = recalled
            ? burned
              ? "已销毁"
              : mine
                ? "你撤回了一条消息"
                : "对方撤回了一条消息"
            : maskGroupText((m.payload && m.payload.text) || "");
          const reBtn = canRe ? `<button type="button" class="reedit-btn" data-i="${i}">重新编辑</button>` : "";
          return `${split}<div class="msg-row system"><span class="sys-notice">${escapeHtml(sysText)}${reBtn}</span></div>`;
        }
        const st = m.status ? " " + m.status : "";
        const recCls = recalled ? " recalled" : "";
        const id = field(m, "msgId", "msg_id") || "";
        const from = senderOf(m);
        const seq = Number(field(m, "convSeq", "conv_seq") || 0);
        const nick = displayMemberNick(from) || from;
        const who = `<div class="msg-nick" title="${escapeHtml(isAnonGroup() ? nick : from)}">${escapeHtml(nick)}${
          group && from && nick !== from && !isAnonGroup() ? `<span class="msg-uid">${escapeHtml(from)}</span>` : ""
        }</div>`;
        const face = isAnonGroup()
          ? avatarHTML("", nick, nick)
          : avatarHTML(avatarOf(from), nick, from);
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
        const reacts = reactionHTML(m);
        const hover = id && !state.selecting
          ? `<div class="msg-hover" data-id="${escapeHtml(id)}">${hoverToolbarHTML()}</div>`
          : "";
        return `${split}<div class="msg-row${mine ? " me" : " peer"}${group ? " grp" : ""}${reacts ? " has-react" : ""}${state.selecting ? " selecting" : ""}">${check}${mine ? "" : face}<div class="msg-col">${who}<div class="bubble-wrap"><div class="bubble${mine ? " me" : " peer"}${st}${recCls}${eph ? " burn" : ""}${hl}" data-id="${id}" data-seq="${seq}">${failDot}${quote}${recalled ? escapeHtml(body) : body}${burnHint}${read}${gRead}</div>${hover}</div>${reacts}</div>${mine ? face : ""}</div>`;
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
        showMsgMenu(e.clientX, e.clientY, el.dataset.id, el.closest(".msg-row"));
      };
      el.onclick = (e) => {
        if (e.target.closest("a,button,img,.voice-bar,.quote-card,.card-msg")) return;
        if (!window.matchMedia || !window.matchMedia("(hover: none)").matches) return;
        const row = el.closest(".msg-row");
        if (!row) return;
        document.querySelectorAll(".msg-row.hover-on").forEach((r) => {
          if (r !== row) r.classList.remove("hover-on");
        });
        row.classList.toggle("hover-on");
      };
    });
    box.querySelectorAll(".msg-hover button").forEach((btn) => {
      btn.onclick = (e) => {
        e.stopPropagation();
        const id = btn.closest(".msg-hover").dataset.id;
        onHoverAction(btn.dataset.h, id, btn);
      };
    });
    box.querySelectorAll(".react-chip").forEach((el) => {
      el.onclick = (e) => {
        e.stopPropagation();
        toggleReact(el.dataset.id, el.dataset.emoji);
      };
    });
    box.querySelectorAll(".card-msg").forEach((el) => {
      el.onclick = (e) => {
        e.stopPropagation();
        openMemberCard(el.dataset.card);
      };
    });
    box.querySelectorAll("[data-act=stt]").forEach((btn) => {
      btn.onclick = (e) => {
        e.stopPropagation();
        showVoiceText(btn.dataset.id);
      };
    });
    box.querySelectorAll(".reedit-btn").forEach((btn) => {
      btn.onclick = (e) => {
        e.stopPropagation();
        const m = state.messages[Number(btn.dataset.i)];
        const text = (m && m._reeditText) || "";
        if (!text) return;
        $("draft").value = text;
        fitDraft();
        $("draft").focus();
      };
    });
    box.querySelectorAll(".fold-toggle").forEach((btn) => {
      btn.onclick = (e) => {
        e.stopPropagation();
        const wrap = btn.closest(".long-msg");
        if (!wrap) return;
        const open = wrap.classList.contains("collapsed");
        wrap.classList.toggle("collapsed", !open);
        btn.textContent = open ? "收起" : "展开";
      };
    });
    box.querySelectorAll(".voice-bar").forEach((bar) => bindVoiceBar(bar));
    if (stick && state.jumpUnread) {
      const el = $("unread-split");
      if (el) el.scrollIntoView({ block: "center" });
      else box.scrollTop = box.scrollHeight;
      state.jumpUnread = false;
    } else if (stick) box.scrollTop = box.scrollHeight;
    else box.scrollTop = box.scrollHeight - prevH + prevTop;
  }

  function unreadSplitIndex() {
    const n = Number(state.unreadAtOpen || 0);
    if (n <= 0) return -1;
    const incoming = [];
    state.messages.forEach((m, i) => {
      if (senderOf(m) === state.uid) return;
      if (isSystemMsg(m) || m.recalled) return;
      incoming.push(i);
    });
    if (!incoming.length) return -1;
    if (incoming.length < n) return incoming[0];
    return incoming[incoming.length - n];
  }

  function bindVoiceBar(bar) {
    if (!bar || bar._bound) return;
    bar._bound = true;
    const src = bar.dataset.src;
    if (!src) return;
    const audio = new Audio(src);
    audio.preload = "metadata";
    const durEl = bar.querySelector(".voice-dur");
    const playBtn = bar.querySelector(".voice-play");
    audio.addEventListener("loadedmetadata", () => {
      if (durEl && !durEl.textContent && audio.duration && isFinite(audio.duration)) {
        durEl.textContent = Math.max(1, Math.round(audio.duration)) + "″";
      }
    });
    const stop = () => {
      audio.pause();
      audio.currentTime = 0;
      bar.classList.remove("playing");
      if (playBtn) playBtn.textContent = "▶";
    };
    audio.addEventListener("ended", stop);
    bar.onclick = (e) => {
      e.stopPropagation();
      if (bar.classList.contains("playing")) {
        stop();
        return;
      }
      document.querySelectorAll(".voice-bar.playing").forEach((other) => {
        if (other !== bar) other.click();
      });
      audio.play().then(() => {
        bar.classList.add("playing");
        if (playBtn) playBtn.textContent = "❚❚";
      }).catch(() => {});
    };
  }

  function hoverToolbarHTML() {
    return (
      `<button type="button" data-h="like" title="赞">${ico("like")}</button>` +
      `<button type="button" data-h="quote" title="回复">${ico("reply")}</button>` +
      `<button type="button" data-h="fwd" title="转发">${ico("fwd")}</button>` +
      `<button type="button" data-h="copy" title="复制">${ico("copy")}</button>` +
      `<button type="button" data-h="more" title="更多">${ico("more")}</button>`
    );
  }

  function ico(name) {
    const p = {
      like: '<path d="M7 11v9H4.5A1.5 1.5 0 0 1 3 18.5v-6A1.5 1.5 0 0 1 4.5 11H7Zm0 0 3.2-6.2A2 2 0 0 1 12 3.8c.9 0 1.6.7 1.6 1.6V9h4.2a2 2 0 0 1 2 2.3l-1 7a2 2 0 0 1-2 1.7H7"/>',
      reply: '<path d="M20 12a8 8 0 0 1-8 8H7l-4 3v-5.2A8 8 0 1 1 20 12Z"/>',
      fwd: '<path d="M14 7l6 5-6 5V13H4v-2h10V7Z"/>',
      copy: '<rect x="8" y="8" width="11" height="13" rx="1.5"/><path d="M6 16H5a1.5 1.5 0 0 1-1.5-1.5v-11A1.5 1.5 0 0 1 5 2h11A1.5 1.5 0 0 1 17.5 3.5V5"/>',
      more: '<circle cx="6" cy="12" r="1.2" fill="currentColor" stroke="none"/><circle cx="12" cy="12" r="1.2" fill="currentColor" stroke="none"/><circle cx="18" cy="12" r="1.2" fill="currentColor" stroke="none"/>',
      select: '<path d="M8 6h12M8 12h12M8 18h12"/><circle cx="4" cy="6" r="1.2" fill="currentColor" stroke="none"/><circle cx="4" cy="12" r="1.2" fill="currentColor" stroke="none"/><circle cx="4" cy="18" r="1.2" fill="currentColor" stroke="none"/>',
      pin: '<path d="M9 10.5 7 21l5-3 5 3-2-10.5"/><path d="M8 9a4 4 0 1 1 8 0c0 1.5-.8 2.5-2 3.5H10C8.8 11.5 8 10.5 8 9Z"/>',
      star: '<path d="M12 3.5 14.5 9l6 .7-4.4 4 1.2 5.8L12 16.8 6.7 19.5l1.2-5.8L3.5 9.7 9.5 9z"/>',
      recall: '<path d="M4 12a8 8 0 1 0 2.2-5.5M4 4v5h5"/>',
      trash: '<path d="M5 7h14M10 7V5h4v2M7 7l1 13h8l1-13"/>',
      flag: '<path d="M5 21V4h9l-1.2 4L18 8l-1.5 5H5"/>',
    };
    return `<svg viewBox="0 0 24 24" aria-hidden="true">${p[name] || ""}</svg>`;
  }

  function onHoverAction(act, id, btn) {
    if (!id) return;
    const m = state.messages.find((x) => field(x, "msgId", "msg_id") === id);
    const text = (m && m.payload && m.payload.text) || "";
    if (act === "like") {
      toggleReact(id, "👍");
      return;
    }
    if (act === "quote") {
      quoteMsg(id);
      return;
    }
    if (act === "fwd") {
      openForward(id);
      return;
    }
    if (act === "copy") {
      if (!text) {
        toast("这条消息没有可复制的文字");
        return;
      }
      navigator.clipboard.writeText(text).then(() => toast("已复制")).catch(() => {});
      return;
    }
    if (act === "more") {
      const r = btn.getBoundingClientRect();
      showMsgMenu(r.left, r.bottom + 4, id, btn.closest(".msg-row"));
    }
  }

  function quoteMsg(id) {
    if (!id) return;
    const m = state.messages.find((x) => field(x, "msgId", "msg_id") === id);
    const preview = ((m && m.payload && m.payload.text) || "消息").slice(0, 80);
    state.quote = { id, preview };
    $("quote-text").textContent = "引用：" + preview;
    $("quote-bar").classList.remove("hidden");
    const from = m ? senderOf(m) : "";
    if (isGroup(state.activeCid) && from && from !== state.uid && !isAnonGroup()) {
      const at = "@" + from + " ";
      const el = $("draft");
      if (el && el.value.indexOf(at) < 0) {
        el.value = at + el.value;
        fitDraft();
      }
    }
  }

  function hideMsgMenu() {
    const el = $("msg-menu");
    if (el) el.classList.add("hidden");
    document.querySelectorAll(".msg-row.menu-open").forEach((r) => r.classList.remove("menu-open"));
  }

  function showMsgMenu(x, y, msgId, row) {
    const m = state.messages.find((x) => field(x, "msgId", "msg_id") === msgId);
    if (!m) return;
    const recalled = m.recalled;
    const items = [];
    const text = (m.payload && m.payload.text) || "";
    if (!recalled) items.push({ label: "多选", icon: "select", run: () => enterSelect(msgId) });
    if (!recalled) items.push({ label: "置顶消息", icon: "pin", run: () => pinChatMsg(msgId) });
    if (!recalled) items.push({ label: "收藏", icon: "star", run: () => addFavorite(msgId) });
    if (text) items.push({ label: "复制", icon: "copy", run: () => navigator.clipboard.writeText(text).then(() => toast("已复制")).catch(() => {}) });
    if (canRecall(m)) items.push({ label: "撤回", icon: "recall", run: () => recall(msgId) });
    items.push({ label: "删除", icon: "trash", run: () => deleteForMe(msgId) });
    items.push({ sep: true });
    items.push({ label: "举报", icon: "flag", run: () => reportMsg(msgId) });
    const menu = $("msg-menu");
    if (!menu) return;
    document.querySelectorAll(".msg-row.menu-open").forEach((r) => r.classList.remove("menu-open"));
    if (row) row.classList.add("menu-open");
    menu.innerHTML = items
      .map((it, i) => (it.sep ? "<hr />" : `<button type="button" data-i="${i}">${ico(it.icon)}<span>${it.label}</span></button>`))
      .join("");
    menu.querySelectorAll("button").forEach((btn) => {
      btn.onclick = (e) => {
        e.stopPropagation();
        hideMsgMenu();
        const it = items[Number(btn.dataset.i)];
        if (it && it.run) it.run();
      };
    });
    menu.classList.remove("hidden");
    const w = menu.offsetWidth || 180;
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

  const REACT_EMOJIS = ["👍", "❤️", "😂", "😮", "😢", "🙏"];

  function reactionHTML(m) {
    const list = m.reactions || [];
    if (!list.length) return "";
    return `<div class="reacts">${list
      .map((r) => {
        const emoji = r.emoji || "";
        const uids = r.uids || [];
        const mine = uids.indexOf(state.uid) >= 0 ? " mine" : "";
        return `<button type="button" class="react-chip${mine}" data-id="${escapeHtml(field(m, "msgId", "msg_id") || "")}" data-emoji="${escapeHtml(emoji)}">${emoji} ${uids.length}</button>`;
      })
      .join("")}</div>`;
  }

  function showReactPick(x, y, msgId) {
    const el = $("react-pick");
    if (!el) return;
    el.innerHTML = REACT_EMOJIS.map((e) => `<button type="button">${e}</button>`).join("");
    el.querySelectorAll("button").forEach((btn) => {
      btn.onclick = (ev) => {
        ev.stopPropagation();
        el.classList.add("hidden");
        toggleReact(msgId, btn.textContent);
      };
    });
    el.classList.remove("hidden");
    el.style.left = Math.min(x, window.innerWidth - 240) + "px";
    el.style.top = Math.min(y + 8, window.innerHeight - 52) + "px";
    // The click that opened this picker still bubbles to document; ignore it.
    state.reactPickUntil = Date.now() + 300;
  }

  async function toggleReact(msgId, emoji) {
    msgId = String(msgId || "").trim();
    emoji = String(emoji || "").trim();
    if (!msgId || !state.activeCid) {
      toast("消息发送后再回应");
      return;
    }
    try {
      const data = await api("/v1/react", { method: "POST", body: JSON.stringify({ cid: state.activeCid, msg_id: msgId, emoji }) });
      const m = state.messages.find((x) => field(x, "msgId", "msg_id") === msgId);
      if (m) {
        m.reactions = data.reactions || data.Reactions || [];
        renderMsgs({ stick: false });
      }
    } catch (err) {
      toast(err.message || "回应失败");
    }
  }

  async function addFavorite(msgId) {
    try {
      await api("/v1/favorites", { method: "POST", body: JSON.stringify({ cid: state.activeCid, msg_id: msgId }) });
      toast("已收藏");
      loadFavorites();
    } catch (err) {
      dlgAlert(err.message);
    }
  }

  async function loadFavorites(q) {
    try {
      const data = await api("/v1/favorites" + (q ? "?q=" + encodeURIComponent(q) : ""));
      state.favorites = data.favorites || [];
    } catch (_) {
      state.favorites = [];
    }
    const el = $("fav-list");
    if (!el) return;
    if (!state.favorites.length) {
      el.innerHTML = `<div class="row"><div class="row-sub">暂无收藏</div></div>`;
      return;
    }
    el.innerHTML = state.favorites
      .map((f) => {
        const id = f.favId || f.fav_id;
        const from = f.fromUid || f.from_uid || "";
        return `<div class="row" data-id="${escapeHtml(id)}" data-cid="${escapeHtml(f.cid || "")}" data-msg="${escapeHtml(f.msgId || f.msg_id || "")}" data-peer="${escapeHtml(from)}">
          ${avatarHTML(avatarOf(from), nickOf(from) || from, from)}
          <div class="row-main"><div class="row-title">${escapeHtml(f.preview || "[消息]")}</div><div class="row-sub">${escapeHtml(from)}</div></div>
          <div class="row-actions"><button type="button" data-act="fwd">转发</button><button type="button" class="danger" data-act="del">删除</button></div>
        </div>`;
      })
      .join("");
    el.querySelectorAll(".row").forEach((row) => {
      row.onclick = () => {
        if (row.dataset.cid) openChat(row.dataset.peer, row.dataset.cid);
      };
      const fwd = row.querySelector("[data-act=fwd]");
      if (fwd) fwd.onclick = (e) => {
        e.stopPropagation();
        const fav = state.favorites.find((x) => (x.favId || x.fav_id) === row.dataset.id);
        if (!fav || !fav.payload) return;
        openForwardFromPayloads([fav.payload]);
      };
      const del = row.querySelector("[data-act=del]");
      if (del) del.onclick = async (e) => {
        e.stopPropagation();
        try {
          await api("/v1/favorites", { method: "DELETE", body: JSON.stringify({ fav_id: row.dataset.id }) });
          loadFavorites($("fav-search") && $("fav-search").value);
        } catch (err) {
          dlgAlert(err.message);
        }
      };
    });
  }

  function openForwardFromPayloads(payloads) {
    const list = $("forward-list");
    if (!list) return;
    list.innerHTML = state.convs
      .map((c) => {
        const peer = field(c, "peerUid", "peer_uid") || "";
        return `<div class="row" data-cid="${c.cid}" data-peer="${peer}">${avatarHTML(convAvatar(c), convTitle(c), isGroup(c.cid) ? "" : peer)}<div class="row-main"><div class="row-title">${escapeHtml(convTitle(c))}</div></div></div>`;
      })
      .join("");
    list.querySelectorAll(".row[data-cid]").forEach((row) => {
      row.onclick = async () => {
        $("forward-box").classList.add("hidden");
        try {
          for (const p of payloads) {
            await sendPayload(JSON.parse(JSON.stringify(p)), { cid: row.dataset.cid, peerUid: row.dataset.peer, forward: true });
          }
          toast("已转发");
        } catch (err) {
          dlgAlert(err.message);
        }
      };
    });
    $("forward-box").classList.remove("hidden");
  }

  async function pinChatMsg(msgId) {
    try {
      const pin = await api("/v1/pinned", { method: "POST", body: JSON.stringify({ cid: state.activeCid, msg_id: msgId }) });
      state.chatPin = pin;
      renderMsgPin();
      toast("已置顶");
    } catch (err) {
      dlgAlert(err.message);
    }
  }

  function renderMsgPin() {
    const bar = $("msg-pin");
    if (!bar) return;
    const pin = state.chatPin;
    const has = pin && (pin.msgId || pin.msg_id);
    bar.classList.toggle("hidden", !has);
    if (has) $("msg-pin-text").textContent = "置顶：" + (pin.text || pin.Text || "一条消息");
  }

  async function loadPinned(cid) {
    try {
      state.chatPin = await api("/v1/pinned?cid=" + encodeURIComponent(cid));
    } catch (_) {
      state.chatPin = null;
    }
    renderMsgPin();
  }

  async function reportMsg(msgId) {
    const reason = await dlgPrompt("请描述原因", "", "举报");
    if (reason == null) return;
    try {
      await api("/v1/report", { method: "POST", body: JSON.stringify({ cid: state.activeCid, msg_id: msgId, reason }) });
      toast("已提交举报");
    } catch (err) {
      dlgAlert(err.message);
    }
  }

  function showVoiceText() {
    toast("转文字会在按住录音时自动识别；已发出的语音可看录音时的文字");
  }

  async function openMemberCard(uid) {
    if (!uid) return;
    await ensureProfiles([uid]);
    const name = nickOf(uid) || uid;
    const isFriend = (state.friends || []).some((f) => friendUid(f) === uid);
    const body = $("member-card-body");
    if (!body) return;
    body.innerHTML = `${avatarHTML(avatarOf(uid), name, uid)}<div class="verify-name">${escapeHtml(name)}</div><div class="row-sub">${escapeHtml(uid)}</div>
      <div class="modal-actions" style="margin-top:16px">
        ${uid !== state.uid && !isFriend ? `<button type="button" class="btn-primary" id="mc-add">加为好友</button>` : ""}
        ${uid !== state.uid ? `<button type="button" class="btn-ghost" id="mc-msg">发消息</button>` : ""}
        ${uid !== state.uid ? `<button type="button" class="btn-ghost" id="mc-card">发送名片</button>` : ""}
      </div>`;
    $("member-card").classList.remove("hidden");
    if ($("mc-add")) $("mc-add").onclick = () => { $("member-card").classList.add("hidden"); sendFriendRequest(uid, isGroup(state.activeCid) ? "group" : "card"); };
    if ($("mc-msg")) $("mc-msg").onclick = () => { $("member-card").classList.add("hidden"); openChat(uid, cidOf(state.uid, uid)); };
    if ($("mc-card")) $("mc-card").onclick = () => { $("member-card").classList.add("hidden"); sendCard(uid); };
  }

  async function sendCard(uid) {
    uid = uid || state.uid;
    if (!state.activeCid) return;
    await sendPayload({ type: "CARD", cardUid: uid, text: nickOf(uid) || uid });
  }

  function applySettings(st) {
    state.settings = Object.assign({ notify_sound: true, notify_preview: true }, st || {});
    document.documentElement.classList.toggle("dark", !!(state.settings.dark));
    const app = $("app");
    if (app) {
      app.classList.remove("wall-green", "wall-gray", "wall-night");
      const wall = state.settings.wallpaper || "";
      if (wall) app.classList.add("wall-" + wall);
    }
    setSwitch("set-dark", !!state.settings.dark);
    const sound = state.settings.notifySound !== false && state.settings.notify_sound !== false;
    const preview = state.settings.notifyPreview !== false && state.settings.notify_preview !== false;
    setSwitch("set-sound", sound);
    setSwitch("set-preview", preview);
    setEnterSend(enterSendOn());
    if ($("set-wall")) $("set-wall").value = state.settings.wallpaper || "";
    if ($("set-dnd-start")) $("set-dnd-start").value = state.settings.dndStart || state.settings.dnd_start || "";
    if ($("set-dnd-end")) $("set-dnd-end").value = state.settings.dndEnd || state.settings.dnd_end || "";
  }

  function inDnd() {
    const start = state.settings.dndStart || state.settings.dnd_start || "";
    const end = state.settings.dndEnd || state.settings.dnd_end || "";
    if (!start || !end) return false;
    const now = new Date();
    const cur = now.getHours() * 60 + now.getMinutes();
    const toMin = (s) => {
      const p = String(s).split(":");
      return Number(p[0]) * 60 + Number(p[1] || 0);
    };
    const a = toMin(start);
    const b = toMin(end);
    if (a === b) return false;
    if (a < b) return cur >= a && cur < b;
    return cur >= a || cur < b;
  }

  function playNotifySound() {
    if (state.settings.notifySound === false || state.settings.notify_sound === false) return;
    try {
      const ctx = new (window.AudioContext || window.webkitAudioContext)();
      const o = ctx.createOscillator();
      const g = ctx.createGain();
      o.frequency.value = 880;
      g.gain.value = 0.04;
      o.connect(g);
      g.connect(ctx.destination);
      o.start();
      o.stop(ctx.currentTime + 0.12);
    } catch (_) {}
  }

  async function saveSettings() {
    const body = {
      dark: $("set-dark") && $("set-dark").classList.contains("on"),
      wallpaper: ($("set-wall") && $("set-wall").value) || "",
      notify_sound: $("set-sound") && $("set-sound").classList.contains("on"),
      notify_preview: $("set-preview") && $("set-preview").classList.contains("on"),
      dnd_start: ($("set-dnd-start") && $("set-dnd-start").value) || "",
      dnd_end: ($("set-dnd-end") && $("set-dnd-end").value) || "",
    };
    try {
      const st = await api("/v1/settings", { method: "POST", body: JSON.stringify(body) });
      applySettings(st);
    } catch (err) {
      dlgAlert(err.message);
    }
  }

  async function captureScreenshot() {
    if (!navigator.mediaDevices || !navigator.mediaDevices.getDisplayMedia) {
      $("file").click();
      return;
    }
    let stream;
    try {
      stream = await navigator.mediaDevices.getDisplayMedia({ video: true, audio: false });
    } catch (_) {
      $("file").click();
      return;
    }
    const track = stream.getVideoTracks()[0];
    let bmp = null;
    try {
      if (window.ImageCapture) bmp = await new ImageCapture(track).grabFrame();
    } catch (_) {}
    if (!bmp) {
      const v = document.createElement("video");
      v.srcObject = stream;
      v.muted = true;
      await v.play().catch(() => {});
      await new Promise((r) => setTimeout(r, 200));
      const c = document.createElement("canvas");
      c.width = v.videoWidth || 1280;
      c.height = v.videoHeight || 720;
      c.getContext("2d").drawImage(v, 0, 0);
      bmp = c;
      v.pause();
    }
    track.stop();
    stream.getTracks().forEach((t) => t.stop());
    const canvas = $("shot-canvas");
    const overlay = $("shot-overlay");
    if (!bmp || !canvas || !overlay) {
      toast("截图失败，请改用选文件");
      return;
    }
    const w = bmp.width || bmp.videoWidth || 1280;
    const h = bmp.height || bmp.videoHeight || 720;
    canvas.width = w;
    canvas.height = h;
    canvas.getContext("2d").drawImage(bmp, 0, 0);
    overlay.classList.remove("hidden");
    let shotMode = "crop";
    const ctx2 = canvas.getContext("2d");
    ctx2.lineCap = "round";
    ctx2.lineJoin = "round";
    let drawing = false;
    let lastPt = null;
    let arrowFrom = null;
    const canvasPos = (e) => {
      const r = canvas.getBoundingClientRect();
      return { x: (e.clientX - r.left) * canvas.width / r.width, y: (e.clientY - r.top) * canvas.height / r.height };
    };
    const drawArrow = (from, to) => {
      ctx2.strokeStyle = "#e54d42";
      ctx2.fillStyle = "#e54d42";
      ctx2.lineWidth = Math.max(4, canvas.width / 240);
      ctx2.beginPath();
      ctx2.moveTo(from.x, from.y);
      ctx2.lineTo(to.x, to.y);
      ctx2.stroke();
      const ang = Math.atan2(to.y - from.y, to.x - from.x);
      const len = Math.max(16, canvas.width / 50);
      ctx2.beginPath();
      ctx2.moveTo(to.x, to.y);
      ctx2.lineTo(to.x - len * Math.cos(ang - 0.4), to.y - len * Math.sin(ang - 0.4));
      ctx2.lineTo(to.x - len * Math.cos(ang + 0.4), to.y - len * Math.sin(ang + 0.4));
      ctx2.closePath();
      ctx2.fill();
    };
    const setShotMode = (m) => {
      shotMode = m;
      crop.style.display = m === "crop" ? "block" : "none";
    };
    if ($("shot-pen")) $("shot-pen").onclick = () => setShotMode("pen");
    if ($("shot-arrow")) $("shot-arrow").onclick = () => setShotMode("arrow");
    canvas.onmousedown = (e) => {
      if (shotMode === "crop") return;
      const p = canvasPos(e);
      drawing = true;
      lastPt = p;
      arrowFrom = p;
    };
    canvas.onmousemove = (e) => {
      if (!drawing || shotMode !== "pen") return;
      const p = canvasPos(e);
      ctx2.strokeStyle = "rgba(20,20,20,.6)";
      ctx2.lineWidth = Math.max(14, canvas.width / 80);
      ctx2.beginPath();
      ctx2.moveTo(lastPt.x, lastPt.y);
      ctx2.lineTo(p.x, p.y);
      ctx2.stroke();
      lastPt = p;
    };
    const endDraw = (e) => {
      if (!drawing) return;
      drawing = false;
      if (shotMode === "arrow" && arrowFrom) drawArrow(arrowFrom, canvasPos(e));
    };
    canvas.onmouseup = endDraw;
    canvas.onmouseleave = () => { drawing = false; };
    const crop = $("shot-crop");
    const rect = { x: 40, y: 40, w: Math.max(40, Math.min(480, w - 80)), h: Math.max(40, Math.min(280, h - 80)) };
    const syncCrop = () => {
      const r = canvas.getBoundingClientRect();
      const sx = r.width / canvas.width;
      const sy = r.height / canvas.height;
      crop.style.left = rect.x * sx + "px";
      crop.style.top = rect.y * sy + "px";
      crop.style.width = rect.w * sx + "px";
      crop.style.height = rect.h * sy + "px";
    };
    syncCrop();
    $("shot-ok").onclick = async () => {
      const cut = document.createElement("canvas");
      if (shotMode !== "crop") {
        cut.width = canvas.width;
        cut.height = canvas.height;
        cut.getContext("2d").drawImage(canvas, 0, 0);
      } else {
        cut.width = rect.w;
        cut.height = rect.h;
        cut.getContext("2d").drawImage(canvas, rect.x, rect.y, rect.w, rect.h, 0, 0, rect.w, rect.h);
      }
      overlay.classList.add("hidden");
      cut.toBlob(async (blob) => {
        if (!blob) return;
        const file = new File([blob], "screenshot.png", { type: "image/png" });
        try {
          await uploadFile(file);
        } catch (err) {
          dlgAlert(err.message);
        }
      }, "image/png");
    };
    $("shot-cancel").onclick = () => overlay.classList.add("hidden");
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
          m.cid = cid;
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
    injectFileHelperConv();
    state.muted = {};
    state.pins = {};
    state.convs.forEach((c) => {
      if (c.muted) state.muted[c.cid] = true;
      if (c.pinned) state.pins[c.cid] = true;
    });
    pruneHiddenConvs();
    renderLists();
    await catchUpActiveChat();
    await ensureActiveChat();
  }

  async function ensureActiveChat() {
    if (state.ensuringChat || state.openingChat) return;
    const list = sortedConvs();
    if (state.activeCid && list.some((c) => c.cid === state.activeCid)) return;
    if (!list.length) return;
    const first = list[0];
    const peer = field(first, "peerUid", "peer_uid") || "";
    state.ensuringChat = true;
    try {
      await openChat(peer, first.cid);
    } finally {
      state.ensuringChat = false;
    }
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

  async function toggleStar(uid) {
    const f = state.friends.find((x) => friendUid(x) === uid);
    if (!f) return;
    const tags = visibleTags(f.tags).slice();
    if (isStarred(f)) {
      f.tags = tags;
    } else {
      f.tags = tags.concat(STAR_TAG);
    }
    try {
      await api("/v1/friend-tags", { method: "POST", body: JSON.stringify({ peer_uid: uid, tags: f.tags }) });
      renderLists();
    } catch (err) {
      dlgAlert(err.message);
    }
  }

  function injectFileHelperConv() {
    if (!state.uid) return;
    const cid = fileHelperCid();
    const hit = state.convs.find((c) => c.cid === cid || field(c, "peerUid", "peer_uid") === FILEHELPER);
    state.profiles[FILEHELPER] = Object.assign({}, state.profiles[FILEHELPER], { uid: FILEHELPER, displayName: "文件传输助手", display_name: "文件传输助手" });
    if (hit) {
      if (!hit.title) hit.title = "文件传输助手";
      if (!field(hit, "peerUid", "peer_uid")) hit.peerUid = FILEHELPER;
      return;
    }
    state.convs.unshift({
      cid,
      peerUid: FILEHELPER,
      peer_uid: FILEHELPER,
      title: "文件传输助手",
      kind: "p2p",
      unread: 0,
      lastText: "文件传输助手",
      last_text: "文件传输助手",
      updatedAtMs: 1,
    });
  }

  async function openMediaPane() {
    if (!$("media-pane") || !state.activeCid) return;
    $("media-pane").classList.remove("hidden");
    await fillMediaList();
  }

  function msgHasLink(m) {
    const text = (m.payload && m.payload.text) || "";
    return /https?:\/\//.test(text);
  }

  async function fillMediaList() {
    const box = $("media-list");
    if (!box || !state.activeCid) return;
    let msgs = state.messages.slice();
    try {
      const data = await api("/v1/timeline?cid=" + encodeURIComponent(state.activeCid) + "&limit=200");
      if (data.messages) msgs = data.messages;
    } catch (_) {}
    const kind = state.mediaKind || "image";
    if (kind === "image") {
      const imgs = msgs.filter(isImageMsg);
      box.innerHTML = imgs.length
        ? `<div class="media-grid">${imgs.map((m) => {
            const media = mediaOf(m.payload);
            const src = media.thumbUrl || media.thumb_url || media.url || "";
            const href = media.url || src;
            return `<img src="${escapeHtml(src)}" data-full="${escapeHtml(href)}" alt="" />`;
          }).join("")}</div>`
        : `<div class="row-sub">暂无图片</div>`;
      box.querySelectorAll("img").forEach((img) => {
        img.onclick = () => openLightbox(img.dataset.full || img.src);
      });
      return;
    }
    if (kind === "file") {
      const files = msgs.filter((m) => isFileMsg(m) && !isAudioMsg(m) && !isImageMsg(m));
      box.innerHTML = files.length
        ? files.map((m) => {
            const media = mediaOf(m.payload);
            const name = media.filename || "文件";
            const href = media.url || "#";
            return `<a class="media-file" href="${escapeHtml(href)}" target="_blank">${escapeHtml(name)}</a>`;
          }).join("")
        : `<div class="row-sub">暂无文件</div>`;
      return;
    }
    const links = msgs.filter(msgHasLink);
    box.innerHTML = links.length
      ? links.map((m) => {
          const text = (m.payload && m.payload.text) || "";
          const url = (text.match(/https?:\/\/[^\s]+/) || [""])[0];
          return `<a class="media-link" href="${escapeHtml(url)}" target="_blank">${escapeHtml(url)}</a>`;
        }).join("")
      : `<div class="row-sub">暂无链接</div>`;
  }

  async function filterChatByKind(kind) {
    if (!state.activeCid) return;
    try {
      const data = await api("/v1/timeline?cid=" + encodeURIComponent(state.activeCid) + "&limit=200");
      const all = data.messages || [];
      state.messages = all.filter((m) => {
        m.cid = state.activeCid;
        if (kind === "image") return isImageMsg(m);
        return isFileMsg(m) && !isAudioMsg(m);
      });
      renderMsgs({ stick: false });
    } catch (err) {
      toast(err.message);
    }
  }

  async function filterChatByDate(day) {
    const t = Date.parse(day);
    if (!t) {
      toast("日期格式不对");
      return;
    }
    const start = new Date(t);
    start.setHours(0, 0, 0, 0);
    const end = start.getTime() + 86400000;
    try {
      const data = await api("/v1/timeline?cid=" + encodeURIComponent(state.activeCid) + "&limit=200");
      state.messages = (data.messages || []).filter((m) => {
        m.cid = state.activeCid;
        const ms = Number(field(m, "createdAtMs", "created_at_ms") || 0);
        return ms >= start.getTime() && ms < end;
      });
      renderMsgs({ stick: false });
    } catch (err) {
      toast(err.message);
    }
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
    const incoming = state.requests.incoming || [];
    const outgoing = state.requests.outgoing || [];
    const badge = $("req-badge");
    if (badge) {
      const n = incoming.length;
      badge.textContent = n > 99 ? "99+" : String(n);
      badge.classList.toggle("hidden", n === 0);
    }
    const row = $("new-friends-row");
    if (row) row.classList.toggle("active", !!state.verifyOpen);
    if (!state.verifyOpen) return;
    const list = $("verify-list");
    const detail = $("verify-detail");
    const back = $("verify-back");
    const title = $("verify-title");
    if (!list || !detail) return;
    if (state.verifyPeer) {
      list.classList.add("hidden");
      detail.classList.remove("hidden");
      if (back) back.classList.remove("hidden");
      if (title) title.textContent = "朋友验证";
      renderVerifyDetail();
      return;
    }
    list.classList.remove("hidden");
    detail.classList.add("hidden");
    if (back) back.classList.add("hidden");
    if (title) title.textContent = "好友申请";
    if (!incoming.length && !outgoing.length) {
      list.innerHTML = `<div class="verify-empty">暂无好友申请</div>`;
      return;
    }
    list.innerHTML =
      incoming
        .map((r) => {
          const from = field(r, "fromUid", "from_uid");
          const name = nickOf(from) || from;
          return `<div class="row verify-row" data-uid="${escapeHtml(from)}" data-kind="in">
            ${avatarHTML(avatarOf(from), name, from)}
            <div class="row-main"><div class="row-title">${escapeHtml(name)}</div><div class="row-sub">请求加为好友</div></div>
            <div class="row-actions">
              <button type="button" class="btn-pass" data-act="accept">通过</button>
              <button type="button" class="btn-reject" data-act="decline">拒绝</button>
            </div>
          </div>`;
        })
        .join("") +
      outgoing
        .map((r) => {
          const to = field(r, "toUid", "to_uid");
          const name = nickOf(to) || to;
          return `<div class="row verify-row" data-uid="${escapeHtml(to)}" data-kind="out">
            ${avatarHTML(avatarOf(to), name, to)}
            <div class="row-main"><div class="row-title">${escapeHtml(name)}</div><div class="row-sub">等待对方验证</div></div>
          </div>`;
        })
        .join("");
    list.querySelectorAll(".verify-row").forEach((el) => {
      el.onclick = () => openVerifyDetail(el.dataset.uid, el.dataset.kind);
    });
    list.querySelectorAll("[data-act]").forEach((btn) => {
      btn.onclick = (e) => {
        e.stopPropagation();
        handleRequest(btn.closest(".row").dataset.uid, btn.dataset.act);
      };
    });
  }

  function sourceLabel(src) {
    const key = String(src || "").trim();
    if (key === "group" || key === "通过群聊添加") return "通过群聊添加";
    if (key === "qr" || key === "通过扫一扫添加") return "通过扫一扫添加";
    if (key === "card" || key === "通过名片添加") return "通过名片添加";
    if (key === "search" || key === "通过搜索添加") return "通过搜索添加";
    return key || "通过搜索添加";
  }

  function findRequest(uid) {
    const incoming = state.requests.incoming || [];
    const outgoing = state.requests.outgoing || [];
    const hitIn = incoming.find((r) => field(r, "fromUid", "from_uid") === uid);
    if (hitIn) return { row: hitIn, kind: "in" };
    const hitOut = outgoing.find((r) => field(r, "toUid", "to_uid") === uid);
    if (hitOut) return { row: hitOut, kind: "out" };
    return null;
  }

  function renderVerifyDetail() {
    const el = $("verify-detail");
    if (!el) return;
    const uid = state.verifyPeer;
    const found = findRequest(uid);
    if (!found) {
      el.innerHTML = `<div class="verify-empty">该申请已处理</div>`;
      return;
    }
    const name = nickOf(uid) || uid;
    const hello = String(field(found.row, "hello", "Hello") || "").trim() || "请求加为好友";
    const source = sourceLabel(field(found.row, "source", "Source"));
    const verifying = state.verifyStep === "confirm";
    let actions = "";
    if (found.kind === "out") {
      actions = `<div class="verify-wait">等待对方验证</div>`;
    } else if (verifying) {
      actions = `<div class="verify-actions">
        <button type="button" class="btn-pass-lg" data-act="accept">通过</button>
        <button type="button" class="btn-reject-lg" data-act="decline">拒绝</button>
      </div>`;
    } else {
      actions = `<button type="button" class="btn-goto" data-act="goto">前往验证</button>`;
    }
    el.innerHTML = `
      <div class="verify-card">
        <div class="verify-user">
          ${avatarHTML(avatarOf(uid), name, uid)}
          <div class="verify-name">${escapeHtml(name)}</div>
        </div>
        <div class="verify-hello">
          <div class="verify-hello-text">${escapeHtml(hello)}</div>
          ${found.kind === "in" ? `<button type="button" class="verify-reply" data-act="reply">回复</button>` : ""}
        </div>
        <div class="verify-meta"><span>来源</span><span>${escapeHtml(source)}</span></div>
      </div>
      ${actions}`;
    el.querySelectorAll("[data-act]").forEach((btn) => {
      btn.onclick = async (e) => {
        e.stopPropagation();
        const act = btn.dataset.act;
        if (act === "goto") {
          state.verifyStep = "confirm";
          renderVerifyDetail();
          return;
        }
        if (act === "reply") {
          const note = await dlgPrompt("通过后将把这条回复发给对方", state.verifyReplyDraft[uid] || "", "回复");
          if (note === null) return;
          state.verifyReplyDraft[uid] = String(note || "").trim();
          toast(state.verifyReplyDraft[uid] ? "已记下回复，通过后发送" : "已清空回复");
          return;
        }
        await handleRequest(uid, act);
      };
    });
  }

  function closeVerify() {
    state.verifyOpen = false;
    state.verifyPeer = "";
    state.verifyStep = "";
    toggleHidden("verify-pane", true);
    const main = document.querySelector(".chat-main");
    if (main) main.classList.remove("hidden");
    const row = $("new-friends-row");
    if (row) row.classList.remove("active");
  }

  function openVerifyList() {
    state.verifyOpen = true;
    state.verifyPeer = "";
    state.verifyStep = "";
    setTab("contacts");
    toggleHidden("verify-pane", false);
    const main = document.querySelector(".chat-main");
    if (main) main.classList.add("hidden");
    setChatSide(false);
    renderRequests();
  }

  function openVerifyDetail(uid, kind) {
    if (!uid) return;
    state.verifyOpen = true;
    state.verifyPeer = uid;
    state.verifyStep = kind === "out" ? "detail" : "detail";
    setTab("contacts");
    toggleHidden("verify-pane", false);
    const main = document.querySelector(".chat-main");
    if (main) main.classList.add("hidden");
    setChatSide(false);
    renderRequests();
  }

  async function sendFriendRequest(peer, source) {
    peer = String(peer || "").trim();
    if (!peer || peer === state.uid) return;
    if ((state.friends || []).some((f) => friendUid(f) === peer)) {
      toast("已经是好友");
      return;
    }
    const me = nickOf(state.uid) || state.uid;
    const hello = await dlgPrompt("你需要发送验证申请，等待对方通过", "我是" + me, "申请添加朋友");
    if (hello === null) return;
    try {
      await api("/v1/friend-requests", {
        method: "POST",
        body: JSON.stringify({ peer_uid: peer, hello: String(hello || "").trim(), source: source || "search" }),
      });
      toast("已发送好友申请");
      if ($("add-uid")) $("add-uid").value = "";
      if ($("search-hits")) $("search-hits").innerHTML = "";
      await loadRequests();
      await loadFriends();
      openVerifyList();
    } catch (err) {
      dlgAlert(err.message);
    }
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
      const draft = (state.verifyReplyDraft || {})[peer];
      if (action === "accept" && draft) {
        try {
          await sendPayload({ type: "TEXT", text: draft }, { cid: cidOf(state.uid, peer), peerUid: peer });
        } catch (_) {}
        delete state.verifyReplyDraft[peer];
      }
      await loadRequests();
      await loadFriends();
      await loadConvs();
      if (state.verifyOpen) {
        const still = findRequest(peer);
        if (!still) {
          state.verifyPeer = "";
          state.verifyStep = "";
        }
        renderRequests();
      }
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

  function rememberGroup(g) {
    if (!g) return;
    const cid = field(g, "cid") || "";
    if (cid) {
      state.groupCache = state.groupCache || {};
      state.groupCache[cid] = g;
    }
    if (cid && cid === state.activeCid) state.group = g;
  }

  function activeGroup() {
    if (!isGroup(state.activeCid)) return null;
    const g = state.group;
    if (g && (field(g, "cid") || "") === state.activeCid) return g;
    return (state.groupCache && state.groupCache[state.activeCid]) || null;
  }

  function speakBlockedReason() {
    if (!state.activeCid) return "idle";
    if (!isGroup(state.activeCid)) {
      return isBlocked(state.activePeer) ? "blocked" : "";
    }
    const g = activeGroup();
    if (!g) return "";
    const me = (g.members || []).find((m) => uidOf(m) === state.uid) || {};
    const role = me.role || "";
    if (role === "owner") return "";
    if (me.muted || me.Muted) return "muted_me";
    if ((g.mutedAll || g.muted_all) && role !== "admin") return "muted_all";
    return "";
  }

  function lockComposer() {
    const reason = speakBlockedReason();
    const on = !!state.activeCid && !reason;
    setComposerEnabled(on);
    const draft = $("draft");
    const sub = $("chat-sub");
    if (reason === "blocked") {
      if (draft) draft.placeholder = "已拉黑，无法发送";
      if (sub) sub.textContent = "已拉黑，无法发送";
    } else if (reason === "muted_all") {
      if (draft) draft.placeholder = "全员禁言中";
      if (sub) sub.textContent = "全员禁言中";
    } else if (reason === "muted_me") {
      if (draft) draft.placeholder = "你已被禁言";
      if (sub) sub.textContent = "你已被禁言";
    } else if (on) {
      syncBurnUI();
      if (sub && isGroup(state.activeCid)) sub.textContent = groupModeLabel(activeGroupMode());
    } else if (draft) {
      draft.placeholder = "";
    }
  }

  function applyGroupAvatarToConv(cid, g) {
    if (!cid || !g) return;
    const url = field(g, "avatarUrl", "avatar_url") || "";
    const patch = (c) => {
      if (!c || c.cid !== cid) return;
      if (g.name) c.title = g.name;
      c.avatarUrl = url;
      c.avatar_url = url;
      const p = Object.assign({}, peerProfile(c));
      p.avatarUrl = url;
      p.avatar_url = url;
      const mode = groupModeOf(g);
      if (mode) {
        p.email = "grp:" + mode;
      }
      c.peerProfile = p;
      c.peer_profile = p;
    };
    (state.convs || []).forEach(patch);
    (state.hiddenConvs || []).forEach(patch);
  }

  function renderChatHeadAvatar() {
    const el = $("chat-head-avatar");
    if (!el) return;
    if (!state.activeCid) {
      el.innerHTML = "";
      el.classList.add("hidden");
      return;
    }
    let url = "";
    let name = "";
    let uid = "";
    if (isGroup(state.activeCid)) {
      const g = activeGroup();
      const conv = (state.convs || []).find((c) => c.cid === state.activeCid) ||
        (state.hiddenConvs || []).find((c) => c.cid === state.activeCid);
      url = (g && (field(g, "avatarUrl", "avatar_url") || "")) || convAvatar(conv) || "";
      name = (g && g.name) || (conv && convTitle(conv)) || "群聊";
      uid = state.activeCid;
    } else {
      uid = state.activePeer || "";
      name = nickOf(uid) || uid;
      url = avatarOf(uid);
    }
    el.innerHTML = avatarHTML(url, name, uid);
    el.classList.remove("hidden");
  }

  function announcePinSig(text) {
    const raw = String(text || "");
    let h = 5381;
    for (let i = 0; i < raw.length; i++) h = ((h << 5) + h + raw.charCodeAt(i)) >>> 0;
    return h.toString(16) + ":" + raw.length;
  }
  function announcePinDismissKey(cid, text) {
    return "surge:announce-pin:" + state.uid + ":" + cid + ":" + announcePinSig(text);
  }
  function isAnnouncePinDismissed(cid, text) {
    if (!state.uid || !cid) return false;
    try {
      return localStorage.getItem(announcePinDismissKey(cid, text)) === "1";
    } catch (_) {
      return false;
    }
  }
  function dismissAnnouncePin() {
    if (!state.uid || !state.activeCid) return;
    const g = isGroup(state.activeCid) ? activeGroup() : null;
    const raw = g ? String(field(g, "announcement") || "").trim() : "";
    if (!raw) return;
    try {
      localStorage.setItem(announcePinDismissKey(state.activeCid, raw), "1");
    } catch (_) {}
    syncAnnouncePin();
  }

  function syncAnnouncePin() {
    const el = $("announce-pin");
    const text = $("announce-pin-text");
    if (!el) return;
    const g = isGroup(state.activeCid) ? activeGroup() : null;
    const raw = g ? String(field(g, "announcement") || "").trim() : "";
    if (!raw || isAnnouncePinDismissed(state.activeCid, raw)) {
      el.classList.add("hidden");
      if (text) text.textContent = "";
      el.title = "";
      return;
    }
    const preview = raw.replace(/\s+/g, " ");
    if (text) text.textContent = preview;
    el.title = raw;
    el.classList.remove("hidden");
  }

  function updateChatHeader() {
    renderChatHeadAvatar();
    if (!state.activeCid) return;
    if (isGroup(state.activeCid)) return;
    const sub = $("chat-sub");
    if (isFileHelper(state.activePeer)) {
      sub.textContent = "发给自己 · 跨端同步";
      return;
    }
    const on = state.online[state.activePeer];
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
      lockComposer();
      renderChatHeadAvatar();
      syncAnnouncePin();
      closeAdminsBox();
      return;
    }
    bar.classList.remove("hidden");
    toggleHidden("side-direct", true);
    toggleHidden("side-group-admin", false);
    toggleHidden("rename-row", !isGroupManager());
    toggleHidden("announce-row", !isGroupManager());
    toggleHidden("admins-btn", !isGroupOwner());
    try {
      const g = await api("/v1/group?cid=" + encodeURIComponent(state.activeCid));
      rememberGroup(g);
      const members = g.members || [];
      $("chat-title").textContent = (g.name || "群聊") + " (" + members.length + ")";
      if ($("side-group-name")) $("side-group-name").textContent = g.name || "";
      const avEl = $("side-group-avatar");
      if (avEl) {
        const url = field(g, "avatarUrl", "avatar_url") || "";
        avEl.innerHTML = avatarHTML(url, g.name || "群聊", g.cid || state.activeCid);
      }
      applyGroupAvatarToConv(state.activeCid, g);
      renderChatHeadAvatar();
      syncAnnouncePin();
      renderLists();
      const owner = g.ownerUid || g.owner_uid;
      const isOwner = owner === state.uid;
      if ($("group-avatar-btn")) $("group-avatar-btn").classList.toggle("readonly", !isOwner);
      const manager = isGroupManager();
      toggleHidden("rename-row", !manager);
      toggleHidden("announce-row", !manager);
      toggleHidden("admins-btn", !isOwner);
      if ($("side-announce")) {
        const raw = String(field(g, "announcement") || "");
        const preview = raw.replace(/\s+/g, " ").trim();
        $("side-announce").textContent = preview || "未设置";
        $("side-announce").title = raw;
      }
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
      const anon = groupModeOf(g) === "anonymous";
      const addTile = groupModeOf(g) === "private" && !isOwner
        ? ""
        : `<button type="button" class="side-member add" id="side-add-btn"><span class="side-add-plus">+</span><span class="side-member-name">添加</span></button>`;
      $("member-chips").innerHTML = members
        .map((m) => {
          const id = uidOf(m);
          const kick = manager && id && id !== state.uid && (m.role !== "owner") ? " kick" : "";
          const real = (m.nickname || m.Nickname) || names[id] || nickOf(id) || id;
          const label = anon && !manager ? anonNick(id) : real;
          const face = anon && !manager ? avatarHTML("", label, label) : avatarHTML(avatarOf(id), label, id);
          const badge = m.role === "owner"
            ? `<span class="role-badge">群主</span>`
            : m.role === "admin"
              ? `<span class="role-badge admin">管理</span>`
              : "";
          return `<div class="side-member${kick}" data-uid="${escapeHtml(id)}" data-name="${escapeHtml(label)}" data-role="${escapeHtml(m.role || "")}">${face}<span class="side-member-name">${escapeHtml(label)}${badge}</span></div>`;
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
      lockComposer();
      if ($("admins-box") && !$("admins-box").classList.contains("hidden")) {
        if (isOwner) renderAdminRows();
        else closeAdminsBox();
      }
    } catch (err) {
      $("chat-sub").textContent = err.message;
      lockComposer();
      renderChatHeadAvatar();
      syncAnnouncePin();
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

  async function setGroupMemberRole(uid, role) {
    await api("/v1/group-member", { method: "POST", body: JSON.stringify({ cid: state.activeCid, uid, role }) });
    await refreshGroup();
  }

  function closeAdminsBox() {
    toggleHidden("admins-box", true);
  }

  function renderAdminRows() {
    const listEl = $("admins-list");
    if (!listEl) return;
    const members = (state.group && state.group.members) || [];
    if (!members.length) {
      listEl.innerHTML = `<div class="devices-empty">暂无成员</div>`;
      return;
    }
    listEl.innerHTML = members
      .map((m) => {
        const id = uidOf(m);
        if (!id) return "";
        const role = m.role || m.Role || "member";
        const label = (m.nickname || m.Nickname) || nickOf(id) || id;
        const badge = role === "owner" ? "群主" : role === "admin" ? "管理员" : "成员";
        let act = "";
        if (role !== "owner") {
          const next = role === "admin" ? "member" : "admin";
          const text = next === "admin" ? "设为管理员" : "取消管理员";
          act = `<div class="row-actions"><button type="button" data-uid="${escapeHtml(id)}" data-role="${escapeHtml(next)}">${text}</button></div>`;
        }
        return `<div class="row">${avatarHTML(avatarOf(id), label, id)}<div class="row-main"><div class="row-title">${escapeHtml(label)}</div><div class="row-sub">${escapeHtml(badge)}</div></div>${act}</div>`;
      })
      .join("");
  }

  function openAdminsBox() {
    if (!isGroupOwner() || !state.group) return;
    const box = $("admins-box");
    if (!box) return;
    renderAdminRows();
    box.classList.remove("hidden");
  }

  async function onMemberClick(uid, role) {
    if (!uid) return;
    if (uid === state.uid) {
      if ($("mynick-row")) $("mynick-row").click();
      return;
    }
    if (!isGroupManager()) {
      openMemberCard(uid);
      return;
    }
    const items = [];
    items.push(["资料 / 加好友", () => openMemberCard(uid)]);
    const owner = state.group && (state.group.ownerUid || state.group.owner_uid);
    const isOwner = owner === state.uid;
    const m = (state.group.members || []).find((x) => uidOf(x) === uid) || {};
    const muted = !!(m.muted || m.Muted);
    items.push([muted ? "解除禁言" : "禁言", async () => {
      await api("/v1/group-member", { method: "POST", body: JSON.stringify({ cid: state.activeCid, uid, muted: !muted }) });
      const mem = ((state.group && state.group.members) || []).find((x) => uidOf(x) === uid);
      if (mem) {
        mem.muted = !muted;
        mem.Muted = !muted;
      }
      lockComposer();
      await refreshGroup();
    }]);
    if (isOwner && role !== "owner") {
      const next = role === "admin" ? "member" : "admin";
      items.push([next === "admin" ? "设为管理员" : "取消管理员", () => setGroupMemberRole(uid, next)]);
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
    state.openingChat = true;
    closeVerify();
    try {
    if (state.activeCid && state.activeCid !== cid) saveDraft();
    if (state.activeCid !== cid) {
      const prev = state.convs.find((c) => c.cid === cid) || (state.hiddenConvs || []).find((c) => c.cid === cid);
      state.unreadAtOpen = Number((prev && (prev.unread || 0)) || 0);
      state.jumpUnread = state.unreadAtOpen > 0;
      state.typingMap = {};
      state.messages = [];
      state.highlightId = "";
      if ($("msgs")) $("msgs").innerHTML = "";
    }
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
    forgetHidden(cid);
    if (isGroup(cid)) {
      if (!(state.group && (field(state.group, "cid") || "") === cid)) {
        state.group = (state.groupCache && state.groupCache[cid]) || null;
      }
    } else {
      state.group = null;
    }
    syncBurnUI();
    state.peerReadSeq = 0;
    state.hasMore = false;
    state.searchQ = "";
    state.readCursors = {};
    if ($("chat-search")) $("chat-search").value = "";
    $("chat-search-bar").classList.add("hidden");
    if ($("media-pane")) $("media-pane").classList.add("hidden");
    const conv = state.convs.find((c) => c.cid === cid) || (state.hiddenConvs || []).find((c) => c.cid === cid);
    if (isGroup(cid)) {
      $("chat-title").textContent = (conv && conv.title) || "群聊";
      $("chat-sub").textContent = "";
    } else {
      $("chat-title").textContent = (conv && convTitle(conv)) || peer;
      updateChatHeader();
    }
    renderChatHeadAvatar();
    syncAnnouncePin();
    toggleHidden("side-shared", false);
    toggleHidden("chat-search-toggle", false);
    toggleHidden("mute-btn", false);
    toggleHidden("pin-btn", false);
    toggleHidden("hide-btn", false);
    const helper = isFileHelper(peer);
    toggleHidden("remark-btn", isGroup(cid) || helper);
    toggleHidden("tag-btn", isGroup(cid) || helper);
    toggleHidden("e2ee-btn", isGroup(cid) || helper);
    toggleHidden("block-btn", isGroup(cid) || helper);
    toggleHidden("qr-card-btn", isGroup(cid) || helper);
    toggleHidden("send-card-btn", isGroup(cid) || helper);
    toggleHidden("e2ee-fp", isGroup(cid) || helper || !state.e2eeOn);
    toggleHidden("side-direct", isGroup(cid));
    toggleHidden("side-group-admin", !isGroup(cid));
    if (!isGroup(cid)) renderSidePeer();
    syncSideSwitches();
    if (!isGroup(cid) && state.e2eeOn) refreshE2eeFp();
    lockComposer();
    loadDraft(cid);
    loadPinned(cid);
    await reloadTimeline();
    renderLists();
    await refreshGroup();
    markRead();
    await loadConvs();
    refreshPresence();
    refreshGroupRead();
    } finally {
      state.openingChat = false;
    }
  }

  async function reloadTimeline() {
    if (!state.activeCid) return;
    const cid = state.activeCid;
    const q = state.searchQ ? "&q=" + encodeURIComponent(state.searchQ) : "";
    const data = await api("/v1/timeline?cid=" + encodeURIComponent(cid) + "&limit=50" + q);
    if (state.activeCid !== cid) return;
    const fetched = (data.messages || []).map((m) => {
      m.cid = cid;
      return m;
    });
    const seen = {};
    fetched.forEach((m) => {
      const id = String(field(m, "msgId", "msg_id") || "");
      if (id) seen[id] = true;
    });
    const live = state.messages.filter((m) => {
      if (String(field(m, "cid", "cid") || "") !== cid) return false;
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
        const cid = state.activeCid;
        for (const m of older) {
          m.cid = cid;
          await decodeIncoming(m);
        }
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
      if (!m._reeditText) {
        m._reeditText = (m.payload && m.payload.text) || "";
      }
      m.recalled = true;
      m._burnLeft = 0;
      m.payload = { type: "RECALL", text: was ? "已销毁" : "" };
    });
    if (cid === state.activeCid) renderMsgs();
    loadConvs();
  }

  function showTyping(from) {
    if (from === state.uid) return;
    if (!from) return;
    if (isAnonGroup()) return;
    state.typingMap = state.typingMap || {};
    state.typingMap[from] = Date.now();
    refreshTyping();
  }

  function refreshTyping() {
    const el = $("typing");
    if (!el) return;
    const now = Date.now();
    const alive = Object.keys(state.typingMap || {}).filter((u) => now - state.typingMap[u] < 3000);
    alive.forEach((u) => {
      if (now - state.typingMap[u] >= 3000) delete state.typingMap[u];
    });
    const names = Object.keys(state.typingMap || {}).filter((u) => now - state.typingMap[u] < 3000).map((u) => nickOf(u) || u);
    if (!names.length) {
      el.classList.add("hidden");
      return;
    }
    el.textContent = isGroup(state.activeCid)
      ? names.join("、") + " 正在输入…"
      : "对方正在输入…";
    el.classList.remove("hidden");
    clearTimeout(state.typingTimer);
    state.typingTimer = setTimeout(refreshTyping, 800);
  }

  function notifyIncoming(ev) {
    const from = senderOf(ev);
    if (from === state.uid) return;
    const mentions = (ev.payload && (ev.payload.mentionUids || ev.payload.mention_uids)) || [];
    const mentioned = mentions.indexOf(state.uid) >= 0 || ((ev.payload && ev.payload.text) || "").indexOf("@" + state.uid) >= 0;
    if (isMuted(ev.cid) && !mentioned) return;
    if (inDnd() && !mentioned) return;
    playNotifySound();
    if (!document.hidden) return;
    if (!window.Notification || Notification.permission !== "granted") return;
    const previewOn = state.settings.notifyPreview !== false && state.settings.notify_preview !== false;
    const text = previewOn ? ((ev.payload && ev.payload.text) || "[新消息]") : "你收到一条新消息";
    const fromName = nickOf(from) || from;
    const n = new Notification(fromName || "新消息", { body: String(text).slice(0, 80), tag: ev.cid || "" });
    n.onclick = () => {
      try { window.focus(); } catch (_) {}
      n.close();
      openChat(from, ev.cid);
    };
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
      if (env.typing.cid === state.activeCid) showTyping(senderOf(env.typing) || env.typing.fromUid || env.typing.from_uid);
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
    if (kind === "reaction") {
      const parts = extra.split(" ");
      const cid = parts[0];
      const msgId = parts[1];
      if (cid === state.activeCid && msgId) reloadTimeline().catch(() => {});
      return;
    }
    if (kind === "friend_request") {
      toast(name + " 请求添加你为好友");
      openVerifyList();
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
        renderChatHeadAvatar();
        syncAnnouncePin();
        renderMsgs();
        loadConvs();
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
        renderChatHeadAvatar();
        syncAnnouncePin();
        renderMsgs();
        loadConvs();
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
      const sysText = String((ev.payload && ev.payload.text) || "");
      if (isSystemMsg(ev) && /禁言|群公告/.test(sysText)) {
        refreshGroup();
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
    setEnterSend(enterSendOn());
    loadMuted();
    loadPins();
    loadHiddenConvs();
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
    try {
      applySettings(await api("/v1/settings"));
    } catch (_) {
      applySettings(state.settings);
    }
    startPresencePoll();
    startWSElection();
    maybeApproveTicket();
    e2eePair().catch(() => {});
    maybeAddFriendFromURL();
    maybeJoinFromURL();
  }

  function maybeAddFriendFromURL() {
    const add = state.addPeer || new URLSearchParams(location.search).get("add") || "";
    if (!add || add === state.uid || !state.token) return;
    state.addPeer = "";
    history.replaceState({}, "", "/");
    sendFriendRequest(add, "qr");
  }

  function maybeJoinFromURL() {
    const token = new URLSearchParams(location.search).get("join") || "";
    if (!token || !state.token) return;
    history.replaceState({}, "", "/");
    api("/v1/group-join-invite", { method: "POST", body: JSON.stringify({ token }) })
      .then(async (g) => {
        toast("已加入群聊");
        await loadConvs();
        if (g.cid) openChat("", g.cid);
      })
      .catch((err) => toast(err.message || "入群失败"));
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
    if (!(dest && dest.forward) && cid === state.activeCid && speakBlockedReason()) {
      const r = speakBlockedReason();
      toast(r === "muted_all" ? "全员禁言中" : r === "muted_me" ? "你已被禁言" : "无法发送");
      lockComposer();
      return;
    }
    if (typeof payload.text === "string") payload.text = wellFormed(payload.text);
    if (!(dest && dest.forward)) {
      payload.mentionUids = mentionUidsOf(payload.text || "");
      payload = await attachLinkPreview(payload);
      if (!(dest && dest.forward) && isBurnWanted(cid)) payload.ephemeral = true;
      const sendMode = cid === state.activeCid ? activeGroupMode() : convGroupMode((state.convs || []).find((c) => c.cid === cid));
      if (isGroup(cid) && sendMode === "ephemeral") payload.ephemeral = true;
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
    if (file && (file.type || "").indexOf("image/") === 0 && file.type !== "image/gif" && !state.sendOriginal && !($("orig-toggle") && $("orig-toggle").checked)) {
      file = await compressImage(file);
    }
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
    const video = (file.type || "").indexOf("video/") === 0;
    await sendPayload({
      type: image ? "IMAGE" : video ? "VIDEO" : "FILE",
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
        transcript: file._transcript || "",
      },
    });
    $("draft").value = "";
    localStorage.removeItem(draftKey());
    fitDraft();
  }

  function compressImage(file) {
    return new Promise((resolve) => {
      const url = URL.createObjectURL(file);
      const img = new Image();
      img.onload = () => {
        URL.revokeObjectURL(url);
        const max = 1920;
        let w = img.width;
        let h = img.height;
        if (w > max || h > max) {
          const scale = max / Math.max(w, h);
          w = Math.round(w * scale);
          h = Math.round(h * scale);
        }
        const c = document.createElement("canvas");
        c.width = w;
        c.height = h;
        c.getContext("2d").drawImage(img, 0, 0, w, h);
        c.toBlob((blob) => {
          if (!blob) return resolve(file);
          resolve(new File([blob], String(file.name || "image").replace(/\.\w+$/, ".jpg"), { type: "image/jpeg" }));
        }, "image/jpeg", 0.82);
      };
      img.onerror = () => {
        URL.revokeObjectURL(url);
        resolve(file);
      };
      img.src = url;
    });
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

  $("login-btn").onclick = async () => {
    const uid = $("login-uid").value.trim();
    const password = $("login-pass").value;
    if (!uid) return;
    if (!(await dlgConfirm("确认在此浏览器登录网页版？", "登录确认"))) return;
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
    if ($("pane-favs")) $("pane-favs").classList.toggle("hidden", tab !== "favs");
    if (tab === "chats" && state.verifyOpen) closeVerify();
    if (tab === "favs") loadFavorites();
  }
  onClick("new-chat-btn", () => {
    setTab("contacts");
    const el = $("add-uid");
    if (el) el.focus();
  });
  onClick("new-friends-row", () => openVerifyList());
  onClick("verify-back", () => {
    if (state.verifyPeer) openVerifyList();
    else closeVerify();
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
    await sendFriendRequest(peer, "search");
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
          row.onclick = () => sendFriendRequest(row.dataset.uid, "search");
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
    const modeEl = document.querySelector('input[name="group-mode"]:checked');
    const mode = (modeEl && modeEl.value) || "normal";
    if (!name) return;
    try {
      const g = await api("/v1/groups", { method: "POST", body: JSON.stringify({ name, members, mode }) });
      $("group-name").value = "";
      $("group-members").value = "";
      await loadConvs();
      if (g.cid) await openChat("", g.cid);
    } catch (err) {
      dlgAlert(err.message);
    }
  };
  document.querySelectorAll('input[name="group-mode"]').forEach((el) => {
    el.addEventListener("change", () => {
      document.querySelectorAll("#group-modes label").forEach((lab) => lab.classList.toggle("on", lab.querySelector("input").checked));
    });
  });

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
    if (!isGroupManager()) return;
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
      if (!isGroupOwner()) {
        throw new Error("仅群主可设置群头像");
      }
      await api("/v1/group-update", {
        method: "POST",
        body: JSON.stringify({ cid: state.activeCid, avatar_url: url }),
      });
      await refreshGroup();
      await loadConvs();
    } else {
      await api("/v1/me", { method: "POST", body: JSON.stringify({ avatar_url: url }) });
      await loadMe();
      if (state.activeCid) renderMsgs({ stick: false });
    }
  }

  $("group-avatar-btn").onclick = () => {
    if (!isGroupOwner()) return;
    $("group-avatar-file").click();
  };
  $("group-avatar-file").onchange = () => {
    const f = $("group-avatar-file").files && $("group-avatar-file").files[0];
    $("group-avatar-file").value = "";
    if (!f || !isGroupOwner()) return;
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
    const reason = speakBlockedReason();
    if (!state.activeCid || reason) {
      if (reason && reason !== "idle") {
        toast(reason === "muted_all" ? "全员禁言中" : reason === "muted_me" ? "你已被禁言" : reason === "blocked" ? "已拉黑，无法发送" : "无法发送");
        lockComposer();
      }
      return;
    }
    const text = $("draft").value.trim();
    if (!text) return;
    $("draft").value = "";
    localStorage.removeItem(draftKey());
    fitDraft();
    await sendPayload({ type: "TEXT", text });
  };
  if ($("burn-toggle")) {
    $("burn-toggle").onchange = () => {
      setBurnWanted(state.activeCid, $("burn-toggle").checked);
      syncBurnUI();
    };
  }

  $("attach-btn").onclick = () => $("file").click();
  if ($("shot-btn")) $("shot-btn").onclick = () => captureScreenshot();
  if ($("mute-all-btn")) {
    $("mute-all-btn").onclick = async () => {
      if (!state.group) return;
      const cur = !!(state.group.mutedAll || state.group.muted_all);
      try {
        const g = await api("/v1/group-mute-all", {
          method: "POST",
          body: JSON.stringify({ cid: state.activeCid, muted: !cur }),
        });
        rememberGroup(g);
        $("mute-all-btn").textContent = g.mutedAll || g.muted_all ? "解除禁言" : "全员禁言";
        lockComposer();
      } catch (err) {
        dlgAlert(err.message);
      }
    };
  }
  if ($("tag-btn")) {
    $("tag-btn").onclick = async () => {
      if (!state.activePeer) return;
      const cur = visibleTags((state.friends.find((f) => friendUid(f) === state.activePeer) || {}).tags);
      const raw = await dlgPrompt("多个标签用逗号分隔", cur.join(","), "好友标签");
      if (raw == null) return;
      const tags = raw.split(/[,，]/).map((s) => s.trim()).filter(Boolean);
      const f = state.friends.find((x) => friendUid(x) === state.activePeer);
      if (f && isStarred(f)) tags.push(STAR_TAG);
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
          if (state.voiceText) file._transcript = state.voiceText;
          state.voiceText = "";
          try {
            await uploadFile(file);
          } catch (err) {
            dlgAlert(err.message);
          }
        };
        recMedia.start();
        recBtn.classList.add("rec-on");
        state.voiceText = "";
        const Recog = window.SpeechRecognition || window.webkitSpeechRecognition;
        if (Recog) {
          try {
            const rec = new Recog();
            rec.lang = "zh-CN";
            rec.interimResults = false;
            rec.onresult = (ev) => {
              const t = ev.results && ev.results[0] && ev.results[0][0] && ev.results[0][0].transcript;
              if (t) state.voiceText = t;
            };
            rec.start();
            recBtn._recog = rec;
          } catch (_) {}
        }
      } catch (err) {
        dlgAlert("无法录音：" + (err.message || err));
      }
    };
    const stopRec = () => {
      if (recBtn._recog) {
        try { recBtn._recog.stop(); } catch (_) {}
        recBtn._recog = null;
      }
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
          const qq = q.toLowerCase();
          const seen = {};
          const local = [];
          const pushLocal = (cid, peer, title, sub) => {
            if (!cid || seen[cid]) return;
            seen[cid] = true;
            local.push({ cid, peer: peer || "", title: title || cid, sub: sub || "" });
          };
          (state.convs || []).concat(state.hiddenConvs || []).forEach((c) => {
            const title = convTitle(c) || c.title || "";
            const peer = field(c, "peerUid", "peer_uid") || "";
            const last = c.lastText || c.last_text || "";
            if (
              (title && title.toLowerCase().includes(qq)) ||
              (peer && peer.toLowerCase().includes(qq)) ||
              (last && last.toLowerCase().includes(qq))
            ) {
              pushLocal(c.cid, peer, title, last || (isGroup(c.cid) ? "群聊" : peer));
            }
          });
          (state.friends || []).forEach((f) => {
            const uid = friendUid(f);
            const name = friendName(f);
            if ((name && name.toLowerCase().includes(qq)) || (uid && uid.toLowerCase().includes(qq))) {
              pushLocal(cidOf(state.uid, uid), uid, name, uid);
            }
          });
          const data = await api("/v1/search?q=" + encodeURIComponent(q));
          const hits = data.hits || [];
          hits.forEach((h) => {
            const msg = h.message || {};
            if (field(msg, "msgId", "msg_id")) return;
            pushLocal(h.cid, peerFromCid(h.cid, state.uid), h.title || h.cid, (msg.payload && msg.payload.text) || "");
          });
          const localHTML = local.length
            ? `<div class="mid-label">联系人 / 会话</div>` +
              local
                .map((h) => `<div class="row" data-cid="${escapeHtml(h.cid)}" data-peer="${escapeHtml(h.peer)}"><div class="row-main"><div class="row-title">${escapeHtml(h.title)}</div><div class="row-sub">${escapeHtml(h.sub || "会话")}</div></div></div>`)
                .join("")
            : "";
          const msgHits = hits.filter((h) => field((h.message || {}), "msgId", "msg_id"));
          const msgHTML = msgHits.length
            ? `<div class="mid-label">聊天记录</div>` +
              msgHits
                .map((h) => {
                  const msg = h.message || {};
                  const text = (msg.payload && msg.payload.text) || "";
                  return `<div class="row" data-cid="${escapeHtml(h.cid)}" data-mid="${escapeHtml(field(msg, "msgId", "msg_id") || "")}" data-seq="${escapeHtml(String(field(msg, "convSeq", "conv_seq") || 0))}"><div class="row-main"><div class="row-title">${escapeHtml(h.title || h.cid)}</div><div class="row-sub">${escapeHtml(text)}</div></div></div>`;
                })
                .join("")
            : "";
          box.classList.remove("hidden");
          if (!localHTML && !msgHTML) {
            box.innerHTML = `<div class="row"><div class="row-sub">没有匹配的会话或聊天记录</div></div>`;
          } else {
            box.innerHTML = localHTML + msgHTML;
          }
          box.querySelectorAll(".row[data-cid]").forEach((row) => {
            row.onclick = () => {
              const cid = row.dataset.cid;
              const peer = row.dataset.peer || peerFromCid(cid, state.uid) || "";
              const mid = row.dataset.mid;
              $("global-search").value = "";
              box.classList.add("hidden");
              box.innerHTML = "";
              if (mid) jumpToMessage(cid, mid, row.dataset.seq, peer);
              else openChat(peer, cid);
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
  async function openGroupAnnounce() {
    if (!isGroup(state.activeCid) || !state.group) return;
    const cur = String(field(state.group, "announcement") || "");
    if (!isGroupOwner()) {
      await dlgAlert(cur || "未设置", "群公告");
      return;
    }
    const next = await dlgPromptArea("所有群成员可见。留空并确定可清空公告。", cur, "编辑群公告");
    if (next === null || next === cur) return;
    try {
      await api("/v1/group-update", {
        method: "POST",
        body: JSON.stringify({ cid: state.activeCid, announcement: next }),
      });
      await refreshGroup();
      await reloadTimeline();
    } catch (err) {
      dlgAlert(err.message || "保存失败", "群公告");
    }
  }
  onClick("announce-pin-dismiss", (e) => {
    if (e) {
      e.stopPropagation();
      e.preventDefault();
    }
    dismissAnnouncePin();
  });
  onClick("announce-pin", (e) => {
    if (e && e.target && e.target.closest && e.target.closest("#announce-pin-dismiss")) return;
    openGroupAnnounce();
  });
  onClick("announce-row", () => {
    openGroupAnnounce();
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
  let qrCardUrl = "";
  function closeQrCard() {
    const box = $("qr-card-box");
    if (box) box.classList.add("hidden");
    const img = $("qr-card-img");
    if (img) {
      img.removeAttribute("src");
      img.alt = "名片二维码";
    }
    if (qrCardUrl) {
      URL.revokeObjectURL(qrCardUrl);
      qrCardUrl = "";
    }
  }
  async function loadQrCard(retry) {
    const box = $("qr-card-box");
    const img = $("qr-card-img");
    if (!box || !img) {
      dlgAlert("二维码界面未就绪", "名片二维码");
      return;
    }
    if (!state.token) {
      dlgAlert("请先登录后再查看名片二维码", "名片二维码");
      return;
    }
    img.removeAttribute("src");
    img.alt = "加载中";
    box.classList.remove("hidden");
    try {
      const r = await fetch("/v1/me/qr.png?t=" + Date.now(), {
        headers: { Authorization: "Bearer " + state.token },
      });
      if (r.status === 401 && retry !== false && state.refresh) {
        await refreshTokens();
        return loadQrCard(false);
      }
      if (!r.ok) {
        const text = await r.text();
        throw new Error(friendlyHttp(r.status, text) || "无法加载二维码");
      }
      const blob = await r.blob();
      if (qrCardUrl) URL.revokeObjectURL(qrCardUrl);
      qrCardUrl = URL.createObjectURL(blob);
      img.src = qrCardUrl;
      img.alt = "名片二维码";
    } catch (err) {
      closeQrCard();
      dlgAlert((err && err.message) || "无法加载二维码", "名片二维码");
    }
  }
  onClick("qr-card-btn", () => { loadQrCard(); });
  onClick("send-card-btn", () => sendCard(state.uid));
  onClick("settings-btn", async () => {
    try {
      applySettings(await api("/v1/settings"));
    } catch (_) {
      applySettings(state.settings);
    }
    $("settings-box").classList.remove("hidden");
  });
  onClick("settings-ok", async () => {
    await saveSettings();
    $("settings-box").classList.add("hidden");
  });
  ["set-dark", "set-sound", "set-preview", "set-enter-send"].forEach((id) => {
    const el = $(id);
    if (!el) return;
    el.onclick = () => {
      el.classList.toggle("on");
      if (id === "set-enter-send") setEnterSend(el.classList.contains("on"));
    };
  });
  onClick("set-pass-btn", async () => {
    const oldP = ($("set-old-pass") && $("set-old-pass").value) || "";
    const neu = ($("set-new-pass") && $("set-new-pass").value) || "";
    const neu2 = ($("set-new-pass2") && $("set-new-pass2").value) || "";
    if (!oldP || !neu) {
      dlgAlert("请填写当前密码和新密码");
      return;
    }
    if (neu !== neu2) {
      dlgAlert("两次输入的新密码不一致");
      return;
    }
    try {
      await api("/v1/me", { method: "POST", body: JSON.stringify({ old_password: oldP, new_password: neu }) });
      toast("密码已更新");
      if ($("set-old-pass")) $("set-old-pass").value = "";
      if ($("set-new-pass")) $("set-new-pass").value = "";
      if ($("set-new-pass2")) $("set-new-pass2").value = "";
    } catch (err) {
      dlgAlert(err.message || "改密失败");
    }
  });
  onClick("media-tab-btn", () => openMediaPane());
  onClick("media-back", () => {
    if ($("media-pane")) $("media-pane").classList.add("hidden");
  });
  if ($("media-tabs")) {
    $("media-tabs").addEventListener("click", (e) => {
      const btn = e.target.closest("button[data-mk]");
      if (!btn) return;
      state.mediaKind = btn.dataset.mk;
      $("media-tabs").querySelectorAll("button").forEach((b) => b.classList.toggle("on", b === btn));
      fillMediaList();
    });
  }
  if ($("chat-search-kinds")) {
    $("chat-search-kinds").addEventListener("click", async (e) => {
      const btn = e.target.closest("button[data-sk]");
      if (!btn) return;
      state.searchKind = btn.dataset.sk;
      $("chat-search-kinds").querySelectorAll("button").forEach((b) => b.classList.toggle("on", b === btn));
      if (btn.dataset.sk === "date") {
        const day = await dlgPrompt("例如 2026-08-21", "", "按日期查找");
        if (day) filterChatByDate(day);
        return;
      }
      if (btn.dataset.sk === "image" || btn.dataset.sk === "file") {
        filterChatByKind(btn.dataset.sk);
        return;
      }
      state.searchQ = "";
      reloadTimeline().catch(() => {});
    });
  }
  onClick("group-invite-qr-btn", async () => {
    if (!state.activeCid || !isGroup(state.activeCid)) return;
    try {
      const inv = await api("/v1/group-invite-link", { method: "POST", body: JSON.stringify({ cid: state.activeCid }) });
      const url = inv.url || inv.Url || "";
      $("invite-qr-img").src = "/v1/group-invite.png?token=" + encodeURIComponent(inv.token || inv.Token || "");
      $("invite-qr-url").textContent = url;
      $("invite-qr-box").dataset.url = url;
      $("invite-qr-box").classList.remove("hidden");
    } catch (err) {
      dlgAlert(err.message);
    }
  });
  onClick("invite-qr-copy", () => {
    const url = ($("invite-qr-box") && $("invite-qr-box").dataset.url) || "";
    if (url) navigator.clipboard.writeText(url).then(() => toast("已复制链接")).catch(() => {});
  });
  onClick("invite-qr-close", () => $("invite-qr-box").classList.add("hidden"));
  onClick("member-card-close", () => $("member-card").classList.add("hidden"));
  onClick("msg-pin-dismiss", async () => {
    try {
      await api("/v1/pinned", { method: "POST", body: JSON.stringify({ cid: state.activeCid, msg_id: "" }) });
      state.chatPin = null;
      renderMsgPin();
    } catch (err) {
      dlgAlert(err.message);
    }
  });
  if ($("msg-pin")) {
    $("msg-pin-text") && ($("msg-pin-text").onclick = () => {
      const id = state.chatPin && (state.chatPin.msgId || state.chatPin.msg_id);
      if (id) jumpToMessage(state.activeCid, id, 0, state.activePeer);
    });
  }
  if ($("fav-search")) {
    let ft = 0;
    $("fav-search").oninput = () => {
      clearTimeout(ft);
      ft = setTimeout(() => loadFavorites($("fav-search").value.trim()), 200);
    };
  }
  document.addEventListener("click", (e) => {
    const pick = $("react-pick");
    if (
      pick &&
      !pick.classList.contains("hidden") &&
      !pick.contains(e.target) &&
      !(state.reactPickUntil && Date.now() < state.reactPickUntil)
    ) {
      pick.classList.add("hidden");
    }
  });
  onClick("qr-card-close", closeQrCard);
  if ($("qr-card-box")) {
    $("qr-card-box").addEventListener("click", (e) => {
      if (e.target === $("qr-card-box")) closeQrCard();
    });
  }
  onClick("readers-ok", () => $("readers-box").classList.add("hidden"));
  function closeDevices() {
    const box = $("devices-box");
    if (box) box.classList.add("hidden");
  }
  function isSelfDevice(d, mine) {
    const did = String((d && d.device_id) || "");
    if (mine && did) return did === mine;
    return !did && d && d.self === "1";
  }
  function renderDeviceRows(list) {
    const mine = deviceId();
    const items = Array.isArray(list) ? list : [];
    if (!items.length) {
      return `<div class="devices-empty">当前没有在线设备。请确认本窗口已连接后再试。</div>`;
    }
    return items
      .map((d) => {
        const connId = String(d.conn_id || "");
        const did = String(d.device_id || "");
        const self = isSelfDevice(d, mine);
        const short = (did || connId).slice(0, 8);
        const sub = self ? "本机" : short ? "在线 · " + short : "在线";
        const kick = self || !connId
          ? ""
          : `<div class="row-actions"><button type="button" class="danger" data-id="${escapeHtml(connId)}">下线</button></div>`;
        return `<div class="row"><div class="row-main"><div class="row-title">网页版</div><div class="row-sub">${escapeHtml(sub)}</div></div>${kick}</div>`;
      })
      .join("");
  }
  async function loadDevices() {
    const box = $("devices-box");
    const listEl = $("devices-list");
    if (!box || !listEl) {
      dlgAlert("设备列表界面未就绪", "登录设备");
      return;
    }
    listEl.innerHTML = `<div class="devices-empty">加载中…</div>`;
    box.classList.remove("hidden");
    try {
      const data = await api("/v1/devices");
      listEl.innerHTML = renderDeviceRows(data.devices || []);
    } catch (err) {
      listEl.innerHTML = `<div class="devices-empty">无法加载登录设备</div>`;
      dlgAlert(err.message || "无法获取登录设备", "登录设备");
    }
  }
  async function kickDevice(connId) {
    if (!connId) return;
    if (!(await dlgConfirm("将该设备踢下线后需重新登录。", "下线设备", true))) return;
    try {
      await api("/v1/devices", { method: "POST", body: JSON.stringify({ conn_id: connId }) });
      toast("已踢下线");
      await loadDevices();
    } catch (err) {
      dlgAlert(err.message || "下线失败", "登录设备");
    }
  }
  onClick("devices-ok", closeDevices);
  onClick("devices-btn", () => {
    loadDevices();
  });
  if ($("devices-box")) {
    $("devices-box").addEventListener("click", (e) => {
      if (e.target === $("devices-box")) closeDevices();
    });
  }
  if ($("devices-list")) {
    $("devices-list").addEventListener("click", (e) => {
      const btn = e.target.closest("button[data-id]");
      if (!btn) return;
      kickDevice(btn.dataset.id);
    });
  }
  onClick("select-cancel", () => exitSelect());
  onClick("select-fwd", () => {
    const ids = Object.keys(state.selected);
    if (!ids.length) return;
    openForward(ids);
  });
  onClick("select-merge", () => {
    const ids = Object.keys(state.selected);
    if (!ids.length) return;
    const srcs = ids.map((id) => state.messages.find((m) => field(m, "msgId", "msg_id") === id)).filter(Boolean);
    srcs.sort((a, b) => Number(field(a, "convSeq", "conv_seq") || 0) - Number(field(b, "convSeq", "conv_seq") || 0));
    const mergeItems = srcs.map((m) => ({
      fromUid: senderOf(m),
      text: ((m.payload && m.payload.text) || "[消息]").slice(0, 80),
      type: encodePayloadType((m.payload && m.payload.type) || 1),
      createdAtMs: Number(field(m, "createdAtMs", "created_at_ms") || 0),
    }));
    state.forwardMerge = { type: "MERGE", text: "聊天记录", mergeItems };
    openForwardFromPayloads([state.forwardMerge]);
    exitSelect();
  });
  onClick("select-fav", async () => {
    const ids = Object.keys(state.selected);
    if (!ids.length) return;
    for (const id of ids) {
      try { await addFavorite(id); } catch (_) {}
    }
    exitSelect();
  });
  onClick("select-del", async () => {
    const ids = Object.keys(state.selected);
    if (!ids.length) return;
    if (!(await dlgConfirm("删除选中的 " + ids.length + " 条消息？仅对自己可见。", "批量删除"))) return;
    for (const id of ids) {
      try { await deleteForMe(id); } catch (_) {}
    }
    exitSelect();
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
    const cid = state.activeCid;
    const conv = state.convs.find((c) => c.cid === cid) || {};
    const snap = {
      cid,
      peer_uid: field(conv, "peerUid", "peer_uid") || state.activePeer || "",
      peerUid: field(conv, "peerUid", "peer_uid") || state.activePeer || "",
      title: convTitle(conv) || ($("chat-title") && $("chat-title").textContent) || "",
      kind: conv.kind || (isGroup(cid) ? "group" : "p2p"),
      last_text: conv.lastText || conv.last_text || "",
      peer_profile: peerProfile(conv),
    };
    try {
      await api("/v1/conversation-hide", { method: "POST", body: JSON.stringify({ cid }) });
      rememberHidden(snap);
      state.showHidden = false;
      state.activeCid = "";
      state.activePeer = "";
      state.messages = [];
      $("chat-title").textContent = "选择一个会话";
      $("chat-sub").textContent = "";
      renderChatHeadAvatar();
      syncAnnouncePin();
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
      toast("已隐藏，可在会话列表底部找回");
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
      renderChatHeadAvatar();
      syncAnnouncePin();
      setChatSide(false);
      setComposerEnabled(false);
      renderMsgs();
      await loadConvs();
    } catch (err) {
      dlgAlert(err.message);
    }
  };
  onClick("admins-btn", () => {
    openAdminsBox();
  });
  onClick("admins-ok", closeAdminsBox);
  if ($("admins-box")) {
    $("admins-box").addEventListener("click", (e) => {
      if (e.target === $("admins-box")) closeAdminsBox();
    });
  }
  if ($("admins-list")) {
    $("admins-list").addEventListener("click", async (e) => {
      const btn = e.target.closest("button[data-uid][data-role]");
      if (!btn) return;
      if (!isGroupOwner()) return;
      try {
        await setGroupMemberRole(btn.dataset.uid, btn.dataset.role);
        renderAdminRows();
      } catch (err) {
        dlgAlert(err.message);
      }
    });
  }
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
      renderChatHeadAvatar();
      syncAnnouncePin();
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
    if (!isAnonGroup() && now - state.lastTypingAt >= 2000) {
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
    const enterSend = enterSendOn();
    if (e.key !== "Enter") return;
    if (enterSend) {
      if (e.shiftKey || e.ctrlKey || e.metaKey) return;
      e.preventDefault();
      $("send-form").requestSubmit();
      return;
    }
    if (e.ctrlKey || e.metaKey) {
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
    loadHiddenConvs();
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
        maybeJoinFromURL();
      })
      .catch((e) => dlgAlert(e.message));
  } else {
    startQR();
  }

  initMidSplitter();
})();
