const collectButton = document.getElementById("collectButton");
const collectResult = document.getElementById("collectResult");
const friendList = document.getElementById("friendList");
const runList = document.getElementById("runList");
const historyPanel = document.getElementById("historyPanel");
const statTotal = document.getElementById("statTotal");
const statOnline = document.getElementById("statOnline");
const statPlaying = document.getElementById("statPlaying");
const statCaptured = document.getElementById("statCaptured");

let currentStatuses = [];
let selectedFriendID = "";
let selectedView = "halfyear";
let currentHistoryData = null;
let currentDayData = null;
let selectedDrillDate = "";
let mergePresenceSegments = true;
const friendRowNodes = new Map();
let historyShell = null;
let dashboardVersion = "";

function browserTZOffsetMinutes() {
  return -new Date().getTimezoneOffset();
}

function escapeHTML(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

function initialChar(name) {
  return String(name || "?").trim().slice(0, 1).toUpperCase() || "?";
}

function isPlaying(item) {
  return Boolean(item.game_name?.Valid && item.game_name.String);
}

function isOnline(item) {
  return Number(item?.persona_state) >= 1 || isPlaying(item);
}

function isOffline(item) {
  return !isPlaying(item) && !isOnline(item);
}

function toneClass(item) {
  if (isPlaying(item)) return "is-playing";
  if (isOnline(item)) return "is-online";
  return "is-offline";
}

function formatStatus(item) {
  if (isPlaying(item)) {
    return `正在玩：${item.game_name.String}`;
  }
  if (isOffline(item) && item.last_logoff_at?.Valid) {
    return `上次在线 ${relativeTime(item.last_logoff_at.Time)} 前`;
  }
  return item.persona_state_text || "离线";
}

function relativeTime(isoString) {
  const date = new Date(isoString);
  if (Number.isNaN(date.getTime())) return "--";

  const diffMs = Math.max(0, Date.now() - date.getTime());
  const minutes = Math.floor(diffMs / 60000);
  if (minutes < 1) return "刚刚";
  if (minutes < 60) return `${minutes}分钟`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}小时`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days}天`;
  const months = Math.floor(days / 30);
  if (months < 12) return `${months}个月`;
  return `${Math.floor(days / 365)}年`;
}

function formatCaptureTime(isoString) {
  const date = new Date(isoString);
  if (Number.isNaN(date.getTime())) return "-";
  return date.toLocaleString();
}

function formatDuration(durationMs) {
  const totalMinutes = Math.max(0, Math.round(durationMs / 60000));
  const hours = Math.floor(totalMinutes / 60);
  const minutes = totalMinutes % 60;
  if (hours <= 0) return `${minutes} 分钟`;
  if (minutes === 0) return `${hours} 小时`;
  return `${hours} 小时 ${minutes} 分钟`;
}

