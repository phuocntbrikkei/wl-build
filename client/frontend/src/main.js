import './style.css';
import './app.css';
import logoUrl from './assets/logo.jpeg';
import {
  decodeExamFilePayload,
  detectExamViewKind,
  renderSecureImage,
  renderSecurePdf,
  renderSecureText,
} from './examViewer.js';
import * as GoApp from '../wailsjs/go/main/App.js';

// Map Wails bindings to window.go.main.App for backward compatibility with obfuscated builds
window.go = window.go || {};
window.go.main = window.go.main || {};
if (typeof window.go.main.App === 'undefined') {
  window.go.main.App = GoApp;
  window.go.main._custom = true;
}

const brandLogoHtml = `<img src="${logoUrl}" alt="Rikkei Lms Connect" class="brand-logo-img" />`;
const APP_VERSION = '1.3';

// Avatar mặc định (SVG nội tuyến, không phụ thuộc mạng) — dùng khi SV chưa có ảnh hoặc URL ảnh lỗi.
const DEFAULT_AVATAR = 'data:image/svg+xml;utf8,' + encodeURIComponent(
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">' +
  '<rect width="100" height="100" rx="50" fill="#e2e8f0"/>' +
  '<circle cx="50" cy="38" r="18" fill="#94a3b8"/>' +
  '<path d="M50 60c-20 0-34 12-34 28v6c0 3 2 6 6 6h56c4 0 6-3 6-6v-6c0-16-14-28-34-28z" fill="#94a3b8"/>' +
  '</svg>'
);

// Trạng thái cục bộ
let loggedIn = false;
let studentInfo = null;
let stats = {
  wifiSSID: '',
  onlineSecs: 0,
  offlineSecs: 0,
  wsConnected: false,
  serverReachable: false,
  allowedApps: '',
  monitorMode: '',
  monitorLabel: '',
  className: '',
  classCode: '',
  currentPeriod: 0,
  currentCourseName: '',
  shifts: [],
  exam: null
};

let chatOpen = false;
let chatMessages = [];
let chatConversations = [];
let activeStaffId = 0;
let lastRenderedMsgCount = 0;
let bannerStaffId = 0;

// Quản lý webcam
let webcamStream = null;
let webcamInterval = null;
const webcamVideo = document.createElement('video');
webcamVideo.autoplay = true;
webcamVideo.playsInline = true;
webcamVideo.style.display = 'none';
document.body.appendChild(webcamVideo);

const webcamCanvas = document.createElement('canvas');
webcamCanvas.width = 320;
webcamCanvas.height = 240;
webcamCanvas.style.display = 'none';
document.body.appendChild(webcamCanvas);

function init() {
  const ready = (typeof window.ObfuscatedCall === 'function') || 
                (typeof window.go !== 'undefined' && typeof window.go.main !== 'undefined' && !window.go.main._custom);
                
  if (!ready || typeof window.runtime === 'undefined') {
    setTimeout(init, 200);
    return;
  }

  window.runtime.EventsOn('start_webcam_stream', startWebcam);
  window.runtime.EventsOn('stop_webcam_stream', stopWebcam);
  window.runtime.EventsOn('chat:message', (data) => {
    updateChatBadge();
    loadChatConversations().catch(console.error);
    if (chatOpen && activeStaffId) {
      loadChatMessages(activeStaffId, true).catch(console.error);
    }
  });
  window.runtime.EventsOn('chat:notify', (data) => {
    updateChatBadge();
    if (data?.staffId) bannerStaffId = Number(data.staffId);
    pulseChatFab();
  });
  window.runtime.EventsOn('exam:paper-sent', () => {
    if (loggedIn) {
      examPaperFiles = null;
      examPaperFilesRoomId = 0;
      lastExamPanelKey = '';
      window.go.main.App.GetStats().then((s) => {
        stats = { ...stats, ...s };
        updateExamPanel(true);
      }).catch(console.error);
    }
  });
  checkPermissionsGate();
}

// Cổng quyền: phải đủ quyền Wi-Fi (Vị trí) + Proxy mới cho vào đăng nhập/giám sát.
async function checkPermissionsGate() {
  let perms = { wifi: false, proxy: false };
  try {
    perms = await window.go.main.App.CheckPermissions();
  } catch (err) {
    console.error('CheckPermissions error:', err);
  }
  if (perms.wifi && perms.proxy) {
    checkLogin();
    return;
  }
  renderPermissionGate(perms);
}

