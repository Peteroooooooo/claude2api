package utils

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"testing"
)

func pngBase64(t *testing.T, w, h int) string {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestImageTokens(t *testing.T) {
	if got := ImageTokens([]string{pngBase64(t, 750, 100)}); got != 100 {
		t.Fatalf("750x100 应为 100 Token，得到 %d", got)
	}
	// 长边 3136 缩到 1568：1568×784/750 ≈ 1639。
	if got := ImageTokens([]string{pngBase64(t, 3136, 1568)}); got != 1639 {
		t.Fatalf("超大图未按长边缩放，得到 %d", got)
	}
	if got := ImageTokens([]string{"not-an-image"}); got != imageFallbackToken {
		t.Fatalf("无法解析时应使用保守估值，得到 %d", got)
	}
}