function formatClock(dateLike) {
  const date = new Date(dateLike);
  if (Number.isNaN(date.getTime())) return "--:--";
  return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

function formatLocalDate(dateLike, options = {}) {
  const date = new Date(dateLike);
  if (Number.isNaN(date.getTime())) return "-";
  return date.toLocaleDateString("zh-CN", options);
}

function formatLocalDateKey(dateLike) {
  const date = new Date(dateLike);
  if (Number.isNaN(date.getTime())) return "";
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  return `${year}-${month}-${day}`;
}

function isActiveTimelineState(segment) {
  return segment.state === "is-online" || segment.state === "is-playing";
}

function mergeExactSegments(segments) {
  const merged = [];
  for (const segment of segments) {
    const last = merged[merged.length - 1];
    if (
      last &&
      last.state === segment.state &&
      last.stateText === segment.stateText &&
      last.gameName === segment.gameName &&
      last.endAt.getTime() === segment.startAt.getTime()
    ) {
      last.endAt = segment.endAt;
      last.samples.push(...segment.samples);
      continue;
    }
    merged.push({
      ...segment,
      samples: [...segment.samples]
    });
  }
  return merged;
}

function mergePresenceRelaxed(segments) {
  const bridged = [];

  for (let i = 0; i < segments.length; i += 1) {
    const current = {
      ...segments[i],
      samples: [...segments[i].samples]
    };

    if (current.state === "is-playing" && current.gameName) {
      let j = i + 1;
      let absorbedUntil = i;
      while (j < segments.length) {
        const gap = [];
        while (
          j < segments.length &&
          isActiveTimelineState(segments[j]) &&
          !segments[j].gameName &&
          (gap.length === 0
            ? segments[j].startAt.getTime() === current.endAt.getTime()
            : segments[j].startAt.getTime() === gap[gap.length - 1].endAt.getTime())
        ) {
          gap.push(segments[j]);
          j += 1;
        }
        if (
          gap.length === 0 ||
          j >= segments.length ||
          segments[j].state !== "is-playing" ||
          segments[j].gameName !== current.gameName ||
          segments[j].startAt.getTime() !== gap[gap.length - 1].endAt.getTime()
        ) {
          break;
        }

        current.endAt = segments[j].endAt;
        for (const segment of gap) {
          current.samples.push(...segment.samples);
        }
        current.samples.push(...segments[j].samples);
        absorbedUntil = j;
        j += 1;
      }
      if (absorbedUntil > i) {
        bridged.push(current);
        i = absorbedUntil;
        continue;
      }
    }

    bridged.push(current);
  }

  const merged = [];
  for (const segment of bridged) {
    const last = merged[merged.length - 1];
    if (!last || last.endAt.getTime() !== segment.startAt.getTime()) {
      merged.push(segment);
      continue;
    }

    const bothOnlineLike = last.state === "is-online" && segment.state === "is-online";
    const samePlayingGame = last.state === "is-playing" && segment.state === "is-playing" && last.gameName === segment.gameName;

    if (bothOnlineLike || samePlayingGame) {
      last.endAt = segment.endAt;
      last.samples.push(...segment.samples);
      if (!last.gameName && segment.gameName) {
        last.gameName = segment.gameName;
      }
      if (bothOnlineLike && last.stateText !== segment.stateText) {
        last.stateText = "在线 / 离开";
      }
      continue;
    }

    merged.push(segment);
  }

  return merged;
}

function summarizeSegmentStateText(segment) {
  const labels = [];
  const seen = new Set();
  for (const sample of segment.samples ?? []) {
    const label = String(sample?.persona_state_text || "").trim();
    if (!label || seen.has(label)) continue;
    seen.add(label);
    labels.push(label);
  }
  if (labels.length > 0) {
    return labels.join(" / ");
  }
  return segment.stateText || (segment.state === "is-playing" ? "在线" : "离线");
}

function compareByName(a, b) {
  return String(a.persona_name || "").localeCompare(String(b.persona_name || ""), "zh-CN", {
    sensitivity: "base",
    numeric: true
  });
}

function compareByLastLogoffDesc(a, b) {
  const aTime = a.last_logoff_at?.Valid ? new Date(a.last_logoff_at.Time).getTime() : 0;
  const bTime = b.last_logoff_at?.Valid ? new Date(b.last_logoff_at.Time).getTime() : 0;
  if (bTime !== aTime) return bTime - aTime;
  return compareByName(a, b);
}

function gameBorderColor(item) {
  if (!item.game_name?.Valid || !item.game_name.String) {
    return "var(--cell-line)";
  }

  const source = String(item.game_app_id?.Valid ? item.game_app_id.Int64 : item.game_name.String);
  let hash = 0;
  for (let i = 0; i < source.length; i += 1) {
    hash = (hash * 31 + source.charCodeAt(i)) >>> 0;
  }
  const hue = hash % 360;
  return `hsl(${hue}deg 56% 62%)`;
}

function avatarURLOf(item) {
  return item.avatar_url?.Valid ? String(item.avatar_url.String || "") : "";
}

function syncAvatarNode(host, avatarURL, name, tone, imageClass, fallbackClass) {
  const label = String(name || "");
  const existing = host.firstElementChild;

  if (avatarURL) {
    let image = existing;
    if (!image || image.tagName !== "IMG") {
      image = document.createElement("img");
      host.replaceChildren(image);
    }
    image.className = `${imageClass} ${tone}`;
    if (image.getAttribute("src") !== avatarURL) {
      image.setAttribute("src", avatarURL);
    }
    image.setAttribute("alt", label);
    return;
  }

  let fallback = existing;
  if (!fallback || fallback.tagName === "IMG") {
    fallback = document.createElement("div");
    host.replaceChildren(fallback);
  }
  fallback.className = `${fallbackClass} ${tone}`;
  fallback.textContent = initialChar(label);
}

function createFriendRowNode() {
  const row = document.createElement("button");
  row.type = "button";
  row.className = "friend-row";

  const avatarHost = document.createElement("span");
  const main = document.createElement("div");
  const name = document.createElement("span");
  const status = document.createElement("div");

  main.className = "friend-main";
  name.className = "friend-name";
  status.className = "friend-status";

  main.append(name, status);
  row.append(avatarHost, main);

  row._avatarHost = avatarHost;
  row._name = name;
  row._status = status;
  return row;
}

function updateFriendRowNode(row, item) {
  const tone = toneClass(item);
  row.className = `friend-row${selectedFriendID === item.friend_steam_id ? " is-selected" : ""}`;
  row.dataset.friendId = item.friend_steam_id;
  syncAvatarNode(row._avatarHost, avatarURLOf(item), item.persona_name, tone, "avatar", "avatar-fallback");
  row._name.className = `friend-name ${tone}`;
  row._name.textContent = item.persona_name;
  row._name.title = `SteamID: ${item.friend_steam_id}`;
  row._status.className = `friend-status ${tone}`;
  row._status.textContent = formatStatus(item);
}

function getFriendRowNode(item) {
  let row = friendRowNodes.get(item.friend_steam_id);
  if (!row) {
    row = createFriendRowNode();
    friendRowNodes.set(item.friend_steam_id, row);
  }
  updateFriendRowNode(row, item);
  return row;
}

function renderGroup(title, key, items) {
  if (!items.length) return null;

  const section = document.createElement("section");
  section.className = `friend-group group-${key}`;

  const head = document.createElement("div");
  head.className = "group-head";
  head.textContent = `${title} `;

  const count = document.createElement("span");
  count.className = "group-count";
  count.textContent = `(${items.length})`;

  head.append(count);
  section.append(head);

  items.forEach((item) => {
    section.append(getFriendRowNode(item));
  });

  return section;
}

function showHistoryEmpty(message, preserveShell = false) {
  if (preserveShell && historyShell) {
    historyShell._body.innerHTML = `<div class="history-empty">${escapeHTML(message)}</div>`;
    return;
  }
  historyShell = null;
  historyPanel.innerHTML = `<div class="history-empty">${escapeHTML(message)}</div>`;
}

function ensureHistoryShell() {
  if (historyShell) return historyShell;

  const shell = document.createElement("div");
  shell.className = "history-shell";

  const top = document.createElement("div");
  top.className = "history-top";

  const friend = document.createElement("div");
  friend.className = "history-friend";

  const avatarHost = document.createElement("span");
  const info = document.createElement("div");
  const name = document.createElement("div");
  const sub = document.createElement("div");

  name.className = "history-name";
  sub.className = "history-sub";
  info.append(name, sub);
  friend.append(avatarHost, info);

  const actions = document.createElement("div");
  actions.className = "history-actions";

  top.append(friend, actions);

  const meta = document.createElement("div");
  meta.className = "history-meta";

  const body = document.createElement("div");
  body.className = "history-body";

  shell.append(top, meta, body);
  shell._avatarHost = avatarHost;
  shell._name = name;
  shell._sub = sub;
  shell._actions = actions;
  shell._meta = meta;
  shell._body = body;

  historyPanel.replaceChildren(shell);
  historyShell = shell;
  return shell;
}

function renderStatuses(items) {
  currentStatuses = items;

  if (!items.length) {
    friendRowNodes.clear();
    friendList.innerHTML = '<div class="empty">还没有采样数据。</div>';
    statTotal.textContent = "0";
    statOnline.textContent = "0";
    statPlaying.textContent = "0";
    statCaptured.textContent = "-";
    showHistoryEmpty("还没有可以展示历史的好友数据。");
    return;
  }

  const total = items.length;
  const playingItems = items.filter(isPlaying).sort(compareByName);
  const onlineItems = items.filter((item) => !isPlaying(item) && isOnline(item)).sort(compareByName);
  const offlineItems = items.filter(isOffline).sort(compareByLastLogoffDesc);

  statTotal.textContent = String(total);
  statOnline.textContent = String(playingItems.length + onlineItems.length);
  statPlaying.textContent = String(playingItems.length);
  statCaptured.textContent = formatCaptureTime(items[0].captured_at);

  const nextIDs = new Set(items.map((item) => item.friend_steam_id));
  Array.from(friendRowNodes.keys()).forEach((friendID) => {
    if (!nextIDs.has(friendID)) {
      friendRowNodes.delete(friendID);
    }
  });

  const groups = [
    renderGroup("游戏中", "playing", playingItems),
    renderGroup("在线好友", "online", onlineItems),
    renderGroup("离线", "offline", offlineItems)
  ].filter(Boolean);
  friendList.replaceChildren(...groups);

  if (!selectedFriendID || !nextIDs.has(selectedFriendID)) {
    selectedFriendID = (playingItems[0] || onlineItems[0] || offlineItems[0]).friend_steam_id;
  }
}

function renderRuns(items) {
  if (!items.length) {
    runList.innerHTML = '<div class="empty">还没有采集记录。</div>';
    return;
  }

  runList.innerHTML = items.map((item) => {
    const statusClass = item.status === "success" ? "is-success" : (item.status === "failed" ? "is-failed" : "");
    const errorHTML = item.error_message?.Valid ? `<div class="run-error">${escapeHTML(item.error_message.String)}</div>` : "";
    return `
      <div class="run-row">
        <div class="muted">${escapeHTML(formatCaptureTime(item.started_at))}</div>
        <div>
          <div class="run-status ${statusClass}">${escapeHTML(item.status)}</div>
          <div class="muted">好友 ${item.friend_count} / 获取 ${item.fetched_count}</div>
          ${errorHTML}
        </div>
      </div>
    `;
  }).join("");
}

function cellStateClass(item) {
  if (!item) return "is-empty";
  if (isPlaying(item)) return "is-playing";
  if (isOnline(item)) return "is-online";
  return "is-offline";
}

function cellTooltip(item, bucketDate, view) {
  if (!item) {
    return `${bucketDate.toLocaleString()} 无采样`;
  }
  const title = [bucketDate.toLocaleString()];
  title.push(item.persona_state_text || "离线");
  if (item.game_name?.Valid && item.game_name.String) {
    title.push(`游戏: ${item.game_name.String}`);
  }
  title.push(`采样: ${formatCaptureTime(item.captured_at)}`);
  if (view === "week") {
    title.push("粒度: 小时");
  } else {
    title.push("粒度: 天");
  }
  return title.join(" | ");
}

function startOfLocalDay(date) {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate());
}