function renderPermissionGate(perms) {
  const rowHtml = (ok, title, desc) => `
    <div class="perm-row" style="display:flex;align-items:flex-start;gap:.75rem;padding:.85rem 0;border-bottom:1px solid #eef0f3;">
      <div style="font-size:1.25rem;line-height:1.4;">${ok ? '✅' : '⛔'}</div>
      <div style="flex:1;">
        <div style="font-weight:700;color:#0f172a;">${title} ${ok ? '<span style="color:#16a34a;font-size:.8rem;">(đã cấp)</span>' : '<span style="color:#dc2626;font-size:.8rem;">(chưa cấp)</span>'}</div>
        <div style="font-size:.82rem;color:#64748b;margin-top:.15rem;">${desc}</div>
      </div>
    </div>`;

  document.querySelector('#app').innerHTML = `
    <div class="login-prompt-container">
      <div class="card">
        <div class="login-brand-row">
          ${brandLogoHtml}
          <div class="login-brand-text">
            <div class="login-brand-name">Rikkei Lms Connect <span class="app-version">v${APP_VERSION}</span></div>
            <div class="login-brand-sub">Rikkei Education</div>
          </div>
        </div>
        <h2 class="login-title">Cần cấp quyền để tiếp tục</h2>
        <p class="login-desc">Ứng dụng giám sát cần đủ các quyền sau mới hoạt động. Vui lòng cấp quyền rồi bấm "Kiểm tra lại".</p>
        ${rowHtml(perms.wifi, 'Quyền Wi-Fi (Vị trí)', 'Cần bật Dịch vụ Vị trí để đọc tên Wi-Fi, đối chiếu mạng lớp học.')}
        ${rowHtml(perms.proxy, 'Quyền Proxy hệ thống', 'Cần cho phép đặt proxy để giám sát truy cập mạng trong giờ học.')}
        <div style="display:flex;gap:.5rem;margin-top:1rem;">
          ${perms.wifi ? '' : '<button class="btn" id="btn-open-location" style="flex:1;padding:.7rem;border:1px solid #cbd5e1;border-radius:.6rem;">Mở cài đặt Vị trí</button>'}
          <button class="btn btn-primary" id="btn-recheck" style="flex:1;padding:.7rem;">Kiểm tra lại</button>
        </div>
      </div>
    </div>
  `;

  const openBtn = document.getElementById('btn-open-location');
  if (openBtn) {
    openBtn.addEventListener('click', () => {
      window.go.main.App.OpenLocationSettings();
    });
  }
  document.getElementById('btn-recheck').addEventListener('click', () => {
    checkPermissionsGate();
  });
}

async function checkLogin() {
  try {
    const isLogged = await window.go.main.App.CheckLoginStatus();
    if (isLogged) {
      loggedIn = true;
      studentInfo = await window.go.main.App.GetStudentInfo();
      renderDashboard();
      startStatsTicker();
      ensureChatWidget();
      updateChatBadge();
      if (stats.monitorMode === 'exam') updateExamPanel();
    } else {
      loggedIn = false;
      renderLoginPrompt();
    }
  } catch (err) {
    console.error('Error checking login status:', err);
  }
}

function renderLoginPrompt() {
  document.querySelector('#app').innerHTML = `
    <div class="login-prompt-container">
      <div class="card">
        <div class="login-brand-row">
          ${brandLogoHtml}
          <div class="login-brand-text">
            <div class="login-brand-name">Rikkei Lms Connect <span class="app-version">v${APP_VERSION}</span></div>
            <div class="login-brand-sub">Rikkei Education</div>
          </div>
        </div>
        <h2 class="login-title">Đăng nhập sinh viên</h2>
        <p class="login-desc">
          Ứng dụng sẽ chuyển hướng bạn đến trang Rikkei Portal. Sau khi đăng nhập thành công, hệ thống sẽ tự động đồng bộ tài khoản học tập của bạn.
        </p>
        <button class="btn btn-primary" id="btn-goto-login" style="width: 100%; padding: 0.75rem;">
          Đi đến trang đăng nhập
        </button>
      </div>
    </div>
  `;

  document.getElementById('btn-goto-login').addEventListener('click', () => {
    window.go.main.App.NavigateToLogin();
  });
}

