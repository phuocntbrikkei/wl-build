//go:build !darwin

package screen

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"

	"github.com/kbinani/screenshot"
)

// resizeRGBA resizes a *image.RGBA image using Nearest-Neighbor scaling for maximum performance.
func resizeRGBA(src *image.RGBA, width, height int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	srcBounds := src.Bounds()
	dx := srcBounds.Dx()
	dy := srcBounds.Dy()
	minX := srcBounds.Min.X
	minY := srcBounds.Min.Y

	for y := 0; y < height; y++ {
		srcY := minY + (y*dy)/height
		for x := 0; x < width; x++ {
			srcX := minX + (x*dx)/width

			srcOffset := src.PixOffset(srcX, srcY)
			dstOffset := dst.PixOffset(x, y)

			copy(dst.Pix[dstOffset:dstOffset+4], src.Pix[srcOffset:srcOffset+4])
		}
	}
	return dst
}

// CaptureScreen chụp màn hình chính và trả về chuỗi Base64 dạng "data:image/jpeg;base64,..."
// Non-darwin: dùng kbinani/screenshot
// CaptureScreen giữ nguyên hành vi cũ (chất lượng 50) cho các nơi gọi khác
// (bằng chứng vi phạm...). Luồng xem trực tiếp dùng CaptureScreenQ để chỉnh nét.
func CaptureScreen() (string, error) {
	return CaptureScreenQ(50)
}

// CaptureScreenQ chụp và nén JPEG ở mức chất lượng cho trước (kẹp 20..95).
func CaptureScreenQ(quality int) (string, error) {
	if quality < 20 {
		quality = 20
	}
	if quality > 95 {
		quality = 95
	}
	n := screenshot.NumActiveDisplays()
	if n <= 0 {
		return "", fmt.Errorf("no active displays found")
	}

	// Chụp màn hình chính (index 0)
	bounds := screenshot.GetDisplayBounds(0)
	img, err := screenshot.CaptureRect(bounds)
	if err != nil {
		return "", fmt.Errorf("failed to capture screen: %w", err)
	}

	// Tối ưu hóa kích thước ảnh giống dự án Raia:
	// Giới hạn chiều rộng tối đa là 1024px và chiều cao tối đa là 768px để giảm dung lượng mạng.
	dx := bounds.Dx()
	dy := bounds.Dy()

	maxWidth := 1024
	maxHeight := 768

	newWidth := dx
	newHeight := dy

	if dx > maxWidth || dy > maxHeight {
		ratioWidth := float64(maxWidth) / float64(dx)
		ratioHeight := float64(maxHeight) / float64(dy)
		ratio := ratioWidth
		if ratioHeight < ratioWidth {
			ratio = ratioHeight
		}
		newWidth = int(float64(dx) * ratio)
		newHeight = int(float64(dy) * ratio)
	}

	var finalImg image.Image = img
	if newWidth != dx || newHeight != dy {
		finalImg = resizeRGBA(img, newWidth, newHeight)
	}

	var buf bytes.Buffer
	// Chất lượng JPEG do người xem chọn (cao hơn = nét hơn, nặng hơn).
	err = jpeg.Encode(&buf, finalImg, &jpeg.Options{Quality: quality})
	if err != nil {
		return "", fmt.Errorf("failed to encode jpeg: %w", err)
	}

	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())
	return "data:image/jpeg;base64," + encoded, nil
}