function startOfLocalWeek(date) {
  const result = startOfLocalDay(date);
  const day = result.getDay();
  const diff = day === 0 ? -6 : 1 - day;
  result.setDate(result.getDate() + diff);
  return result;
}

function addLocalDays(date, days) {
  const next = new Date(date);
  next.setDate(next.getDate() + days);
  return next;
}

function renderYearQuarterHeatmap(items, start, end, view) {
  const startDate = new Date(start);
  const endDate = new Date(end);
  const base = startOfLocalWeek(startDate);
  const columns = Math.max(1, Math.ceil((startOfLocalDay(endDate) - base) / (7 * 24 * 60 * 60 * 1000)));
  const map = new Map(items.map((item) => [formatLocalDateKey(item.bucket_start), item]));

  const monthLabels = [];
  for (let col = 0; col < columns; col += 1) {
    const cellDate = addLocalDays(base, col * 7);
    monthLabels.push(cellDate.getDate() <= 7 ? `${cellDate.getMonth() + 1}月` : "");
  }

  const cells = [];
  for (let col = 0; col < columns; col += 1) {
    for (let row = 0; row < 7; row += 1) {
      const date = addLocalDays(base, col * 7 + row);
      const bucketKey = formatLocalDateKey(date);
      const item = map.get(bucketKey);
      const inRange = date >= startDate && date < endDate;
      const stateClass = inRange ? cellStateClass(item) : "is-empty";
      const drillDate = formatLocalDateKey(date);
      const selectedClass = selectedDrillDate === drillDate ? " is-selected" : "";
      const borderColor = inRange && item ? gameBorderColor(item) : "var(--cell-line)";
      const attrs = inRange ? `data-drill-date="${escapeHTML(drillDate)}"` : "";
      cells.push(`<div class="calendar-cell ${stateClass}${selectedClass}" ${attrs} title="${escapeHTML(cellTooltip(item, date, view))}" style="border-color:${escapeHTML(borderColor)}"></div>`);
    }
  }

  return `
    <div class="calendar-heatmap">
      <div class="calendar-months" style="grid-template-columns: repeat(${columns}, var(--heatmap-cell-size));">
        ${monthLabels.map((label) => `<div>${escapeHTML(label)}</div>`).join("")}
      </div>
      <div class="calendar-layout">
        <div class="weekday-labels">
          <div class="weekday-label">一</div>
          <div class="weekday-label">二</div>
          <div class="weekday-label">三</div>
          <div class="weekday-label">四</div>
          <div class="weekday-label">五</div>
          <div class="weekday-label">六</div>
          <div class="weekday-label">日</div>
        </div>
        <div class="calendar-grid" style="grid-template-rows: repeat(7, var(--heatmap-cell-size)); grid-template-columns: repeat(${columns}, var(--heatmap-cell-size));">
          ${cells.join("")}
        </div>
      </div>
    </div>
  `;
}