function monitorModeMeta(mode) {
  switch (mode) {
    case 'learning':
      return { icon: '📚', label: 'Đang học', cls: 'mode-learning' };
    case 'exam':
      return { icon: '📝', label: 'Đang thi', cls: 'mode-exam' };
    case 'outside_schedule':
      return { icon: '😴', label: 'Ngoài giờ học', cls: 'mode-outside' };
    case 'not_configured':
      return { icon: '⚙️', label: 'Chưa sẵn sàng', cls: 'mode-config' };
    default:
      return { icon: '⏳', label: 'Đang kết nối', cls: 'mode-pending' };
  }
}

let examPaperFiles = null;
let examPaperFilesRoomId = 0;
let examPaperFilesLoading = false;
let lastExamPanelKey = '';
let examViewerObjectUrl = null;

function wireExamViewerGuards(overlay) {
  overlay.addEventListener('contextmenu', (e) => e.preventDefault());
  overlay.addEventListener('keydown', (e) => {
    const key = e.key.toLowerCase();
    if ((e.ctrlKey || e.metaKey) && (key === 'p' || key === 's')) {
      e.preventDefault();
    }
  });
}

function ensureExamViewer() {
  let overlay = document.getElementById('exam-viewer-overlay');
  if (overlay) return overlay;
  overlay = document.createElement('div');
  overlay.id = 'exam-viewer-overlay';
  overlay.className = 'exam-viewer-overlay';
  overlay.innerHTML = `
    <div class="exam-viewer-shell">
      <div class="exam-viewer-bar">
        <button type="button" class="btn btn-secondary" id="exam-viewer-close">← Quay lại</button>
        <span class="exam-viewer-title" id="exam-viewer-title"></span>
      </div>
      <div class="exam-viewer-body" id="exam-viewer-body">
        <div class="exam-viewer-content" id="exam-viewer-content"></div>
        <div class="exam-viewer-loading" id="exam-viewer-loading">Đang tải...</div>
        <div class="exam-viewer-error" id="exam-viewer-error"></div>
      </div>
    </div>
  `;
  document.body.appendChild(overlay);
  overlay.querySelector('#exam-viewer-close').addEventListener('click', closeExamViewer);
  wireExamViewerGuards(overlay);
  return overlay;
}

function setExamViewerLoading(loading) {
  const loadingEl = document.getElementById('exam-viewer-loading');
  const contentEl = document.getElementById('exam-viewer-content');
  const errorEl = document.getElementById('exam-viewer-error');
  if (loadingEl) loadingEl.classList.toggle('is-active', loading);
  if (contentEl) contentEl.classList.toggle('is-ready', !loading);
  if (errorEl) errorEl.classList.remove('is-active');
}

function setExamViewerError(message) {
  const loadingEl = document.getElementById('exam-viewer-loading');
  const contentEl = document.getElementById('exam-viewer-content');
  const errorEl = document.getElementById('exam-viewer-error');
  if (loadingEl) loadingEl.classList.remove('is-active');
  if (contentEl) contentEl.classList.remove('is-ready');
  if (errorEl) {
    errorEl.classList.add('is-active');
    errorEl.textContent = message;
  }
}

function resetExamViewerPanels() {
  const loadingEl = document.getElementById('exam-viewer-loading');
  const contentEl = document.getElementById('exam-viewer-content');
  const errorEl = document.getElementById('exam-viewer-error');
  if (loadingEl) loadingEl.classList.remove('is-active');
  if (contentEl) contentEl.classList.remove('is-ready');
  if (errorEl) errorEl.classList.remove('is-active');
}

function clearExamViewerContent() {
  if (examViewerObjectUrl) {
    URL.revokeObjectURL(examViewerObjectUrl);
    examViewerObjectUrl = null;
  }
  const contentEl = document.getElementById('exam-viewer-content');
  if (contentEl) contentEl.innerHTML = '';
}

