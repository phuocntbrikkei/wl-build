# Build Client & Rikkei Ide (Windows + macOS)

Hướng dẫn build 2 ứng dụng desktop trong repo:

- **Client** (`client/`) — Rikkei LMS Connect, ứng dụng giám sát (Wails/Go).
- **Rikkei Ide** (`theia-ide/`) — IDE dựa trên Eclipse Theia (Electron).

Mỗi thư mục có sẵn script cho cả hai HĐH: `build.ps1` (Windows, PowerShell) và
`build.sh` (macOS/Linux, bash). **Build trên chính HĐH đích** — không cross-compile
giữa Windows ↔ macOS (native module + WebView khác nhau).

---

## 1. Yêu cầu cài đặt

| Công cụ | Client | IDE | Ghi chú |
|---|---|---|---|
| Go ≥ 1.21 | ✅ | – | `go version` |
| Node.js ≥ 18 + npm/yarn | ✅ (npm) | ✅ (yarn) | IDE dùng `yarn` |
| Wails CLI v2 | ✅ | – | `go install github.com/wailsapp/wails/v2/cmd/wails@latest` |
| Windows | WebView2 Runtime | – | thường có sẵn Win10/11 |
| macOS | Xcode Command Line Tools | Xcode CLT | `xcode-select --install` |
| Linux (client) | webkit2gtk, gcc | – | build tag `webkit2_41` |

---

## 2. Build Client (Rikkei LMS Connect)

**Windows:**
```powershell
cd client
.\build.ps1
# -> client\build\bin\RikkeiLmsConnect_v1.3.exe
```

**macOS / Linux:**
```bash
cd client
./build.sh
# macOS: build cả arm64 + amd64 -> client/build/bin/*.app
# Linux: amd64 (-tags webkit2_41)
```

### Khác biệt trên macOS (quan trọng)
Client **cố ý KHÔNG bật proxy hệ thống trên macOS** — cơ chế proxy (bắt URL đã
truy cập + chặn web ở tầng mạng) chỉ cài cho Windows (`internal/netproxy/*_windows.go`,
`internal/proxywatch/*_windows.go`); macOS dùng bản `*_other.go` **no-op**. Mọi
tính năng khác chạy bình thường vì đã có bản `*_darwin.go` thật:

- ✅ Chụp/stream màn hình (`screen_darwin.go`), webcam (`camera_darwin.go` — AVFoundation)
- ✅ Wi-Fi (`wifi_darwin.go`), cửa sổ/ứng dụng đang mở (`window_darwin.go`)
- ✅ Chặn ứng dụng (`blocker_darwin.go`), guard, thi, chat, báo vi phạm, quay video
- ❌ Proxy hệ thống → **không** bắt được "URL đã truy cập" và **không** chặn web ở
  tầng mạng trên macOS. Chặn theo ứng dụng vẫn hoạt động.

Không cần sửa gì để build macOS — build tag tự chọn đúng file.

---

## 3. Build Rikkei Ide

**Windows:**
```powershell
cd theia-ide
.\build.ps1          # build từ đầu (cài deps + tải plugins/python/java + đóng gói)
# hoặc build nhanh khi đã cài sẵn:
.\rebuild.ps1
# -> theia-ide\applications\electron\dist\Rikkei Ide Setup *.exe
```

**macOS / Linux — LẦN ĐẦU khi mới mang folder qua:**
```bash
cd theia-ide
./rebuild.sh --clean-install --full
# --clean-install: xoá node_modules cũ (của Windows) rồi cài lại cho macOS
# --full: tải lại plugins/python/java
# -> theia-ide/applications/electron/dist/*.dmg (+ .zip)
```

**macOS — các lần sau (đã cài xong):**
```bash
./rebuild.sh
```

### Kiến trúc (arm64 vs Intel) — QUAN TRỌNG
Rikkei Ide (Electron) build **theo kiến trúc của máy đang build** — KHÁC client
(client là universal chạy cả 2). Nó nhúng sẵn JRE + Python + ripgrep + native
module theo đúng 1 arch, và các script `download:*` tải theo `process.arch`.

- Build trên **Mac Apple Silicon** → bản **arm64** (chạy máy M1/M2/M3, KHÔNG chạy Intel).
- Build trên **Mac Intel** → bản **x64** (chạy máy Intel).
- Plugin `redhat.java` (có bản theo nền tảng) được **tự chọn đúng OS/arch** bởi
  `scripts/resolve-platform-plugins.js` (chạy trong `download:plugins`), nên
  `./build.sh` trên máy nào ra đúng máy đó.

**Build cho Mac Intel — cách tin cậy nhất: chạy `./build.sh` TRÊN 1 máy Mac Intel.**
Mọi thứ tự bám theo arch của máy nên ra đúng bản x64, không cần chỉnh gì. Không có
máy Intel? Dùng CI có runner Intel (GitHub Actions `macos-13` là Intel; `macos-14+`
là arm64) chạy chính `./build.sh`.

*Cross-build x64 trên máy Apple Silicon* — có sẵn script chạy **toàn bộ build dưới Rosetta**:
```bash
cd theia-ide
./build-mac-intel.sh
```
Script tự re-exec dưới `arch -x86_64` (node x64) rồi build bình thường → toolchain +
native + ffmpeg đều x64 đồng nhất, tránh lỗi `incompatible architecture` khi `theia build`
nạp native. **Yêu cầu**: có Rosetta (`softwareupdate --install-rosetta`) + một Node
**universal/x64** (cài từ nodejs.org `.pkg`; Node arm-only qua brew/nvm KHÔNG chạy x64
→ script sẽ báo và bảo dùng máy Intel/CI).
Sau khi cross-build, quay lại bản arm64: `./rebuild.sh --clean-install --full`.

> Lưu ý: cách cũ (rebuild native x64 nhưng chạy build bằng node arm64) **thất bại** vì
> `theia build` nạp `@theia/ffmpeg` x64 bằng node arm64 → "incompatible architecture".
> Vì vậy phải chạy cả build dưới Rosetta (node x64), hoặc build trên máy Intel.

**Chắc ăn nhất vẫn là: build trên một máy Mac Intel** (`./build.sh`), hoặc CI
GitHub Actions runner `macos-13` (Intel) chạy `./build.sh`.

### Lưu ý macOS khác cho IDE
- **KHÔNG copy `node_modules` từ Windows sang macOS** — chứa native module theo
  nền tảng. Luôn dùng `--clean-install` cho lần build đầu trên máy mới.
- macOS mặc định dùng **bash 3.2**; script đã tránh `declare -A` để chạy được.
- Script tự khôi phục symlink workspace (`ln -sfn`) nên di chuyển thư mục vẫn build được.
- Bản macOS không ký (`-c.mac.identity=null`); mở lần đầu có thể phải chuột phải → Open,
  hoặc `xattr -cr "dist/<tên>.app"`.

---

## 4. Đầu ra

| App | Windows | macOS |
|---|---|---|
| Client | `client/build/bin/RikkeiLmsConnect_v1.3.exe` | `client/build/bin/*.app` |
| Rikkei Ide | `theia-ide/applications/electron/dist/Rikkei Ide Setup*.exe` | `theia-ide/applications/electron/dist/*.dmg` |

Sau khi build xong, upload/dán link các file này vào **lms-admin → Cấu hình giám sát
→ Ứng dụng học tập** để sinh viên tải ở mục "Ứng dụng học tập".