function renderWeekHeatmap(items, start) {
  const startDate = startOfLocalDay(new Date(start));
  const map = new Map(items.map((item) => {
    const date = new Date(item.bucket_start);
    return [`${formatLocalDateKey(date)}-${date.getHours()}`, item];
  }));
  const hourLabels = [];
  for (let hour = 0; hour < 24; hour += 1) {
    hourLabels.push(`<div>${hour % 3 === 0 ? `${hour}:00` : ""}</div>`);
  }

  const rows = [];
  for (let day = 0; day < 7; day += 1) {
    const rowDate = addLocalDays(startDate, day);
    const cells = [];
    for (let hour = 0; hour < 24; hour += 1) {
      const bucket = new Date(rowDate);
      bucket.setHours(hour, 0, 0, 0);
      const item = map.get(`${formatLocalDateKey(bucket)}-${hour}`);
      const stateClass = cellStateClass(item);
      const drillDate = formatLocalDateKey(bucket);
      const selectedClass = selectedDrillDate === drillDate ? " is-selected" : "";
      const borderColor = item ? gameBorderColor(item) : "var(--cell-line)";
      cells.push(`<div class="calendar-cell ${stateClass}${selectedClass}" data-drill-date="${escapeHTML(drillDate)}" title="${escapeHTML(cellTooltip(item, bucket, "week"))}" style="border-color:${escapeHTML(borderColor)}"></div>`);
    }
    rows.push(`
      <div class="week-row">
        <div class="week-day-label">${escapeHTML(formatLocalDate(rowDate, { weekday: "short", month: "numeric", day: "numeric" }))}</div>
        <div class="week-day-grid" style="grid-template-columns: repeat(24, var(--heatmap-cell-size));">
          ${cells.join("")}
        </div>
      </div>
    `);
  }

  return `
    <div class="week-heatmap">
      <div class="hour-labels" style="grid-template-columns: repeat(24, var(--heatmap-cell-size));">
        ${hourLabels.join("")}
      </div>
      ${rows.join("")}
    </div>
  `;
}