async function showExamViewer(url, title, fileName = '') {
  const overlay = ensureExamViewer();
  document.getElementById('exam-viewer-title').textContent = title || 'Xem trong app';
  overlay.classList.add('is-open');
  overlay.focus();
  clearExamViewerContent();
  resetExamViewerPanels();
  setExamViewerLoading(true);

  const contentEl = document.getElementById('exam-viewer-content');
  if (!contentEl) return;

  try {
    const payload = await window.go.main.App.LoadExamViewFile(url);
    const bytes = decodeExamFilePayload(payload);
    const mime = payload?.mime || '';
    const kind = detectExamViewKind(fileName || title, mime, bytes);

    if (kind === 'pdf') {
      await new Promise((resolve) => requestAnimationFrame(resolve));
      await renderSecurePdf(contentEl, bytes);
    } else if (kind === 'image') {
      renderSecureImage(contentEl, bytes, mime);
      const img = contentEl.querySelector('img');
      if (img?.src?.startsWith('blob:')) examViewerObjectUrl = img.src;
      if (img) {
        await new Promise((resolve, reject) => {
          if (img.complete) resolve();
          else {
            img.onload = () => resolve();
            img.onerror = () => reject(new Error('Không hiển thị được ảnh'));
          }
        });
      }
    } else if (kind === 'text') {
      renderSecureText(contentEl, bytes);
    } else {
      setExamViewerError('Chỉ hỗ trợ xem PDF, ảnh hoặc file text trong app.');
      return;
    }
    setExamViewerLoading(false);
  } catch (e) {
    setExamViewerError(e?.message || e || 'Không mở được file');
  }
}

function closeExamViewer() {
  const overlay = document.getElementById('exam-viewer-overlay');
  if (!overlay) return;
  overlay.classList.remove('is-open');
  clearExamViewerContent();
  resetExamViewerPanels();
}

function renderExamResources() {
  const ex = stats.exam;
  if (!ex?.paperSent) return '';

  if (examPaperFilesLoading && !examPaperFiles) {
    return `
      <div class="exam-resources">
        <div class="exam-resources-title">📎 Tài nguyên kèm đề</div>
        <p class="exam-resources-hint">Đang tải danh sách tài nguyên...</p>
      </div>
    `;
  }

  const resources = Array.isArray(examPaperFiles?.resources) ? examPaperFiles.resources : [];
  if (!resources.length) {
    return `
      <div class="exam-resources">
        <div class="exam-resources-title">📎 Tài nguyên kèm đề</div>
        <p class="exam-resources-hint">Gói đề này không có tài nguyên đính kèm.</p>
      </div>
    `;
  }

  const rows = resources.map((r) => `
    <div class="exam-resource-row">
      <span class="exam-resource-name" title="${escapeHtml(r.fileName || '')}">${escapeHtml(r.fileName || 'Tài nguyên')}</span>
      <button type="button" class="btn btn-secondary btn-sm btn-exam-res-dl" data-id="${r.id}">Tải về</button>
    </div>
  `).join('');
  return `
    <div class="exam-resources">
      <div class="exam-resources-title">📎 Tài nguyên kèm đề (${resources.length})</div>
      ${rows}
    </div>
  `;
}

async function loadExamPaperFiles(force = false) {
  const ex = stats.exam;
  if (!ex || stats.monitorMode !== 'exam' || !ex.paperSent) {
    examPaperFiles = null;
    examPaperFilesRoomId = 0;
    examPaperFilesLoading = false;
    return;
  }
  if (!force && examPaperFiles && examPaperFilesRoomId === ex.examRoomId) {
    return;
  }
  examPaperFilesLoading = true;
  try {
    examPaperFiles = await window.go.main.App.GetExamPaperFiles();
    examPaperFilesRoomId = ex.examRoomId;
  } catch (err) {
    console.error('GetExamPaperFiles failed:', err);
    examPaperFiles = { resources: [] };
    examPaperFilesRoomId = ex.examRoomId;
  } finally {
    examPaperFilesLoading = false;
  }
}

function renderExamPanel() {
  const ex = stats.exam;
  if (!ex || stats.monitorMode !== 'exam') return '';
  const paperBtn = ex.paperSent
    ? `<button type="button" class="btn btn-primary" id="btn-exam-paper">📄 Xem đề${ex.paperTitle ? ` (${ex.paperTitle})` : ''}</button>`
    : `<span class="exam-wait">Chờ giảng viên gửi đề...</span>`;
  const quizBtn = ex.quizUrl
    ? `<button type="button" class="btn btn-secondary" id="btn-exam-quiz">📝 Làm trắc nghiệm</button>`
    : '';
  const submitBtn = ex.submitted
    ? `<span class="exam-done">✓ Đã nộp bài tự luận</span>`
    : `<button type="button" class="btn btn-primary" id="btn-exam-submit">📦 Nộp bài (chọn folder)</button>`;
  return `
    <div class="card exam-panel" id="exam-panel">
      <div class="card-title-row">
        <div class="card-title">📝 ${ex.examName || 'Phòng thi'}</div>
      </div>
      <p class="exam-panel-desc">Đề PDF và tài nguyên xem trong app. Trắc nghiệm mở trang quiz trực tiếp (giữ đăng nhập). F5 / Xóa cache / Về trang chính: menu <strong>Rikkei Lms Connect</strong> (F5, Ctrl+Delete, Ctrl+H).</p>
      <div class="exam-panel-actions">
        ${paperBtn}
        ${quizBtn}
        ${submitBtn}
      </div>
      ${renderExamResources()}
    </div>
  `;
}

