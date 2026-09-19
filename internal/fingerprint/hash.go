package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
)

// Size 是指纹的字节长度。
//
// 取 128 位：对当前数据量而言碰撞概率可忽略，同时索引比完整
// SHA-256 小一半。与 03-data-model.md 的 BYTEA 列对应。
const Size = 16

// Compute 计算规范化日志的指纹。
//
// 输入应当是 Normalize 的输出。直接传原始日志也能工作，
// 但那样每个时间戳都会产生不同的指纹。
func Compute(normalized string) []byte {
	sum := sha256.Sum256([]byte(normalized))
	out := make([]byte, Size)
	copy(out, sum[:Size])
	return out
}

// ComputeHex 是 Compute 的十六进制形式，用于日志和调试。
func ComputeHex(normalized string) string {
	return hex.EncodeToString(Compute(normalized))
}

// Of 是 Normalize 加 Compute 的快捷方式。
func Of(raw string) []byte {
	return Compute(Normalize(raw))
}
