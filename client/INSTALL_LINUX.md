# Hướng dẫn Cài đặt & Chạy ứng dụng Simple Care trên Linux

Tài liệu này hướng dẫn cách cài đặt công cụ lập trình, cài đặt thư viện chạy và tiến hành biên dịch ứng dụng Simple Care trên môi trường Linux.

---

## 1. Cho Nhà phát triển (Developer - Build ứng dụng)

### Bước 1: Cài đặt thư viện hệ thống
Cài đặt trình biên dịch C và các thư viện Webview (GTK/WebKit) tùy vào hệ điều hành đang dùng:

*   **Ubuntu / Debian / Linux Mint:**
    ```bash
    sudo apt update
    sudo apt install -y build-essential libgtk-3-dev libwebkit2gtk-4.1-dev libx11-dev pkg-config zenity libcanberra-gtk3-module
    ```

*   **Fedora / RHEL:**
    ```bash
    sudo dnf groupinstall "Development Tools"
    sudo dnf install -y gtk3-devel webkit2gtk4.1-devel libX11-devel pkgconf-pkg-config zenity
    ```

*   **Arch Linux:**
    ```bash
    sudo pacman -Syu --needed base-devel gtk3 webkit2gtk-4.1 libx11 pkgconf zenity
    ```

### Bước 2: Cài đặt Go & Node.js & Wails CLI
Nếu chưa cài đặt bộ công cụ biên dịch:
1.  Tải và cài đặt **Go** (bản 1.23+): [golang.org](https://go.dev/dl/)
2.  Tải và cài đặt **Node.js** (bản 18 hoặc 20 LTS): [nodejs.org](https://nodejs.org/)
3.  Cài đặt **Wails CLI** thông qua Go:
    ```bash
    go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0
    ```

### Bước 3: Tiến hành Biên dịch (Build)
Chạy script tự động phát hiện hệ điều hành để build:
```bash
cd client
chmod +x build.sh
./build.sh
```
Sau khi build xong, file thực thi sẽ nằm tại: `client/build/bin/simple_care_v1.3`

---

## 2. Cho Người dùng cuối (End-user - Chỉ chạy ứng dụng)

Người dùng cuối **không cần** cài đặt Go, Node hay trình biên dịch C. Họ chỉ cần tệp thực thi `simple_care_v1.3` và cài đặt các thư viện đồ họa cơ bản của hệ thống:

*   **Ubuntu / Debian / Linux Mint:**
    ```bash
    sudo apt update
    sudo apt install -y libwebkit2gtk-4.1-0 zenity
    ```

*   **Fedora / RHEL:**
    ```bash
    sudo dnf install -y webkit2gtk4.1 zenity
    ```

*   **Arch Linux:**
    ```bash
    sudo pacman -Sy webkit2gtk-4.1 zenity
    ```

### Lệnh chạy ứng dụng:
Cấp quyền chạy cho file và khởi chạy:
```bash
chmod +x simple_care_v1.3
./simple_care_v1.3
```

---

## 3. Lệnh dọn dẹp bộ nhớ đệm (Clean cache)
Nếu thư mục `client/build/` xuất hiện nhiều thư mục đệm tạm thời (dạng số hexa `00` đến `ff`), bạn có thể dọn dẹp bằng lệnh sau:
```bash
find build -mindepth 1 -maxdepth 1 -type d -name "[0-9a-f][0-9a-f]" -exec rm -rf {} +
```
