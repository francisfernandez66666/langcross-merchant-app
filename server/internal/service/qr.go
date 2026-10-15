package service

import (
	"net/http"

	qrcode "github.com/skip2/go-qrcode"
)

// WriteQR 生成二维码 PNG 并写入响应。
// 用于门口码 / 桌码（内容为 {PublicBaseURL}/s/{slug}?ref=...），物料印刷后内容不可变。
func WriteQR(w http.ResponseWriter, content string, size int) error {
	if size <= 0 || size > 1024 {
		size = 360
	}
	png, err := qrcode.Encode(content, qrcode.Medium, size)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, err = w.Write(png)
	return err
}
