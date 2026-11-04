package utils

import (
	"bytes"
	"encoding/base64"
	"image"
	"strings"

	// 注册标准解码器，用于读取图片尺寸（只读 header，不解整张图）。
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// 与 Anthropic 文档一致：长边先缩放到不超过 1568px，再按 宽×高/750 估算 Token。
const (
	imageMaxEdge       = 1568
	imageTokenDivisor  = 750
	imageFallbackToken = 1600 // 无法读取尺寸时的保守估值（约等于一张中等图）。
)

// ImageTokens 估算一组 Base64 图片在上游占用的输入 Token。
// 这是“我们实际发出去的图片”的本地估算，不代表上游计费。
func ImageTokens(images []string) int {
	total := 0
	for _, img := range images {
		total += singleImageTokens(img)
	}
	return total
}

func singleImageTokens(encoded string) int {
	raw := decodeImage(encoded)
	if len(raw) == 0 {
		return imageFallbackToken
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return imageFallbackToken
	}
	w, h := float64(cfg.Width), float64(cfg.Height)
	if longest := max(w, h); longest > imageMaxEdge {
		scale := imageMaxEdge / longest
		w *= scale
		h *= scale
	}
	tokens := int(w*h/imageTokenDivisor + 0.5)
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}

// decodeImage 兼容裸 Base64 与 data URI，返回原始字节。
func decodeImage(encoded string) []byte {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil
	}
	if _, payload, ok := strings.Cut(encoded, ","); ok && strings.HasPrefix(strings.ToLower(encoded), "data:") {
		encoded = payload
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if raw, err := enc.DecodeString(encoded); err == nil {
			return raw
		}
	}
	return nil
}