function wireExamPanel() {
  const ex = stats.exam;
  const paper = document.getElementById('btn-exam-paper');
  if (paper) paper.addEventListener('click', async () => {
    try {
      const url = await window.go.main.App.GetExamPaperViewURL();
      showExamViewer(url, ex?.paperTitle || 'Đề thi', ex?.paperTitle || 'de.pdf');
    } catch (e) {
      alert(e?.message || e || 'Không mở được đề');
    }
  });
  const quiz = document.getElementById('btn-exam-quiz');
  if (quiz) quiz.addEventListener('click', () => {
    window.go.main.App.OpenExamQuiz().catch((e) => alert(e?.message || e));
  });
  const submit = document.getElementById('btn-exam-submit');
  if (submit) submit.addEventListener('click', async () => {
    try {
      const name = await window.go.main.App.SubmitExamWork();
      alert(`Đã nộp bài: ${name}`);
      const fresh = await window.go.main.App.GetStats();
      stats = { ...stats, ...fresh };
      await loadExamPaperFiles();
      updateExamPanel();
    } catch (e) {
      alert(e?.message || e || 'Nộp bài thất bại');
    }
  });
  document.querySelectorAll('.btn-exam-res-dl').forEach((btn) => {
    btn.addEventListener('click', async () => {
      const id = Number(btn.getAttribute('data-id'));
      try {
        await window.go.main.App.DownloadExamResource(id);
      } catch (e) {
        alert(e?.message || e || 'Tải thất bại');
      }
    });
  });
}

async function updateExamPanel(forceReloadFiles = false) {
  const host = document.getElementById('exam-panel-host');
  if (!host) return;
  if (stats.monitorMode !== 'exam' || !stats.exam) {
    host.innerHTML = '';
    lastExamPanelKey = '';
    return;
  }
  await loadExamPaperFiles(forceReloadFiles);
  host.innerHTML = renderExamPanel();
  wireExamPanel();
}

function examPanelKey() {
  const ex = stats.exam;
  if (!ex) return '';
  const resCount = Array.isArray(examPaperFiles?.resources) ? examPaperFiles.resources.length : -1;
  return `${ex.examRoomId}:${ex.paperSent}:${ex.submitted}:${resCount}:${examPaperFilesLoading}`;
}

