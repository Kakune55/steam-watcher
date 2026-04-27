const insightsPanel = document.getElementById("insightsPanel");
const refreshButton = document.getElementById("refreshInsightsButton");

let selectedRange = "week";
let selectedFriendID = "";
let currentInsights = null;

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

function formatCaptureTime(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "-";
  return date.toLocaleString();
}

function formatDuration(durationMs) {
  const totalMinutes = Math.max(0, Math.round(durationMs / 60000));
  const hours = Math.floor(totalMinutes / 60);
  const minutes = totalMinutes % 60;
  if (hours <= 0) return `${minutes} 分钟`;
  if (minutes === 0) return `${hours} 小时`;
  return `${hours} 小时 ${minutes} 分`;
}

function rangeLabel(range) {
  if (range === "7d") return "近 7 天";
  if (range === "30d") return "近 30 天";
  return "本周";
}

function avatarHTML(item) {
  const avatarURL = String(item.avatar_url || "");
  const name = String(item.persona_name || "");
  if (avatarURL) {
    return `<img class="insight-avatar" src="${escapeHTML(avatarURL)}" alt="${escapeHTML(name)}">`;
  }
  return `<div class="insight-avatar-fallback">${escapeHTML(initialChar(name))}</div>`;
}

function summaryCard(label, value, sub) {
  return `
    <article class="insight-summary">
      <div class="insight-label">${escapeHTML(label)}</div>
      <div class="insight-value" title="${escapeHTML(value)}">${escapeHTML(value)}</div>
      <div class="insight-sub" title="${escapeHTML(sub)}">${escapeHTML(sub)}</div>
    </article>
  `;
}

function playerRows(items) {
  if (!items.length) return '<div class="empty">这段时间还没有游戏记录。</div>';
  return `<div class="insight-list">${items.map((item, index) => `
    <div class="insight-row insight-player is-clickable${selectedFriendID === item.friend_steam_id ? " is-selected" : ""}" data-friend-id="${escapeHTML(item.friend_steam_id)}">
      <div class="insight-rank">#${index + 1}</div>
      ${avatarHTML(item)}
      <div>
        <div class="insight-name" title="${escapeHTML(item.persona_name)}">${escapeHTML(item.persona_name)}</div>
        <div class="insight-meta" title="${escapeHTML(item.top_game || "无游戏")}">最常玩：${escapeHTML(item.top_game || "无")}</div>
      </div>
      <div class="insight-score">${escapeHTML(formatDuration(item.play_ms))}</div>
    </div>
  `).join("")}</div>`;
}

function playerFocus(friend) {
  if (!friend) {
    return `
      <div class="focus-panel">
        <div class="empty">点击玩家榜里的好友查看个人聚焦。</div>
      </div>
    `;
  }

  const profileLink = friend.profile_url
    ? `<a href="${escapeHTML(friend.profile_url)}" target="_blank" rel="noreferrer">Steam 主页</a>`
    : "无主页链接";

  return `
    <div class="focus-panel">
      <div class="focus-head">
        <div class="focus-person">
          ${avatarHTML(friend)}
          <div>
            <div class="focus-name" title="${escapeHTML(friend.persona_name)}">${escapeHTML(friend.persona_name)}</div>
            <div class="focus-meta">${profileLink}</div>
          </div>
        </div>
        <div class="focus-metrics">
          <div>
            <div class="insight-label">本期游戏时长</div>
            <div class="focus-value">${escapeHTML(formatDuration(friend.play_ms))}</div>
          </div>
          <div>
            <div class="insight-label">最常玩</div>
            <div class="focus-value" title="${escapeHTML(friend.top_game || "暂无")}">${escapeHTML(friend.top_game || "暂无")}</div>
          </div>
        </div>
      </div>
      <div class="insight-grid">
        <div class="insight-block">
          <div class="insight-block-title">个人 Top 游戏</div>
          ${gameRows(friend.top_games || [], { compact: true })}
        </div>
        <div class="insight-block">
          <div class="insight-block-title">个人活跃时段</div>
          ${hourHeat(friend.hour_buckets || [])}
        </div>
      </div>
    </div>
  `;
}

function gameRows(items, options = {}) {
  if (!items.length) return '<div class="empty">这段时间还没有游戏记录。</div>';
  return `<div class="insight-list">${items.map((item, index) => `
    <div class="insight-row">
      <div class="insight-rank">#${index + 1}</div>
      <div>
        <div class="insight-name" title="${escapeHTML(item.game_name)}">${escapeHTML(item.game_name)}</div>
        <div class="insight-meta">${options.compact ? "个人游玩时长" : `${item.player_count} 位好友玩过`}</div>
      </div>
      <div class="insight-score">${escapeHTML(formatDuration(item.play_ms))}</div>
    </div>
  `).join("")}</div>`;
}

function coopRows(items) {
  if (!items.length) return '<div class="empty">还没有发现 2 人以上同时游玩的游戏。</div>';
  return `<div class="insight-list">${items.map((item, index) => `
    <div class="insight-row">
      <div class="insight-rank">#${index + 1}</div>
      <div>
        <div class="insight-name" title="${escapeHTML(item.game_name)}">${escapeHTML(item.game_name)}</div>
        <div class="insight-meta">最高 ${item.max_players} 人同时玩</div>
      </div>
      <div class="insight-score">${item.moments} 次</div>
    </div>
  `).join("")}</div>`;
}

