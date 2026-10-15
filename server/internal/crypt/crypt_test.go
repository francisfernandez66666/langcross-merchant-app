package crypt

import (
	"bytes"
	"strings"
	"testing"
)

// testSecret 故意用一个像样的长串：真实部署里 LC_SECRET 就是这么长。
const testSecret = "langcross-dev-secret-please-change-in-prod"

func TestEncryptDecryptRoundTrip(t *testing.T) {
	c, err := NewCipher(testSecret)
	if err != nil {
		t.Fatalf("构造密文器失败: %v", err)
	}
	plain := "lx-live-9f8c2b1a7d4e6f0a3c5b8d2e1f4a7c0b"
	blob, err := c.Encrypt(plain)
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	got, err := c.Decrypt(blob)
	if err != nil {
		t.Fatalf("解密失败: %v", err)
	}
	if got != plain {
		t.Errorf("解密结果不一致: want=%q got=%q", plain, got)
	}
}

// 同一明文两次加密必须不同：否则密文可被比对出「两把 Key 是不是同一个」。
func TestEncryptIsRandomized(t *testing.T) {
	c, _ := NewCipher(testSecret)
	a, _ := c.Encrypt("same-plain-text")
	b, _ := c.Encrypt("same-plain-text")
	if bytes.Equal(a, b) {
		t.Error("两次加密结果相同，nonce 未参与随机化")
	}
}

// 篡改密文必须被发现（AEAD 的完整性保证），否则数据库被改后我们会解密出垃圾。
func TestDecryptDetectsTampering(t *testing.T) {
	c, _ := NewCipher(testSecret)
	blob, _ := c.Encrypt("kung-pao-chicken")
	blob[len(blob)-1] ^= 0xFF // 翻转最后一个字节（落在 tag 区）
	if _, err := c.Decrypt(blob); err == nil {
		t.Error("篡改后的密文竟然解密成功了，完整性校验失效")
	}
}

// 换一把服务密钥后，旧密文必须读不出来：这保证「轮换 LC_SECRET」真的能作废旧 Key。
func TestDecryptFailsWithDifferentSecret(t *testing.T) {
	c1, _ := NewCipher(testSecret)
	c2, _ := NewCipher("another-completely-different-secret-value")
	blob, _ := c1.Encrypt("some-api-key")
	if _, err := c2.Decrypt(blob); err == nil {
		t.Error("换了密钥还能解密，密钥派生不起作用")
	}
}

// 过短 / 空白密钥必须拒绝：短密钥等于把暴力枚举成本降到零。
func TestNewCipherRejectsWeakSecret(t *testing.T) {
	for _, s := range []string{"", "   ", "short", "123456789012345"} {
		if _, err := NewCipher(s); err == nil {
			t.Errorf("密钥 %q 应该被拒绝", s)
		}
	}
}

// 畸形密文（空 / 过短 / 随机噪声）都不能 panic，只能返回错误。
func TestDecryptRejectsGarbage(t *testing.T) {
	c, _ := NewCipher(testSecret)
	for _, blob := range [][]byte{nil, {}, []byte("x"), []byte("0123456789ab")} {
		if _, err := c.Decrypt(blob); err == nil {
			t.Errorf("畸形密文 %v 应该报错", blob)
		}
	}
}

func TestMask(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"lx-abcd1234wxyz", "lx-a…wxyz"}, // 常规：前 4 + … + 后 4
		{"short", "*****"},               // 过短整串打星
		{"12345678", "********"},         // 正好 8 位也打星（留头留尾没意义）
	}
	for _, c := range cases {
		if got := Mask(c.in); got != c.want {
			t.Errorf("Mask(%q)=%q，期望 %q", c.in, got, c.want)
		}
	}
	// 脱敏串绝不含完整明文：这条是「脱敏」二字的定义
	m := Mask("lx-abcd1234wxyz")
	if strings.Contains(m, "abcd1234") {
		t.Errorf("脱敏结果泄漏了中段: %s", m)
	}
}