function renderDashboard() {
  if (!studentInfo) return;

  const mode = monitorModeMeta(stats.monitorMode);
  const classLine = stats.className
    ? `${stats.className}${stats.classCode ? ` (${stats.classCode})` : ''}`
    : 'Đang xác định lớp...';

  document.querySelector('#app').innerHTML = `
    <div class="dashboard-wrapper">
      <header class="header">
        <div class="brand">
          ${brandLogoHtml}
          <div class="brand-text">
            <span class="brand-name">Rikkei Lms Connect <span class="app-version">v${APP_VERSION}</span></span>
            <span class="brand-sub">Rikkei Education</span>
          </div>
        </div>
        <div class="header-actions">
          <button class="btn btn-browser" id="btn-browser" title="Mở trình duyệt. Trong giờ học và giờ thi chỉ mở được các trang giảng viên cho phép.">Trình duyệt</button>
          <button class="btn btn-logout" id="btn-logout">Đăng xuất</button>
        </div>
      </header>

      <div class="main-grid">
        <div class="card profile-card">
          <div class="avatar-container">
            <img src="${studentInfo.avatar || DEFAULT_AVATAR}" alt="Avatar" class="avatar" onerror="this.onerror=null;this.src='${DEFAULT_AVATAR}';" />
            <div class="status-indicator ${stats.serverReachable ? 'online' : 'offline'}"></div>
          </div>
          <h2 class="student-name">${studentInfo.fullName}</h2>
          <div class="student-code">${studentInfo.studentCode}</div>
          <p class="student-email">${studentInfo.email}</p>
          <div class="divider"></div>
          <div class="profile-info-row">
            <span>Lớp</span>
            <strong id="profile-class">${classLine}</strong>
          </div>
          <div class="profile-info-row">
            <span>Điện thoại</span>
            <strong>${studentInfo.phone || 'Chưa cung cấp'}</strong>
          </div>
        </div>

        <div class="right-column">
          <div class="card status-banner ${mode.cls}">
            <div class="status-banner-icon" id="status-icon">${mode.icon}</div>
            <div class="status-banner-main">
              <div class="status-banner-top">
                <span class="monitor-mode-tag" id="monitor-mode-tag">${mode.label}</span>
                <span class="status-class-line" id="status-class-line">${classLine}</span>
              </div>
              <div class="status-banner-value" id="status-message">${stats.monitorLabel || stats.statusMsg || 'Đang kết nối...'}</div>
              <div class="status-banner-sub" id="status-sub">
                ${stats.currentPeriod > 0 && stats.currentCourseName
                  ? `Ca ${stats.currentPeriod} · ${stats.currentCourseName}`
                  : 'Theo dõi theo lịch học của lớp'}
              </div>
            </div>
          </div>

          <div id="exam-panel-host">${renderExamPanel()}</div>

          <div class="stats-grid stats-grid--two">
            <div class="card stat-card">
              <div class="stat-icon-wrap">📡</div>
              <div class="stat-info">
                <div class="stat-title">WiFi</div>
                <div class="stat-value" id="wifi-ssid">${stats.wifiSSID || 'Đang quét...'}</div>
              </div>
            </div>
            <div class="card stat-card">
              <div class="stat-icon-wrap">🖥️</div>
              <div class="stat-info">
                <div class="stat-title">Máy chủ</div>
                <div class="stat-value" id="server-status" style="color: ${stats.serverReachable ? 'var(--online-color)' : 'var(--offline-color)'}">
                  ${stats.serverReachable ? 'Đã kết nối' : 'Mất kết nối'}
                </div>
              </div>
            </div>
          </div>

        </div>
      </div>
    </div>
  `;

  document.getElementById('btn-browser').addEventListener('click', () => {
    window.go.main.App.OpenBrowser('').catch((e) => alert(e?.message || e));
  });
  document.getElementById('btn-logout').addEventListener('click', async () => {
    if (confirm('Bạn có chắc chắn muốn đăng xuất khỏi ứng dụng giám sát?')) {
      loggedIn = false;
      studentInfo = null;
      stopWebcam();
      const w = document.getElementById('student-chat-widget');
      if (w) w.remove();
      await window.go.main.App.Logout();
    }
  });
  wireExamPanel();
}

function ensureChatWidget() {
  if (document.getElementById('student-chat-widget')) return;
  const wrap = document.createElement('div');
  wrap.id = 'student-chat-widget';
  wrap.className = 'student-chat-dock';
  wrap.innerHTML = `
    <button type="button" class="student-chat-fab" id="student-chat-fab" title="Tin nhắn">
      💬<span class="student-chat-badge" id="student-chat-badge" hidden>0</span>
    </button>
    <div class="student-chat-modal" id="student-chat-modal">
      <header class="student-chat-modal-head">
        <strong>Tin nhắn</strong>
        <button type="button" class="student-chat-close" id="student-chat-close" aria-label="Đóng">×</button>
      </header>
      <div class="student-chat-modal-body">
        <aside class="student-chat-sidebar" id="student-chat-sidebar"></aside>
        <section class="student-chat-thread">
          <div class="student-chat-thread-head" id="student-chat-thread-head">Chọn hội thoại</div>
          <div class="student-chat-messages" id="student-chat-messages">
            <div class="student-chat-empty">Chọn giảng viên bên trái để xem tin nhắn</div>
          </div>
          <div class="student-chat-compose">
            <input id="student-chat-input" placeholder="Nhập tin nhắn..." />
            <button type="button" class="btn btn-primary btn-sm" id="student-chat-send">Gửi</button>
          </div>
        </section>
      </div>
    </div>
  `;
  document.body.appendChild(wrap);

  document.getElementById('student-chat-fab').addEventListener('click', () => setChatOpen(true));
  document.getElementById('student-chat-close').addEventListener('click', (e) => {
    e.preventDefault();
    e.stopPropagation();
    setChatOpen(false);
  });
  document.getElementById('student-chat-send').addEventListener('click', sendStudentChat);
  document.getElementById('student-chat-input').addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { e.preventDefault(); sendStudentChat(); }
  });
}

