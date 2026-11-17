// Live log viewer for the redactor proxy.
//
// Connects to the Server-Sent Events endpoint exposed by the logging package,
// renders each JSON log record, and filters the rendered entries entirely in
// the browser so the server does no per-viewer work beyond the stream itself.
(() => {
  "use strict";

  const DEFAULT_MAX_ENTRIES = 1000;
  const MIN_MAX_ENTRIES = 100;
  const MAX_MAX_ENTRIES = 10000;
  const NEAR_BOTTOM_PX = 32;
  const SEARCH_DEBOUNCE_MS = 120;

  // List-view preview limits. Anything longer is cut and can be inspected in
  // the modal, which always renders the full record.
  const PREVIEW_MSG_LINES = 4;
  const PREVIEW_FIELD_LENGTH = 120;
  const MODAL_OPEN_CLASS = "modal--open";

  const LEVELS = ["debug", "info", "warn", "error"];
  const RESERVED_KEYS = new Set(["time", "level", "msg"]);
  const DEFAULT_EVENTS_URL = "/logs/events";

  const els = {
    viewer: document.getElementById("viewer"),
    empty: document.getElementById("empty"),
    emptyText: document.getElementById("empty-text"),
    stats: document.getElementById("stats"),
    status: document.getElementById("status"),
    statusText: document.getElementById("status-text"),
    dropped: document.getElementById("dropped"),
    filterButtons: Array.from(document.querySelectorAll(".filter")),
    search: document.getElementById("search"),
    autoscroll: document.getElementById("autoscroll"),
    clear: document.getElementById("clear"),
    modal: document.getElementById("modal"),
    modalBackdrop: document.getElementById("modal-backdrop"),
    modalClose: document.getElementById("modal-close"),
    modalContent: document.getElementById("modal-content"),
  };

  const maxEntries = resolveMaxEntries();
  const eventsURL = document.body.dataset.eventsUrl || DEFAULT_EVENTS_URL;

  const state = {
    level: "all",
    query: "",
    autoScroll: true,
    entries: [],
    visible: 0,
    dropped: 0,
    source: null,
    reconnectTimer: 0,
    reconnectAttempts: 0,
  };

  let searchTimer = 0;
  let statsScheduled = false;
  let modalReturnFocus = null;

  // ---------------------------------------------------------------- utilities

  function resolveMaxEntries() {
    const raw = new URLSearchParams(window.location.search).get("max");
    const parsed = Number.parseInt(raw || "", 10);
    if (!Number.isFinite(parsed)) return DEFAULT_MAX_ENTRIES;
    return Math.min(MAX_MAX_ENTRIES, Math.max(MIN_MAX_ENTRIES, parsed));
  }

  function normalizeLevel(raw) {
    if (typeof raw === "number" && Number.isFinite(raw)) {
      // slog's standard levels and common custom offsets.
      if (raw <= -4) return "debug";
      if (raw < 4) return "info";
      if (raw < 8) return "warn";
      return "error";
    }
    const value = String(raw == null ? "" : raw).toLowerCase();
    if (LEVELS.includes(value)) return value;
    if (value === "warning") return "warn";
    if (value === "err" || value === "fatal" || value === "panic" || value === "critical") return "error";
    if (value === "trace" || value === "verbose") return "debug";
    return "info";
  }

  function pad(value, width) {
    return String(value).padStart(width || 2, "0");
  }

  // formatTime returns a compact local wall-clock time for the row, the
  // machine-readable timestamp, and the original value for the tooltip.
  // Unparseable timestamps are shown verbatim.
  function formatTime(raw) {
    if (raw == null || raw === "") return { display: "", iso: "", title: "" };
    const original = String(raw);
    const date = new Date(raw);
    if (Number.isNaN(date.getTime())) {
      return { display: original, iso: "", title: original };
    }
    const display = pad(date.getHours()) + ":" + pad(date.getMinutes()) + ":" + pad(date.getSeconds()) + "." + pad(date.getMilliseconds(), 3);
    return { display: display, iso: date.toISOString(), title: original };
  }

  function formatValue(value) {
    if (typeof value === "string") return value;
    if (value === null) return "null";
    if (typeof value === "object") {
      try {
        return JSON.stringify(value);
      } catch (_err) {
        return String(value);
      }
    }
    return String(value);
  }

  // collectFields flattens nested slog groups into dotted key/value pairs.
  function collectFields(value, prefix, out) {
    for (const key of Object.keys(value)) {
      const child = value[key];
      const name = prefix ? prefix + "." + key : key;
      if (child !== null && typeof child === "object" && !Array.isArray(child)) {
        collectFields(child, name, out);
      } else {
        out.push([name, formatValue(child)]);
      }
    }
    return out;
  }

  // recordFields returns the non-reserved fields of a record as flattened
  // dotted key/value pairs, ready for both the row preview and the modal.
  function recordFields(record) {
    const attrs = {};
    for (const key of Object.keys(record)) {
      if (!RESERVED_KEYS.has(key)) attrs[key] = record[key];
    }
    return collectFields(attrs, "", []);
  }

  // truncatePreview keeps the leading maxLen characters and reports whether
  // anything was dropped.
  function truncatePreview(text, maxLen) {
    const value = String(text == null ? "" : text);
    return { preview: value.slice(0, maxLen), truncated: value.length > maxLen };
  }

  // --------------------------------------------------------------- rendering

  function buildEntry(record) {
    const level = normalizeLevel(record.level);
    const time = formatTime(record.time);
    const rawMessage = record.msg == null ? "" : record.msg;
    const message = typeof rawMessage === "string" ? rawMessage : formatValue(rawMessage);

    const entry = document.createElement("div");
    entry.className = "entry";
    entry.dataset.level = level;
    entry.record = record;

    let hasLongContent = false;
    let messageTruncated = false;

    const timeEl = document.createElement("time");
    timeEl.className = "entry__time";
    timeEl.textContent = time.display;
    if (time.iso) timeEl.dateTime = time.iso;
    if (time.title) timeEl.title = time.title;

    const levelEl = document.createElement("span");
    levelEl.className = "entry__level";
    levelEl.textContent = level.toUpperCase();

    const body = document.createElement("div");
    body.className = "entry__body";

    const msgEl = document.createElement("span");
    msgEl.className = "entry__msg";
    if (message === "") {
      msgEl.classList.add("entry__msg--empty");
      msgEl.textContent = "(no message)";
    } else {
      const lines = message.split("\n");
      if (lines.length > PREVIEW_MSG_LINES) {
        hasLongContent = true;
        messageTruncated = true;
        msgEl.classList.add("entry__msg--truncated");
        msgEl.textContent = lines.slice(0, PREVIEW_MSG_LINES).join("\n");
      } else {
        msgEl.textContent = message;
      }
    }
    body.appendChild(msgEl);

    if (messageTruncated) {
      const expandEl = document.createElement("span");
      expandEl.className = "entry__expand";
      expandEl.textContent = "Click to expand";
      body.appendChild(expandEl);
    }

    const haystack = [time.display, level, message];

    const pairs = recordFields(record);
    if (pairs.length > 0) {
      const fieldsEl = document.createElement("span");
      fieldsEl.className = "entry__fields";
      for (const [name, value] of pairs) {
        const field = document.createElement("span");
        field.className = "field";

        const keyEl = document.createElement("span");
        keyEl.className = "field__key";
        keyEl.textContent = name;

        const valueEl = document.createElement("span");
        valueEl.className = "field__val";
        const valuePreview = truncatePreview(value, PREVIEW_FIELD_LENGTH);
        if (valuePreview.truncated) {
          hasLongContent = true;
          valueEl.classList.add("field__val--truncated");
          valueEl.textContent = valuePreview.preview + "…";
        } else {
          valueEl.textContent = value;
        }

        field.append(keyEl, document.createTextNode("="), valueEl);
        fieldsEl.appendChild(field);
        haystack.push(name, value);
      }
      body.appendChild(fieldsEl);
    }

    entry.append(timeEl, levelEl, body);

    // Only rows with hidden content invite a click; the modal always shows the
    // untruncated record.
    if (hasLongContent) {
      entry.dataset.hasLongContent = "true";
      entry.tabIndex = 0;
      entry.addEventListener("click", () => openModal(record));
      entry.addEventListener("keydown", (event) => {
        if (event.key !== "Enter" && event.key !== " ") return;
        event.preventDefault();
        openModal(record);
      });
    }

    return {
      el: entry,
      level: level,
      haystack: haystack.join(" ").toLowerCase(),
    };
  }

  function matches(entry) {
    if (state.level !== "all" && entry.level !== state.level) return false;
    if (state.query !== "" && entry.haystack.indexOf(state.query) === -1) return false;
    return true;
  }

  function addEntry(record) {
    const entry = buildEntry(record);
    state.entries.push(entry);
    els.viewer.appendChild(entry.el);

    const visible = matches(entry);
    entry.el.hidden = !visible;
    if (visible) state.visible++;

    // Keep memory bounded: drop the oldest rows once the cap is exceeded.
    while (state.entries.length > maxEntries) {
      const removed = state.entries.shift();
      if (!removed.el.hidden) state.visible--;
      removed.el.remove();
    }

    updateEmptyState();
    scheduleStats();
    if (state.autoScroll) stickToBottom();
  }

  function applyFilters() {
    let visible = 0;
    for (const entry of state.entries) {
      const show = matches(entry);
      entry.el.hidden = !show;
      if (show) visible++;
    }
    state.visible = visible;
    updateEmptyState();
    scheduleStats();
    if (state.autoScroll) stickToBottom();
  }

  function updateEmptyState() {
    const hasEntries = state.entries.length > 0;
    const hasVisible = state.visible > 0;
    els.empty.hidden = hasVisible;
    if (!hasVisible) {
      els.emptyText.textContent = hasEntries
        ? "No entries match the current filters."
        : "Waiting for log entries…";
    }
  }

  function scheduleStats() {
    if (statsScheduled) return;
    statsScheduled = true;
    window.requestAnimationFrame(() => {
      statsScheduled = false;
      updateStats();
    });
  }

  function updateStats() {
    const total = state.entries.length;
    if (total === 0) {
      els.stats.textContent = "No entries";
    } else if (state.visible === total) {
      els.stats.textContent = total.toLocaleString() + (total === 1 ? " entry" : " entries") + " · limit " + maxEntries.toLocaleString();
    } else {
      els.stats.textContent = "Showing " + state.visible.toLocaleString() + " of " + total.toLocaleString() + " entries";
    }
  }

  // -------------------------------------------------------------------- modal

  function isModalOpen() {
    return Boolean(els.modal) && els.modal.classList.contains(MODAL_OPEN_CLASS);
  }

  function openModal(record) {
    if (!els.modal || !els.modalContent) return;

    const level = normalizeLevel(record.level);
    const time = formatTime(record.time);
    const rawMessage = record.msg == null ? "" : record.msg;
    const message = typeof rawMessage === "string" ? rawMessage : formatValue(rawMessage);

    els.modalContent.replaceChildren();
    els.modalContent.dataset.level = level;

    // Timestamp and level summary.
    const meta = document.createElement("div");
    meta.className = "modal__meta";

    const timeEl = document.createElement("span");
    timeEl.className = "modal__meta-time";
    timeEl.textContent = time.title || time.display || "(no timestamp)";
    if (time.iso) timeEl.title = time.iso;

    const levelEl = document.createElement("span");
    levelEl.className = "modal__level";
    levelEl.textContent = level.toUpperCase();

    meta.append(timeEl, levelEl);
    els.modalContent.appendChild(meta);

    // Full, untruncated message.
    const messageSection = document.createElement("section");
    messageSection.className = "modal__section";

    const messageTitle = document.createElement("h3");
    messageTitle.className = "modal__section-title";
    messageTitle.textContent = "Message";

    const messagePre = document.createElement("pre");
    messagePre.className = "modal__pre";
    messagePre.textContent = message === "" ? "(no message)" : message;

    messageSection.append(messageTitle, messagePre);
    els.modalContent.appendChild(messageSection);

    // All fields with their complete values.
    const pairs = recordFields(record);
    if (pairs.length > 0) {
      const fieldsSection = document.createElement("section");
      fieldsSection.className = "modal__section";

      const fieldsTitle = document.createElement("h3");
      fieldsTitle.className = "modal__section-title";
      fieldsTitle.textContent = "Fields";

      const fieldsEl = document.createElement("div");
      fieldsEl.className = "modal__fields";

      for (const [name, value] of pairs) {
        const row = document.createElement("div");
        row.className = "modal__field";

        const keyEl = document.createElement("span");
        keyEl.className = "modal__field-key";
        keyEl.textContent = name;

        const valueEl = document.createElement("span");
        valueEl.className = "modal__field-value";
        valueEl.textContent = value;

        row.append(keyEl, valueEl);
        fieldsEl.appendChild(row);
      }

      fieldsSection.append(fieldsTitle, fieldsEl);
      els.modalContent.appendChild(fieldsSection);
    }

    modalReturnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    els.modal.classList.add(MODAL_OPEN_CLASS);
    document.body.style.overflow = "hidden";
    els.modalContent.scrollTop = 0;
    if (els.modalClose) els.modalClose.focus();
  }

  function closeModal() {
    if (!isModalOpen()) return;

    els.modal.classList.remove(MODAL_OPEN_CLASS);
    document.body.style.overflow = "";
    if (els.modalContent) els.modalContent.replaceChildren();

    const previous = modalReturnFocus;
    modalReturnFocus = null;
    if (previous && previous.isConnected && typeof previous.focus === "function") {
      previous.focus();
    }
  }

  // ---------------------------------------------------------------- scrolling

  function isNearBottom() {
    const el = els.viewer;
    return el.scrollHeight - el.scrollTop - el.clientHeight <= NEAR_BOTTOM_PX;
  }

  function stickToBottom() {
    els.viewer.scrollTop = els.viewer.scrollHeight;
  }

  function setAutoScroll(enabled) {
    const changed = state.autoScroll !== enabled;
    state.autoScroll = enabled;
    updateAutoScrollButton();
    if (enabled && changed) stickToBottom();
  }

  function updateAutoScrollButton() {
    els.autoscroll.textContent = state.autoScroll ? "Pause" : "Resume";
    els.autoscroll.setAttribute("aria-pressed", String(state.autoScroll));
    els.autoscroll.classList.toggle("is-paused", !state.autoScroll);
    els.autoscroll.title = state.autoScroll ? "Pause auto-scroll" : "Resume auto-scroll";
  }

  // --------------------------------------------------------------- connection

  function setStatus(kind, text) {
    els.status.dataset.state = kind;
    els.statusText.textContent = text;
  }

  function connect() {
    if (state.reconnectTimer) {
      window.clearTimeout(state.reconnectTimer);
      state.reconnectTimer = 0;
    }
    if (state.source) {
      state.source.close();
      state.source = null;
    }
    if (typeof window.EventSource === "undefined") {
      setStatus("disconnected", "Live streaming unsupported");
      return;
    }

    setStatus("connecting", "Connecting…");

    let source;
    try {
      source = new window.EventSource(eventsURL);
    } catch (_err) {
      setStatus("disconnected", "Disconnected");
      scheduleReconnect();
      return;
    }
    state.source = source;

    source.onopen = () => {
      state.reconnectAttempts = 0;
      setStatus("connected", "Connected");
    };

    source.onerror = () => {
      if (source.readyState === window.EventSource.CLOSED) {
        setStatus("disconnected", "Disconnected");
        scheduleReconnect();
      } else {
        setStatus("reconnecting", "Reconnecting…");
      }
    };

    source.onmessage = (event) => handleLogLine(event.data);
    source.addEventListener("dropped", (event) => handleDropped(event.data));
  }

  // scheduleReconnect retries only after the browser has permanently closed the
  // EventSource. While it is still CONNECTING the browser retries on its own,
  // and creating a second connection would duplicate every event.
  function scheduleReconnect() {
    if (state.reconnectTimer) return;
    state.source = null;
    state.reconnectAttempts++;
    const delay = Math.min(30000, 1000 * Math.pow(2, Math.min(state.reconnectAttempts - 1, 5)));
    setStatus("reconnecting", "Reconnecting in " + Math.ceil(delay / 1000) + "s…");
    state.reconnectTimer = window.setTimeout(() => {
      state.reconnectTimer = 0;
      connect();
    }, delay);
  }

  function handleLogLine(raw) {
    let record;
    try {
      record = JSON.parse(raw);
    } catch (_err) {
      addEntry({
        time: new Date().toISOString(),
        level: "warn",
        msg: "Unparsed log line",
        raw: raw,
      });
      return;
    }
    if (record === null || typeof record !== "object" || Array.isArray(record)) {
      addEntry({
        time: new Date().toISOString(),
        level: "warn",
        msg: "Unexpected log payload",
        raw: raw,
      });
      return;
    }
    addEntry(record);
  }

  function handleDropped(raw) {
    const amount = parseDropped(raw);
    state.dropped += amount;
    updateDropped();
  }

  function parseDropped(raw) {
    const text = String(raw == null ? "" : raw).trim();
    if (text === "") return 1;
    try {
      const parsed = JSON.parse(text);
      if (typeof parsed === "number" && Number.isFinite(parsed)) {
        return Math.max(0, Math.trunc(parsed));
      }
      if (parsed !== null && typeof parsed === "object") {
        const value = parsed.dropped != null ? parsed.dropped : parsed.count;
        if (typeof value === "number" && Number.isFinite(value)) {
          return Math.max(0, Math.trunc(value));
        }
      }
    } catch (_err) {
      // Fall through to the plain-number form.
    }
    const numeric = Number(text);
    if (Number.isFinite(numeric) && numeric > 0) return Math.trunc(numeric);
    return 1;
  }

  function updateDropped() {
    if (state.dropped <= 0) {
      els.dropped.hidden = true;
      return;
    }
    els.dropped.hidden = false;
    els.dropped.textContent = state.dropped.toLocaleString() + " dropped";
    els.dropped.title = "Entries dropped by the server for this viewer because its queue was full";
  }

  // ------------------------------------------------------------------ controls

  function setLevel(level) {
    state.level = level;
    for (const button of els.filterButtons) {
      const active = button.dataset.level === level;
      button.classList.toggle("is-active", active);
      button.setAttribute("aria-pressed", String(active));
    }
    applyFilters();
  }

  function setQuery(query) {
    state.query = query.trim().toLowerCase();
    applyFilters();
  }

  function bindControls() {
    for (const button of els.filterButtons) {
      button.addEventListener("click", () => setLevel(button.dataset.level));
    }

    els.search.addEventListener("input", () => {
      window.clearTimeout(searchTimer);
      searchTimer = window.setTimeout(() => setQuery(els.search.value), SEARCH_DEBOUNCE_MS);
    });
    els.search.addEventListener("keydown", (event) => {
      if (event.key === "Escape") {
        event.preventDefault();
        window.clearTimeout(searchTimer);
        els.search.value = "";
        setQuery("");
      }
    });

    els.autoscroll.addEventListener("click", () => {
      const enable = !state.autoScroll;
      setAutoScroll(enable);
      if (enable) stickToBottom();
    });

    els.clear.addEventListener("click", () => {
      state.entries = [];
      state.visible = 0;
      els.viewer.replaceChildren();
      updateEmptyState();
      scheduleStats();
    });

    els.viewer.addEventListener("scroll", () => {
      if (isNearBottom()) {
        if (!state.autoScroll) setAutoScroll(true);
      } else if (state.autoScroll) {
        setAutoScroll(false);
      }
    }, { passive: true });

    if (els.modalClose) els.modalClose.addEventListener("click", closeModal);
    if (els.modalBackdrop) els.modalBackdrop.addEventListener("click", closeModal);

    document.addEventListener("keydown", (event) => {
      if (event.key === "Escape" && isModalOpen()) {
        event.preventDefault();
        closeModal();
        return;
      }
      if (event.key !== "/" || event.ctrlKey || event.metaKey || event.altKey) return;
      const target = event.target;
      if (target instanceof HTMLElement && (target.isContentEditable || target.tagName === "INPUT" || target.tagName === "TEXTAREA")) return;
      event.preventDefault();
      els.search.focus();
    });

    window.addEventListener("beforeunload", () => {
      if (state.source) state.source.close();
    });
  }

  function init() {
    bindControls();
    updateAutoScrollButton();
    updateEmptyState();
    updateStats();
    connect();
  }

  init();
})();