function renderDayDrilldown(data) {
  if (!data || !data.items) return "";

  const segments = [];
  const dayStart = new Date(data.start);
  const dayEnd = new Date(data.end);
  const now = new Date();
  const points = (data.items ?? []).map((item) => ({
    ...item,
    capturedAtDate: new Date(item.captured_at)
  })).sort((a, b) => a.capturedAtDate - b.capturedAtDate);

  if (points.length === 0) {
    segments.push({
      startAt: dayStart,
      endAt: dayEnd,
      state: "is-empty",
      gameName: "",
      samples: []
    });
  } else {
    if (points[0].capturedAtDate > dayStart) {
      segments.push({
        startAt: dayStart,
        endAt: points[0].capturedAtDate,
        state: "is-empty",
        gameName: "",
        stateText: "",
        samples: []
      });
    }

    for (let i = 0; i < points.length; i += 1) {
      const point = points[i];
      const nextPoint = points[i + 1];
      const startAt = point.capturedAtDate < dayStart ? dayStart : point.capturedAtDate;
      const endAt = nextPoint ? nextPoint.capturedAtDate : dayEnd;
      if (endAt <= startAt) {
        continue;
      }
      segments.push({
        startAt,
        endAt,
        state: cellStateClass(point),
        gameName: point.game_name?.Valid ? point.game_name.String : "",
        stateText: point.persona_state_text || "离线",
        samples: [point]
      });
    }
  }

  const exactMerged = mergeExactSegments(segments);
  const merged = mergePresenceSegments ? mergePresenceRelaxed(exactMerged) : exactMerged;

  function segmentTitle(segment) {
    if (segment.state === "is-playing") {
      return `正在玩：${segment.gameName}`;
    }
    if (segment.state === "is-online") {
      return segment.stateText || "在线";
    }
    if (segment.state === "is-offline") {
      return segment.stateText || "离线";
    }
    return "无采样";
  }

  function segmentMeta(segment, durationText) {
    if (segment.state === "is-playing") {
      const stateText = summarizeSegmentStateText(segment);
      return `状态：${stateText} · 持续 ${durationText}`;
    }
    return `持续 ${durationText}`;
  }

  const totalMs = Math.max(1, dayEnd.getTime() - dayStart.getTime());
  const isCurrentDay = formatLocalDateKey(dayStart) === formatLocalDateKey(new Date());
  const effectiveDayEnd = isCurrentDay && now < dayEnd ? now : dayEnd;

  const summaryBar = merged.map((segment) => {
    const segmentEnd = segment.endAt.getTime() === dayEnd.getTime() ? effectiveDayEnd : segment.endAt;
    const durationMs = Math.max(0, segmentEnd.getTime() - segment.startAt.getTime());
    const widthPercent = (durationMs / totalMs) * 100;
    const segmentClass = segment.state === "is-playing"
      ? "day-summary-playing"
      : segment.state === "is-online"
        ? "day-summary-online"
        : "day-summary-offline";
    const title = segmentTitle(segment);
    const tip = `${formatClock(segment.startAt)} - ${segment.endAt.getTime() === dayEnd.getTime() ? (isCurrentDay ? "至今" : "24:00") : formatClock(segmentEnd)} | ${title}`;
    return `<div class="day-summary-segment ${segmentClass}" title="${escapeHTML(tip)}" style="width:${widthPercent}%"></div>`;
  }).join("");

  const itemsHTML = merged.map((segment) => {
    const titleClass = segment.state === "is-empty" ? "is-offline" : segment.state;
    const title = segmentTitle(segment);
    const segmentEnd = segment.endAt.getTime() === dayEnd.getTime() ? effectiveDayEnd : segment.endAt;
    const durationMs = Math.max(0, segmentEnd.getTime() - segment.startAt.getTime());

    const durationText = formatDuration(durationMs);
    const metaText = segmentMeta(segment, durationText);
    const startText = formatClock(segment.startAt);
    const endText = segment.endAt.getTime() === dayEnd.getTime()
      ? (isCurrentDay ? "至今" : "24:00")
      : formatClock(segmentEnd);

    return `
      <article class="timeline-item ${titleClass}">
        <div class="timeline-time">${escapeHTML(startText)} - ${escapeHTML(endText)}</div>
        <div class="timeline-card ${titleClass}">
          <div class="timeline-title ${titleClass}">${escapeHTML(title)}</div>
          <div class="timeline-meta">${escapeHTML(metaText)}</div>
        </div>
      </article>
    `;
  }).join("");

  return `
    <section class="day-drilldown">
      <div class="day-drilldown-head">
        <div>
          <div class="day-drilldown-title">当天明细</div>
          <div class="day-drilldown-note">${escapeHTML(formatLocalDate(data.start, { year: "numeric", month: "numeric", day: "numeric" }))} · 24 小时粒度</div>
        </div>
        <label class="day-merge-toggle">
          <input type="checkbox" data-merge-presence-toggle ${mergePresenceSegments ? "checked" : ""}>
          <span>合并在线状态</span>
        </label>
      </div>
      <div class="day-summary-bar">${summaryBar}</div>
      <div class="day-timeline">${itemsHTML}</div>
    </section>
  `;
}

