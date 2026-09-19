package worker

import "encoding/json"

// jsonBytes 序列化成 []byte，用作 jsonb 参数。
//
// 故意忽略 marshal 错误：输入都是 []string / []domain.Evidence /
// map[string]string 这类必然可序列化的值。返回 nil 时 PostgreSQL
// 会把它当成 JSON null，不会破坏约束。
func jsonBytes(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("null"), nil
	}
	return b, nil
}