function pulseChatFab() {
  const fab = document.getElementById('student-chat-fab');
  if (!fab) return;
  fab.classList.remove('student-chat-fab--pulse');
  void fab.offsetWidth;
  fab.classList.add('student-chat-fab--pulse');
}

function setChatOpen(open) {
  chatOpen = open;
  const modal = document.getElementById('student-chat-modal');
  const fab = document.getElementById('student-chat-fab');
  if (modal) modal.classList.toggle('is-open', open);
  if (fab) fab.classList.toggle('is-hidden', open);
  if (open) {
    loadChatConversations().then(() => {
      const sid = bannerStaffId || activeStaffId || Number(chatConversations[0]?.staffId || 0);
      if (sid) selectStaffChat(sid);
    }).catch(console.error);
  }
}

function selectStaffChat(staffId) {
  if (!staffId) return;
  activeStaffId = staffId;
  const conv = chatConversations.find((c) => Number(c.staffId) === staffId);
  const head = document.getElementById('student-chat-thread-head');
  if (head) head.textContent = conv?.staffName || conv?.staffEmail || 'Giảng viên';
  renderChatConversations();
  loadChatMessages(staffId).catch(console.error);
}

async function loadChatConversations() {
  const rows = await window.go.main.App.GetChatConversations();
  chatConversations = Array.isArray(rows) ? rows : [];
  if (chatOpen) renderChatConversations();
  updateChatBadge();
  return chatConversations;
}

function renderChatConversations() {
  const list = document.getElementById('student-chat-sidebar');
  if (!list) return;
  if (!chatConversations.length) {
    list.innerHTML = '<div class="student-chat-empty">Chưa có tin nhắn</div>';
    return;
  }
  list.innerHTML = chatConversations.map((c) => {
    const sid = Number(c.staffId || 0);
    const unread = Number(c.unread || 0);
    const active = sid === activeStaffId ? ' active' : '';
    const name = escapeHtml(c.staffName || c.staffEmail || 'Giảng viên');
    const preview = escapeHtml(c.lastMessage || '—');
    return `<button type="button" class="student-chat-conv-row${active}" data-staff-id="${sid}">
      <span class="student-chat-conv-name">${name}${unread > 0 ? `<em class="student-chat-conv-unread">${unread}</em>` : ''}</span>
      <span class="student-chat-conv-preview">${preview}</span>
    </button>`;
  }).join('');
  list.querySelectorAll('.student-chat-conv-row').forEach((btn) => {
    btn.addEventListener('click', () => selectStaffChat(Number(btn.getAttribute('data-staff-id'))));
  });
}

async function loadChatMessages(staffId, silent = false) {
  const msgs = await window.go.main.App.GetChatMessages(staffId);
  chatMessages = Array.isArray(msgs) ? msgs : [];
  renderChatMessages(silent);
  updateChatBadge();
  if (!silent) loadChatConversations().catch(console.error);
}

function renderChatMessages(silent = false) {
  const box = document.getElementById('student-chat-messages');
  if (!box) return;
  const wasAtBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 48;
  if (!chatMessages.length) {
    box.innerHTML = '<div class="student-chat-empty">Chưa có tin nhắn</div>';
    lastRenderedMsgCount = 0;
    return;
  }
  if (chatMessages.length === lastRenderedMsgCount && silent) return;
  lastRenderedMsgCount = chatMessages.length;
  box.innerHTML = chatMessages.map((m) => {
    const role = m.senderRole === 'student' ? 'student' : 'staff';
    const time = m.createdAt ? new Date(m.createdAt).toLocaleTimeString('vi-VN', { hour: '2-digit', minute: '2-digit' }) : '';
    return `<div class="student-chat-bubble student-chat-bubble--${role}">
      <div class="student-chat-body">${escapeHtml(m.body || '')}</div>
      <div class="student-chat-time">${time}</div>
    </div>`;
  }).join('');
  if (!silent || wasAtBottom) {
    requestAnimationFrame(() => { box.scrollTop = box.scrollHeight; });
  }
}