function renderYearSummary(items) {
  const monthMap = new Map();

  for (const item of items) {
    const date = new Date(item.bucket_start);
    const key = `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}`;
    if (!monthMap.has(key)) {
      monthMap.set(key, {
        label: formatLocalDate(date, { year: "numeric", month: "long" }),
        total: 0,
        playing: 0,
        online: 0,
        offline: 0,
        games: new Map()
      });
    }

    const bucket = monthMap.get(key);
    bucket.total += 1;
    if (isPlaying(item)) {
      bucket.playing += 1;
      const gameName = item.game_name.String;
      bucket.games.set(gameName, (bucket.games.get(gameName) || 0) + 1);
    } else if (isOnline(item)) {
      bucket.online += 1;
    } else {
      bucket.offline += 1;
    }
  }

  const cards = Array.from(monthMap.entries()).sort(([a], [b]) => a.localeCompare(b)).map(([, month]) => {
    const topGame = Array.from(month.games.entries()).sort((a, b) => b[1] - a[1])[0];
    const playingRatio = month.playing;
    const onlineRatio = month.online;
    const offlineRatio = month.offline;

    return `
      <article class="month-card">
        <div class="month-name">${escapeHTML(month.label)}</div>
        <div class="month-bar" style="--playing-ratio:${playingRatio}; --online-ratio:${onlineRatio}; --offline-ratio:${offlineRatio};">
          <div class="month-bar-playing"></div>
          <div class="month-bar-online"></div>
          <div class="month-bar-offline"></div>
        </div>
        <div class="month-metrics">
          <div><strong>游戏中</strong> ${month.playing} 天</div>
          <div><strong>在线</strong> ${month.online} 天</div>
          <div><strong>离线</strong> ${month.offline} 天</div>
          <div><strong>最常玩</strong> ${topGame ? escapeHTML(topGame[0]) : "无"}</div>
        </div>
      </article>
    `;
  });

  return `<div class="year-summary">${cards.join("")}</div>`;
}

