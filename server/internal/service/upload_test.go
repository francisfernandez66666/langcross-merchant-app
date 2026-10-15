package service

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pngMagic 是最小合法 PNG 头（8 字节签名），足以骗过魔数嗅探。
var pngMagic = []byte("\x89PNG\r\n\x1a\n")

func TestSniffImageAcceptAndReject(t *testing.T) {
	cases := []struct {
		name string
		head []byte
		ext  string
		ok   bool
	}{
		{"png", pngMagic, "png", true},
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0x00, 0x01}, "jpg", true},
		{"gif87a", []byte("GIF87a...."), "gif", true},
		{"gif89a", []byte("GIF89a...."), "gif", true},
		{"webp", append(append([]byte{}, []byte("RIFF")...), []byte("....WEBP")...), "webp", true},
		{"exe 改后缀", []byte("MZ\x90\x00\x03"), "", false},
		{"文本", []byte("<?xml version"), "", false},
		{"空", nil, "", false},
	}
	for _, c := range cases {
		ext, ok := SniffImage(c.head)
		if ok != c.ok || (ok && ext != c.ext) {
			t.Errorf("%s: SniffImage 得到 (%q,%v)，期望 (%q,%v)", c.name, ext, ok, c.ext, c.ok)
		}
	}
}

func TestLocalImageStoreSaveAndDelete(t *testing.T) {
	dir := t.TempDir()
	store := NewLocalImageStore(dir)

	url, err := store.Save(bytes.NewReader(append(append([]byte{}, pngMagic...), make([]byte, 64)...)))
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if !strings.HasPrefix(url, ImageURLPrefix) || !strings.HasSuffix(url, ".png") {
		t.Fatalf("返回的 URL 形态不对: %s", url)
	}
	// 文件必须真的落盘（而不是只返回一个 URL）
	name := strings.TrimPrefix(url, ImageURLPrefix)
	if _, serr := os.Stat(filepath.Join(dir, name)); serr != nil {
		t.Errorf("文件未落盘: %v", serr)
	}
	// 删除后文件消失
	if err := store.Delete(url); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, serr := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(serr) {
		t.Error("删除后文件仍然存在")
	}
	// 幂等：再删一次不报错
	if err := store.Delete(url); err != nil {
		t.Errorf("重复删除应幂等，实际报错: %v", err)
	}
}

// ★ 安全：伪造的图片（exe 内容 + .png 名字）必须被拒。
// 唯一可信的依据是文件自己的魔数，扩展名与 Content-Type 都不可信。
func TestLocalImageStoreRejectsFakeImage(t *testing.T) {
	store := NewLocalImageStore(t.TempDir())
	if _, err := store.Save(bytes.NewReader([]byte("MZ\x90\x00 this is an executable"))); !errors.Is(err, ErrBadType) {
		t.Errorf("伪装成图片的非图片应被拒绝，实际 err=%v", err)
	}
}

// 超限文件必须在读取阶段就被截断并拒绝，而不是读进内存再判断。
func TestLocalImageStoreRejectsOversize(t *testing.T) {
	store := NewLocalImageStore(t.TempDir())
	big := append(append([]byte{}, pngMagic...), make([]byte, MaxImageBytes+1)...)
	if _, err := store.Save(bytes.NewReader(big)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("超过 5MB 应被拒绝，实际 err=%v", err)
	}
}

// 目录穿越：Delete 绝不能删掉存储目录以外的文件。
// 分两种情形：
//   - 带本存储前缀但夹带 .. / 分隔符 → 必须报错（明显的攻击）；
//   - 完全不属于本存储的 URL（如 /etc/passwd）→  silently ignore 是刻意设计
//     （旧数据 / 外部图片不归我们管），但绝不能真去删文件。
func TestLocalImageStoreRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	store := NewLocalImageStore(dir)

	// 1) 明显的穿越必须报错
	for _, bad := range []string{
		"/api/uploads/../../etc/passwd",
		"/api/uploads/..\\..\\windows\\system32",
		"/api/uploads/sub/dir.png", // 子目录
	} {
		if err := store.Delete(bad); err == nil {
			t.Errorf("恶劣 URL %q 竟然没有报错", bad)
		}
	}

	// 2) 外来 URL：不报错，但绝不能删到文件
	victim := filepath.Join(dir, "victim.txt")
	if err := os.WriteFile(victim, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, foreign := range []string{"/etc/passwd", "", "/api/uploads"} {
		if err := store.Delete(foreign); err != nil {
			t.Errorf("外来 URL %q 应被忽略而非报错: %v", foreign, err)
		}
	}
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("外来 URL 竟然动了存储目录里的文件: %v", err)
	}
}

// 保存必须原子：写一半的文件不该被客人看到。
// 验证手段：Save 成功后目标文件完整存在，且目录里没有残留临时文件。
func TestLocalImageStoreLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	store := NewLocalImageStore(dir)
	for i := 0; i < 5; i++ {
		if _, err := store.Save(bytes.NewReader(pngMagic)); err != nil {
			t.Fatalf("第 %d 次保存失败: %v", i, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("残留临时文件: %s", e.Name())
		}
	}
	if len(entries) != 5 {
		t.Errorf("应有 5 个文件，实际 %d", len(entries))
	}
}
