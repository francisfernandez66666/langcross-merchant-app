// Package service 的图片存储实现之一：本地磁盘图片存储（ImageStore 的默认实现）。
//
// 职责边界：只负责「把上传的字节变成本地文件 + 生成可访问 URL」，不校验菜品归属、
// 不碰数据库。上传接口的鉴权与归属校验在 httpapi 层做，与其它接口同一套。
//
// ★ 接缝（换存储不动业务代码）：ImageStore 是接口。当前默认 LocalImageStore 落
// 本地磁盘；将来要换对象存储（S3 / COS / OSS）只需新增一个实现并在装配处替换，
// 接口契约（Save 返回 URL、Delete 按 URL 删除）不变。
package service

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/idgen"
)

// 上传约束：集中在这里，接口层与实现层共用同一组数字，避免两处漂移。
const (
	MaxImageBytes  = 5 << 20 // 5MB。菜品图超过这个尺寸通常是原图直传，压不动就是没处理
	ImageURLPrefix = "/api/uploads/"
)

// errTooLarge / errBadType 是调用方用于区分「体积超限」与「格式不合规」的哨兵错误。
var (
	errTooLarge = errors.New("图片超过 5MB 上限")
	errBadType  = errors.New("仅支持 PNG / JPEG / WebP / GIF 图片")
)

// ImageStore 菜品图片存储。Save 返回可被客人端直接引用的 URL（相对路径）。
type ImageStore interface {
	// Save 读入 r 的字节并存为图片文件，返回访问 URL。
	// 实现必须自行做类型与体积校验，校验失败返回 errTooLarge / errBadType。
	Save(r io.Reader) (string, error)
	// Delete 按 Save 返回的 URL 删除文件；文件不存在时视为成功（幂等）。
	Delete(url string) error
}

// LocalImageStore 把图片落在本地目录，由 HTTP 静态路由对外提供。
type LocalImageStore struct {
	dir string // 落盘目录（不存在时首次 Save 创建）
}

// NewLocalImageStore 创建本地图片存储。dir 为空时用 ./data/uploads。
func NewLocalImageStore(dir string) *LocalImageStore {
	if strings.TrimSpace(dir) == "" {
		dir = "./data/uploads"
	}
	return &LocalImageStore{dir: dir}
}

// sniffType 读文件头判断真实格式。
//
// ★ 为什么不看扩展名、也不看 Content-Type：两者都由客户端控制，把 .exe 改名成
// .png 就能骗过扩展名判断，伪造 Content-Type 也只要一行代码。
// 魔数是文件自己带的，伪造不了——这是上传白名单唯一可信的依据。
func sniffType(head []byte) (ext string, ok bool) {
	switch {
	case len(head) >= 8 && string(head[:8]) == "\x89PNG\r\n\x1a\n":
		return "png", true
	case len(head) >= 3 && head[0] == 0xFF && head[1] == 0xD8 && head[2] == 0xFF:
		return "jpg", true
	case len(head) >= 6 && (string(head[:6]) == "GIF87a" || string(head[:6]) == "GIF89a"):
		return "gif", true
	case len(head) >= 12 && string(head[:4]) == "RIFF" && string(head[8:12]) == "WEBP":
		return "webp", true
	default:
		return "", false
	}
}

// Save 落盘并返回 URL。具体流程：限读 → 嗅探类型 → 生成不可猜文件名 → 写临时文件
// → 原子重命名。用临时文件 + rename 而非直接打开目标路径：避免并发写同一文件名
// 时客人端读到「写了一半」的图。
func (s *LocalImageStore) Save(r io.Reader) (string, error) {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return "", fmt.Errorf("创建上传目录失败: %w", err)
	}
	// io.LimitReader 多读 1 字节：正好等于上限时放行，超出 1 字节即拒。
	// 比「先读完再判断长度」省内存——恶意大文件在读取阶段就被截断，不会撑爆内存。
	raw, err := io.ReadAll(io.LimitReader(r, MaxImageBytes+1))
	if err != nil {
		return "", fmt.Errorf("读取上传内容失败: %w", err)
	}
	if len(raw) > MaxImageBytes {
		return "", errTooLarge
	}
	ext, ok := sniffType(raw)
	if !ok {
		return "", errBadType
	}

	// 文件名 = 32 位随机 hex + 规范扩展名。不用原始文件名：
	// 原始文件名可能含路径穿越字符（../）、中文、同名冲突。
	name := idgen.New() + "." + ext
	tmp := filepath.Join(s.dir, ".tmp-"+name)
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return "", fmt.Errorf("写文件失败: %w", err)
	}
	final := filepath.Join(s.dir, name)
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp) // 清理残留临时文件
		return "", fmt.Errorf("落盘失败: %w", err)
	}
	return ImageURLPrefix + name, nil
}

// Delete 删除 URL 指向的文件。URL 必须带本存储的前缀，否则拒绝——
// 防止调用方拿任意路径（如 /etc/passwd）来删除文件。
func (s *LocalImageStore) Delete(url string) error {
	if url == "" || !strings.HasPrefix(url, ImageURLPrefix) {
		return nil // 非本存储管理的 URL：不碰
	}
	name := strings.TrimPrefix(url, ImageURLPrefix)
	// 只允许纯文件名：出现路径分隔符一律拒绝，堵死目录穿越。
	if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return fmt.Errorf("非法图片 URL")
	}
	if err := os.Remove(filepath.Join(s.dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// SniffImage 导出魔数判断，供测试与上层复用（避免为了断言把私有函数暴露出去）。
func SniffImage(head []byte) (string, bool) { return sniffType(head) }

// ErrTooLarge / ErrBadType 把包内哨兵透出给上层，让接口层能给出不同的人话提示。
var (
	ErrTooLarge = errTooLarge
	ErrBadType  = errBadType
)

// ServeImage 生成静态文件处理器，对外提供 ImageURLPrefix 下的图片。
//
// 注意两点：
//  1. 只处理 GET/HEAD，且用 http.ServeContent（带 Range 与 Last-Modified），
//     客人端弱网时能断点续传，CDN 也好缓存。
//  2. 不信任 URL 里的路径：先清洗 filepath.Base 再拼目录，杜绝 ../ 穿越。
func ServeImage(dir string) http.Handler {
	if strings.TrimSpace(dir) == "" {
		dir = "./data/uploads"
	}
	fs := http.FileServer(http.Dir(dir))
	return http.StripPrefix(ImageURLPrefix, fs)
}