function escapeHtml(s) {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

async function sendStudentChat() {
  const input = document.getElementById('student-chat-input');
  if (!input || !input.value.trim() || !activeStaffId) return;
  try {
    await window.go.main.App.SendChatMessage(input.value.trim());
    input.value = '';
    await loadChatMessages(activeStaffId);
  } catch (err) {
    alert(err?.message || err || 'Gửi tin thất bại');
  }
}

async function updateChatBadge() {
  const badge = document.getElementById('student-chat-badge');
  if (!badge || !window.go?.main?.App?.GetChatUnread) return;
  try {
    const n = await window.go.main.App.GetChatUnread();
    if (n > 0) {
      badge.hidden = false;
      badge.textContent = n > 9 ? '9+' : String(n);
    } else {
      badge.hidden = true;
    }
  } catch {
    /* ignore */
  }
}

function startStatsTicker() {
  const pullStats = async () => {
    if (!loggedIn) return;
    try {
      const freshStats = await window.go.main.App.GetStats();
      stats = { ...stats, ...freshStats };
      if (!Array.isArray(stats.shifts)) stats.shifts = [];

      const wifiEl = document.getElementById('wifi-ssid');
      if (wifiEl) wifiEl.innerText = stats.wifiSSID || 'Không có WiFi';

      const serverEl = document.getElementById('server-status');
      if (serverEl) {
        serverEl.innerText = stats.serverReachable ? 'Đã kết nối' : 'Mất kết nối';
        serverEl.style.color = stats.serverReachable ? 'var(--online-color)' : 'var(--offline-color)';
      }

      const statusMsgEl = document.getElementById('status-message');
      if (statusMsgEl) {
        statusMsgEl.innerText = stats.monitorLabel || stats.statusMsg || 'Đang kết nối...';
      }

      const mode = monitorModeMeta(stats.monitorMode);
      const statusIconEl = document.getElementById('status-icon');
      if (statusIconEl) statusIconEl.innerText = mode.icon;

      const modeTagEl = document.getElementById('monitor-mode-tag');
      if (modeTagEl) modeTagEl.innerText = mode.label;

      const banner = document.querySelector('.status-banner');
      if (banner) banner.className = `card status-banner ${mode.cls}`;

      const classLine = stats.className
        ? `${stats.className}${stats.classCode ? ` (${stats.classCode})` : ''}`
        : 'Đang xác định lớp...';
      const classLineEl = document.getElementById('status-class-line');
      if (classLineEl) classLineEl.innerText = classLine;
      const profileClassEl = document.getElementById('profile-class');
      if (profileClassEl) profileClassEl.innerText = classLine;

      const statusSubEl = document.getElementById('status-sub');
      if (statusSubEl) {
        if (stats.monitorMode === 'exam' && stats.exam?.examName) {
          statusSubEl.innerText = stats.exam.examName;
        } else {
          statusSubEl.innerText = stats.currentPeriod > 0 && stats.currentCourseName
            ? `Ca ${stats.currentPeriod} · ${stats.currentCourseName}`
            : 'Theo dõi theo lịch học của lớp';
        }
      }

      if (stats.monitorMode === 'exam' && stats.exam) {
        const panelKey = examPanelKey();
        if (panelKey !== lastExamPanelKey) {
          lastExamPanelKey = panelKey;
          updateExamPanel();
        }
      } else if (lastExamPanelKey !== '') {
        lastExamPanelKey = '';
        const host = document.getElementById('exam-panel-host');
        if (host) host.innerHTML = '';
      }

      const indicator = document.querySelector('.status-indicator');
      if (indicator) {
        indicator.className = `status-indicator ${stats.serverReachable ? 'online' : 'offline'}`;
      }

      updateChatBadge();
    } catch (err) {
      console.error('Error fetching stats:', err);
    }
  };
  pullStats();
  setInterval(pullStats, 1000);
}

async function startWebcam() {
  if (webcamStream) return;
  try {
    webcamStream = await navigator.mediaDevices.getUserMedia({
      video: { width: 320, height: 240, frameRate: { max: 10 } }
    });
    webcamVideo.srcObject = webcamStream;
    webcamInterval = setInterval(() => {
      const ctx = webcamCanvas.getContext('2d');
      if (ctx) {
        ctx.drawImage(webcamVideo, 0, 0, webcamCanvas.width, webcamCanvas.height);
        const dataUrl = webcamCanvas.toDataURL('image/jpeg', 0.4);
        window.go.main.App.SendWebcamFrame(dataUrl);
      }
    }, 500);
  } catch (err) {
    console.error('Failed to open webcam:', err);
  }
}

function stopWebcam() {
  if (webcamInterval) {
    clearInterval(webcamInterval);
    webcamInterval = null;
  }
  if (webcamStream) {
    webcamStream.getTracks().forEach(track => track.stop());
    webcamStream = null;
  }
  webcamVideo.srcObject = null;
}

init();