function hourHeat(buckets) {
  const values = Array.isArray(buckets) ? buckets : [];
  const maxValue = Math.max(1, ...values);
  const cells = [];
  const labels = [];
  for (let hour = 0; hour < 24; hour += 1) {
    const value = values[hour] || 0;
    cells.push(`<div class="hour-cell" style="--ratio:${(value / maxValue).toFixed(3)}" title="${hour}:00 · ${escapeHTML(formatDuration(value))}"></div>`);
    labels.push(`<div>${hour % 6 === 0 ? `${hour}` : ""}</div>`);
  }
  return `<div class="hour-heat">${cells.join("")}</div><div class="hour-labels">${labels.join("")}</div>`;
}

function rangeButtons(range) {
  return `
    <div class="range-toggle">
      <button class="range-button${range === "week" ? " is-active" : ""}" type="button" data-range="week">本周</button>
      <button class="range-button${range === "7d" ? " is-active" : ""}" type="button" data-range="7d">7天</button>
      <button class="range-button${range === "30d" ? " is-active" : ""}" type="button" data-range="30d">30天</button>
    </div>
  `;
}

function renderInsights(range, insights) {
  currentInsights = insights;
  const topPlayer = insights.top_players?.[0];
  const topGame = insights.popular_games?.[0];
  const focusedFriend = (insights.top_players || []).find((item) => item.friend_steam_id === selectedFriendID);
  const peak = insights.peak || {};
  const peakText = peak.player_count > 0 ? `${peak.player_count} 人游戏中` : "暂无峰值";
  const peakSub = peak.player_count > 0 ? formatCaptureTime(peak.captured_at) : "等待更多采样";

  insightsPanel.innerHTML = `
    <div class="insights-toolbar">
      <div>
        <div class="section-title">统计洞察</div>
        <div class="section-note">${escapeHTML(rangeLabel(range))} · 按快照估算游戏时长</div>
      </div>
      ${rangeButtons(range)}
    </div>
    <div class="insights-body">
      <div class="insight-summary-grid">
        ${summaryCard("游戏时长冠军", topPlayer?.persona_name || "暂无", topPlayer ? formatDuration(topPlayer.play_ms) : "等待更多采样")}
        ${summaryCard("最受欢迎游戏", topGame?.game_name || "暂无", topGame ? `${topGame.player_count} 位好友 · ${formatDuration(topGame.play_ms)}` : "等待更多采样")}
        ${summaryCard("活跃峰值", peakText, peakSub)}
        ${summaryCard("活跃好友", `${insights.active_friend_count || 0} 人`, `总游戏时长 ${formatDuration(insights.total_play_ms || 0)}`)}
      </div>
      <div class="insight-grid">
        <div class="insight-block">
          <div class="insight-block-title">本期游戏时长最多</div>
          ${playerRows(insights.top_players || [])}
        </div>
        <div class="insight-block">
          <div class="insight-block-title">朋友间最受欢迎的游戏</div>
          ${gameRows(insights.popular_games || [])}
        </div>
      </div>
      <div class="insight-grid">
        <div class="insight-block">
          <div class="insight-block-title">黄金游戏时段</div>
          ${hourHeat(insights.hour_buckets || [])}
        </div>
        <div class="insight-block">
          <div class="insight-block-title">最适合组队的游戏</div>
          ${coopRows(insights.coop_games || [])}
        </div>
      </div>
      ${playerFocus(focusedFriend)}
    </div>
  `;
}

function showEmpty(message) {
  insightsPanel.innerHTML = `<div class="empty">${escapeHTML(message)}</div>`;
}

async function loadInsights(range = selectedRange) {
  selectedRange = range;
  showEmpty("正在加载统计洞察...");
  const response = await fetch(`/api/insights?range=${encodeURIComponent(range)}&tz_offset_minutes=${browserTZOffsetMinutes()}`);
  const data = await response.json();
  if (!response.ok) throw new Error(data.error || "加载统计洞察失败");
  renderInsights(data.range || range, data.insights || {});
}

insightsPanel.addEventListener("click", async (event) => {
  const button = event.target.closest("[data-range]");
  if (button) {
    const range = button.getAttribute("data-range");
    if (!range || range === selectedRange) return;
    try {
      await loadInsights(range);
    } catch (error) {
      showEmpty(error.message);
    }
    return;
  }

  const playerRow = event.target.closest("[data-friend-id]");
  if (playerRow) {
    selectedFriendID = playerRow.getAttribute("data-friend-id") || "";
    if (currentInsights) {
      renderInsights(selectedRange, currentInsights);
    }
  }
});

refreshButton.addEventListener("click", async () => {
  refreshButton.disabled = true;
  try {
    await loadInsights(selectedRange);
  } catch (error) {
    showEmpty(error.message);
  } finally {
    refreshButton.disabled = false;
  }
});

loadInsights();
