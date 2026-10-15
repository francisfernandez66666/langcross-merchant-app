// Package service 承载对外部能力的抽象（OCR / 翻译 / 二维码），
// 使本地无外部服务时也能跑通，生产只需换实现。
//
// ── qr.go：门口码 / 桌码生成 ────────────────────────────────
//
// 二维码是把线下客流导入线上菜单的入口，分两种：
//   - 门口码：ref=door，贴在门口，路人扫了看整店菜单；
//   - 桌码：ref=t01 等，贴在桌上，用于定位就餐位并驱动「评价领券」。
//
// 内容格式固定为 {PublicBaseURL}/s/{slug}?ref=xxx：
//   - /s/{slug} 是门店公开页，slug 由 idgen.Slug 生成、永不变更；
//   - ref 用于区分扫码来源（门口 / 具体桌号），是经营洞察的埋点维度。
//
// ★ 不变量：二维码内容一旦印成物料就不可变。因此 slug 与 PublicBaseURL
// 都必须长期稳定；更换域名会导致所有已印刷的物料失效。
package service

import (
	"net/http"

	qrcode "github.com/skip2/go-qrcode"
)

// WriteQR 生成二维码 PNG 并写入响应。
// 用于门口码 / 桌码（内容为 {PublicBaseURL}/s/{slug}?ref=...），物料印刷后内容不可变。
//
// size 为像素边长；非法值（<=0 或 >1024）回落到默认 360。
// 纠错级别固定为 Medium：在「贴纸磨损 / 手机拍摄角度」与「码面密度」之间取平衡。
// 响应带 1 天公共缓存：二维码内容长期不变，可放心让浏览器 / CDN 缓存。
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