function renderHistoryPanel(data) {
  const shell = ensureHistoryShell();
  const meta = data.meta;
  const latestStatus = currentStatuses.find((item) => item.friend_steam_id === meta.friend_steam_id);
  const tone = latestStatus ? toneClass(latestStatus) : "is-offline";
  const viewLabel = data.view === "year" ? "最近 1 年总览" : (data.view === "halfyear" ? "最近 6 个月" : "最近 7 天");
  let historyHTML = "";
  if (data.view === "year") {
    historyHTML = renderYearSummary(data.items ?? []);
  } else if (data.view === "week") {
    historyHTML = renderWeekHeatmap(data.items ?? [], data.start);
  } else {
    historyHTML = renderYearQuarterHeatmap(data.items ?? [], data.start, data.end, data.view);
  }
  const profileLink = meta.profile_url?.Valid
    ? `<a href="${escapeHTML(meta.profile_url.String)}" target="_blank" rel="noreferrer">Steam 主页</a>`
    : "无主页链接";

  syncAvatarNode(shell._avatarHost, avatarURLOf(meta), meta.persona_name, tone, "history-avatar", "history-avatar-fallback");
  shell._name.className = `history-name ${tone}`;
  shell._name.textContent = meta.persona_name;
  shell._name.title = `SteamID: ${meta.friend_steam_id}`;
  shell._sub.innerHTML = `${latestStatus ? escapeHTML(formatStatus(latestStatus)) : "暂无最新状态"} | ${profileLink}`;
  shell._actions.innerHTML = `
    <div class="view-toggle">
      <button class="view-button${data.view === "year" ? " is-active" : ""}" type="button" data-view="year">年</button>
      <button class="view-button${data.view === "halfyear" ? " is-active" : ""}" type="button" data-view="halfyear">6月</button>
      <button class="view-button${data.view === "week" ? " is-active" : ""}" type="button" data-view="week">周</button>
    </div>
  `;
  shell._meta.innerHTML = `
    <div><strong>范围</strong> ${escapeHTML(viewLabel)}</div>
    <div><strong>采样点</strong> ${(data.items ?? []).length}</div>
    <div class="legend">
      <span class="legend-item"><span class="legend-swatch" style="background:rgba(139, 197, 63, 0.28); border-color:${escapeHTML(gameBorderColor({ game_name: { Valid: true, String: 'x' }, game_app_id: { Valid: true, Int64: 10 } }))}"></span>游戏中</span>
      <span class="legend-item"><span class="legend-swatch" style="background:rgba(102, 192, 244, 0.24)"></span>在线</span>
      <span class="legend-item"><span class="legend-swatch" style="background:rgba(154, 167, 179, 0.14)"></span>离线</span>
      <span class="legend-item"><span class="legend-swatch"></span>边框区分不同游戏</span>
    </div>
  `;
  shell._body.innerHTML = `
    ${historyHTML}
    ${data.view === "year" ? "" : renderDayDrilldown(currentDayData)}
  `;
}

async function loadDayHistory(friendID, dateKey) {
  selectedDrillDate = dateKey;
  const response = await fetch(`/api/friends/${encodeURIComponent(friendID)}/history?view=day&date=${encodeURIComponent(dateKey)}&tz_offset_minutes=${browserTZOffsetMinutes()}`);
  const data = await response.json();
  if (!response.ok) {
    throw new Error(data.error || "加载当天明细失败");
  }
  currentDayData = data;
  if (currentHistoryData) {
    renderHistoryPanel(currentHistoryData);
  }
}

