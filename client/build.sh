#!/bin/bash
set -e

# Change directory to client folder if not already there
cd "$(dirname "$0")"

echo "============================================="
echo "            BUILDING WAILS CLIENT            "
echo "============================================="

# Setup Go path for tools like wails
GOPATH=$(go env GOPATH)
if [ -n "$GOPATH" ] && [ -d "$GOPATH/bin" ]; then
    export PATH="$GOPATH/bin:$PATH"
fi
if [ -d "$HOME/go/bin" ]; then
    export PATH="$HOME/go/bin:$PATH"
fi

# Check if wails is installed
WAILS_CMD="wails"
if ! command -v wails &> /dev/null; then
    if [ -f "$HOME/go/bin/wails" ]; then
        WAILS_CMD="$HOME/go/bin/wails"
        echo "[*] Found Wails CLI at $WAILS_CMD"
    else
        echo "[-] Wails CLI not found. Please install Wails first."
        echo "    Run: go install github.com/wailsapp/wails/v2/cmd/wails@latest"
        exit 1
    fi
fi

BUILD_FLAGS=(-clean -ldflags "-s -w")

# Detect host OS
HOST_OS=$(go env GOOS)

if [ "$HOST_OS" = "darwin" ]; then
    # Build UNIVERSAL: 1 file .app chạy NATIVE cả Apple Silicon (arm64) lẫn Intel (amd64).
    # Trước đây build arm64 rồi build amd64 -> đè lên nhau, chỉ còn bản Intel nên máy ARM
    # đòi Rosetta rồi lỗi. Universal (universal2/lipo) giải quyết triệt để.
    # Chốt deployment target = macOS 10.13 (khớp LSMinimumSystemVersion). Nếu KHÔNG đặt,
    # clang lấy mặc định = SDK máy build (macOS 15) -> kbinani/screenshot chọn nhánh
    # ScreenCaptureKit, link kéo theo @rpath/libswiftCoreMedia.dylib mà binary Go không có
    # LC_RPATH -> dyld "Library not loaded ... no LC_RPATH's found", app chết ngay khi mở.
    # Thêm rpath /usr/lib/swift làm lưới an toàn nếu sau này có framework Swift khác.
    # Go 1.25+ bỏ macOS 11 (runtime build cho macOS 12/13) -> app chết trên máy SV macOS 11/12.
    # Bắt buộc Go <= 1.24 (bản cuối chạy macOS 11). go.mod client = 1.23.
    GO_MINOR=$(go env GOVERSION | sed -E 's/^go1\.([0-9]+).*/\1/')
    if [ -n "$GO_MINOR" ] && [ "$GO_MINOR" -ge 25 ] 2>/dev/null; then
        echo "[!] LỖI: đang dùng $(go env GOVERSION) — bản build sẽ KHÔNG chạy trên macOS 11/12."
        echo "    Cài Go 1.24.x rồi build lại (vd: brew install go@1.24; export PATH=\"\$(brew --prefix go@1.24)/bin:\$PATH\")"
        echo "    và đặt GOTOOLCHAIN=local để Go không tự tải bản mới."
        exit 1
    fi
    export GOTOOLCHAIN=local
    export MACOSX_DEPLOYMENT_TARGET=10.13
    export CGO_CFLAGS="${CGO_CFLAGS:-} -mmacosx-version-min=10.13"
    export CGO_LDFLAGS="${CGO_LDFLAGS:-} -mmacosx-version-min=10.13 -Wl,-rpath,/usr/lib/swift"
    echo "[*] Building macOS Universal (arm64 + amd64), min macOS $MACOSX_DEPLOYMENT_TARGET..."
    "$WAILS_CMD" build -platform darwin/universal "${BUILD_FLAGS[@]}"

    # CHẶN CỨNG: binary KHÔNG được link ScreenCaptureKit (không có trên macOS < 12.3 ->
    # "ScreenCaptureKit image not found") hay @rpath/libswift* (thiếu trên macOS 12 ->
    # "libswiftCoreMedia missing"). Chụp màn hình dùng bản vá third_party/screenshot (chỉ
    # CoreGraphics). Phát hiện là DỪNG build — không đóng gói bản sẽ crash trên máy SV.
    for bin in build/bin/*.app/Contents/MacOS/*; do
        [ -f "$bin" ] || continue
        if otool -L "$bin" | grep -qE "ScreenCaptureKit|@rpath/libswift"; then
            echo "[!] LỖI: $bin còn link thư viện chỉ có trên macOS mới:"
            otool -L "$bin" | grep -E "ScreenCaptureKit|@rpath/libswift" || true
            echo "    -> kiểm tra go.mod còn 'replace github.com/kbinani/screenshot => ./third_party/screenshot'"
            exit 1
        fi
        echo "[*] minos: $(otool -l "$bin" | awk '/LC_BUILD_VERSION/{f=1} f&&/minos/{print $2; exit}') — không link ScreenCaptureKit/Swift: OK"
    done
    echo "[+] macOS universal build completed!"

    # ĐÓNG GÓI ĐỂ PHÁT CHO SV — làm luôn ở đây, KHÔNG nén lại bằng cách khác.
    # "Không thể mở ứng dụng" trên máy SV thường do 1 trong 3:
    #  (1) file thực thi mất quyền chạy (+x) khi nén/giải nén qua Windows/zip lạ,
    #  (2) chữ ký hỏng sau khi lipo/universal (Apple Silicon từ chối chạy binary không ký/ký sai),
    #  (3) cờ quarantine khi tải từ web (app chưa notarize).
    # -> ký ad-hoc cả bundle, kiểm tra, rồi nén bằng ditto (giữ +x, symlink, xattr chuẩn Apple).
    APP_DIR="build/bin"
    for app in "$APP_DIR"/*.app; do
        [ -d "$app" ] || continue
        name="$(basename "$app" .app)"
        xattr -cr "$app" 2>/dev/null || true
        chmod +x "$app"/Contents/MacOS/* 2>/dev/null || true
        echo "[*] Ký ad-hoc: $app"
        codesign --force --deep --sign - "$app"
        codesign --verify --deep --strict "$app" && echo "[+] Chữ ký hợp lệ"
        lipo -archs "$app"/Contents/MacOS/* 2>/dev/null | sed 's/^/[*] Kiến trúc: /'
        exe="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleExecutable' "$app/Contents/Info.plist" 2>/dev/null)"
        if [ -n "$exe" ] && [ ! -x "$app/Contents/MacOS/$exe" ]; then
            echo "[!] LỖI: CFBundleExecutable='$exe' không tồn tại/không chạy được trong Contents/MacOS"
            exit 1
        fi

        # Gói phát hành: .zip (ditto) + .dmg (kéo thả vào Applications).
        zip_out="$APP_DIR/${name// /_}-macOS-universal.zip"
        rm -f "$zip_out"
        ditto -c -k --sequesterRsrc --keepParent "$app" "$zip_out"
        echo "[+] ZIP: $zip_out"
        dmg_out="$APP_DIR/${name// /_}-macOS-universal.dmg"
        stage="$(mktemp -d)"
        ditto "$app" "$stage/$(basename "$app")"
        ln -s /Applications "$stage/Applications"
        rm -f "$dmg_out"
        hdiutil create -volname "$name" -srcfolder "$stage" -ov -format UDZO "$dmg_out" >/dev/null
        rm -rf "$stage"
        echo "[+] DMG: $dmg_out"
    done
    echo "[i] Phát cho SV file .dmg (hoặc .zip) ở trên. App CHƯA notarize -> macOS 15 chặn"
    echo "    ('operation not permitted' / 'Không thể mở'). SV chạy 1 lần trong Terminal:"
    echo "      xattr -cr \"/Applications/Rikkei Lms Connect.app\""
    echo "    hoặc: mở app 1 lần -> System Settings -> Privacy & Security -> Open Anyway."
elif [ "$HOST_OS" = "linux" ]; then
    echo "[*] Building Linux Intel/AMD64 (amd64)..."
    "$WAILS_CMD" build -platform linux/amd64 -tags webkit2_41 "${BUILD_FLAGS[@]}"
    echo "[+] Linux amd64 build completed successfully!"
else
    echo "[!] Unsupported host OS: $HOST_OS. Please build manually using 'wails build'."
    exit 1
fi
