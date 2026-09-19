package handler

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// cursor 是列表分页的位置标记。
//
// 用 (created_at, id) 复合游标而不是 offset：Incident 持续新增，
// offset 分页在列表头部插入新记录时会漏掉条目。
type cursor struct {
	CreatedAt time.Time `json:"t"`
	ID        int64     `json:"i"`
}

// encodeCursor 把位置编码成不透明的 base64 串。
//
// 编码成不透明串是为了让调用方按"标记"来用，而不是自己拼装 ——
// 以后要改游标结构时不必破坏接口。
func encodeCursor(t time.Time, id int64) string {
	b, err := json.Marshal(cursor{CreatedAt: t, ID: id})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeCursor 解析游标。
func decodeCursor(s string) (cursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursor{}, fmt.Errorf("decode cursor: %w", err)
	}
	var cur cursor
	if err := json.Unmarshal(b, &cur); err != nil {
		return cursor{}, fmt.Errorf("unmarshal cursor: %w", err)
	}
	return cur, nil
}