async function loadHistory(friendID, view = selectedView) {
  const sameFriend = currentHistoryData?.meta?.friend_steam_id === friendID;
  selectedFriendID = friendID;
  selectedView = view;
  currentHistoryData = null;
  currentDayData = null;

  if (currentStatuses.length) {
    renderStatuses(currentStatuses);
  }

  showHistoryEmpty("正在加载历史热力图...", sameFriend);

  const response = await fetch(`/api/friends/${encodeURIComponent(friendID)}/history?view=${encodeURIComponent(view)}&tz_offset_minutes=${browserTZOffsetMinutes()}`);
  const data = await response.json();
  if (!response.ok) {
    throw new Error(data.error || "加载好友历史失败");
  }
  currentHistoryData = data;

  if (view === "year") {
    selectedDrillDate = "";
    renderHistoryPanel(data);
    return;
  }

  const lastPoint = (data.items ?? []).length ? data.items[data.items.length - 1] : null;
  selectedDrillDate = lastPoint ? formatLocalDateKey(lastPoint.bucket_start) : "";
  if (selectedDrillDate) {
    await loadDayHistory(friendID, selectedDrillDate);
  } else {
    renderHistoryPanel(data);
  }
}

function sleep(ms) {
  return new Promise((resolve) => {
    window.setTimeout(resolve, ms);
  });
}

async function syncDashboard(changed, statuses, runs) {
  if (!changed) return;

  renderStatuses(statuses ?? []);
  renderRuns(runs ?? []);

  if (selectedFriendID) {
    await loadHistory(selectedFriendID, selectedView);
  }
}

async function pollDashboard() {
  const query = dashboardVersion ? `?since=${encodeURIComponent(dashboardVersion)}` : "";
  const response = await fetch(`/api/dashboard${query}`, {
    headers: {
      "Accept": "application/json"
    }
  });
  const data = await response.json();
  if (!response.ok) {
    throw new Error(data.error || "同步面板失败");
  }

  if (typeof data.version === "string") {
    dashboardVersion = data.version;
  }

  await syncDashboard(Boolean(data.changed), data.statuses, data.runs);
}

async function startDashboardPolling() {
  for (;;) {
    try {
      await pollDashboard();
    } catch (error) {
      if (!dashboardVersion) {
        collectResult.textContent = "初始化刷新失败";
        showHistoryEmpty(error.message || "初始化失败");
      }
      await sleep(3000);
    }
  }
}

friendList.addEventListener("click", async (event) => {
  const row = event.target.closest("[data-friend-id]");
  if (!row) return;

  const friendID = row.getAttribute("data-friend-id");
  if (!friendID) return;

  try {
    await loadHistory(friendID, selectedView);
  } catch (error) {
    showHistoryEmpty(error.message);
  }
});

historyPanel.addEventListener("click", async (event) => {
  const button = event.target.closest("[data-view]");
  if (button && selectedFriendID) {
    const nextView = button.getAttribute("data-view");
    if (!nextView) return;

    try {
      await loadHistory(selectedFriendID, nextView);
    } catch (error) {
      showHistoryEmpty(error.message);
    }
    return;
  }

  const cell = event.target.closest("[data-drill-date]");
  if (cell && selectedFriendID && selectedView !== "year") {
    const dateKey = cell.getAttribute("data-drill-date");
    if (!dateKey) return;
    try {
      await loadDayHistory(selectedFriendID, dateKey);
    } catch (error) {
      showHistoryEmpty(error.message);
    }
  }
});

historyPanel.addEventListener("change", (event) => {
  const toggle = event.target.closest("[data-merge-presence-toggle]");
  if (!toggle) return;

  mergePresenceSegments = Boolean(toggle.checked);
  if (currentHistoryData) {
    renderHistoryPanel(currentHistoryData);
  }
});

collectButton.addEventListener("click", async () => {
  collectButton.disabled = true;
  collectResult.textContent = "正在采集...";

  try {
    const response = await fetch("/api/collect", { method: "POST" });
    const data = await response.json();
    if (!response.ok) {
      throw new Error(data.error || "采集失败");
    }

    collectResult.textContent = `采集完成：好友 ${data.friend_count}，成功获取 ${data.fetched_count}`;
  } catch (error) {
    collectResult.textContent = error.message;
  } finally {
    collectButton.disabled = false;
  }
});

startDashboardPolling();
