package domain

// 本文件：AI 经营助手的 LLM 凭据模型。
//
// 与 translation_api_key（能言翻译 Key）是姊妹设计，但凭据形态不同：
//
//	翻译 Key：一串 key + mode（fast/pro）
//	LLM 凭据：base_url + model + key 三元组（OpenAI 兼容协议）
//
// 为什么单独一张表而不再塞 translation_api_key：字段语义完全不同，
// 硬合并会让「 provider=lexicorn 」这一列变成谎言，也会让脱敏展示难以写对。
//
// 维护权限（与翻译 Key 一致，用户明确要求）：写操作只归平台超管，
// 租户管理员只看脱敏状态。理由：LLM 调用是真金白银的成本，
// 且 key 一旦泄露影响的是平台侧额度，不是单个租户的翻译额度。
//
// 对应迁移：migrations/0006_org_rbac.sql 的 ai_credential 表。

// AICredential 是一份 LLM（OpenAI 兼容）凭据。
type AICredential struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`  // 空串 = 平台级默认（所有租户共用）
	Name      string `json:"name"`       // 别名，便于人识别（如「平台默认 DeepSeek」）
	BaseURL   string `json:"base_url"`   // 如 https://api.deepseek.com/v1
	Model     string `json:"model"`      // 如 deepseek-chat / gpt-4o-mini
	KeyMasked string `json:"key_masked"` // 脱敏展示；明文只在 reveal 一次性返回
	Status    string `json:"status"`     // active / disabled
	CreatedBy string `json:"created_by,omitempty"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// 凭据状态常量（与翻译 Key 分开定义，语义各自独立）。
const (
	AICredActive   = "active"
	AICredDisabled = "disabled"
)

// ValidAICredStatus 判断状态取值是否登记在册。
func ValidAICredStatus(s string) bool { return s == AICredActive || s == AICredDisabled }

// LLMCredential 是解密后的可用凭据，只在内存中流转，不落库、不进响应。
type LLMCredential struct {
	BaseURL string
	APIKey  string
	Model   string
	Name    string
}

// ValidLLMBaseURL 粗校 base_url 形态。
//
// 为什么不用 net/url 严格解析：这里要挡的是「手滑填了模型名 / 邮箱」这类错，
// 不是做安全校验（URL 由超管录入，本身就是可信输入）。
// 只要求「有scheme + 有host」，把 http://localhost:1234/v1 这类本地网关放行。
func ValidLLMBaseURL(u string) bool {
	if len(u) < 10 { // 最短形如 http://a.b
		return false
	}
	if !HasPrefixAny(u, "http://", "https://") {
		return false
	}
	return len(u) > len("https://a")+2 // 保证 scheme 之后还有内容
}

// HasPrefixAny 判断 s 是否以任意一个前缀开头（小工具，避免在 domain 里引 strings 的风格分裂）。
func HasPrefixAny(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if len(s) >= len(p) && s[:len(p)] == p {
			return true
		}
	}
	return false
}

// ValidLLMModel 校验模型名：非空、不含空白（模型名里出现空格基本是复制错了）。
func ValidLLMModel(m string) bool {
	if m == "" {
		return false
	}
	for _, r := range m {
		if r == ' ' || r == '\t' || r == '\n' {
			return false
		}
	}
	return true
}
