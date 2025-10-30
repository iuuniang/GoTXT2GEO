/*
Copyright © 2025 TheMachine <592858548@qq.com>
*/
package namex

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

var (
	blacklist = map[string]struct{}{
		// GIS字段名
		"fid": {}, "area": {}, "len": {}, "points": {}, "numofpts": {}, "entity": {},
		"eminx": {}, "eminy": {}, "emaxx": {}, "emaxy": {}, "eminz": {}, "emaxz": {},
		"min_measure": {}, "max_measure": {},
		// SQL保留字
		"add": {}, "alter": {}, "and": {}, "between": {}, "by": {}, "column": {},
		"create": {}, "delete": {}, "drop": {}, "exists": {}, "for": {}, "from": {},
		"group": {}, "having": {}, "in": {}, "insert": {}, "into": {}, "is": {},
		"like": {}, "not": {}, "null": {}, "or": {}, "order": {}, "select": {},
		"set": {}, "table": {}, "update": {}, "values": {}, "where": {},
	}
)

// 默认最大长度 (0 表示不截断)。可根据需要调整。
const DefaultMaxNameLength = 52

// Sanitize 将任意文件或图层名转换为一个安全的、可用于标识符的字符串。
//
// 处理流程:
//  1. 提取路径的最后一部分并移除其扩展名。
//  2. 使用 Unicode NFKC 对字符串进行归一化，以兼容全角字符等。
//  3. 清理字符串，仅保留字母、数字和下划线，将连续的非法字符替换为单个下划线，并移除首尾的下划线。
//  4. 如果结果为空，则返回 "unnamed"。
//  5. 如果名称以数字开头或与黑名单中的保留字冲突，则在前面添加下划线。
//  6. 根据 DefaultMaxNameLength（如果设置）按 rune 安全地截断名称。
//  7. 如果提供了 `providedUsed` 集合，则通过在末尾附加数字来确保名称的唯一性。
//
// 参数:
//
//	filePath: 原始文件名或图层名。
//	providedUsed: 一个用于跟踪已使用名称的 map，以避免重复。可为 nil。
//
// 返回:
//
//	一个非空的、仅包含字母、数字和下划线的安全字符串。
//
// normalizeAndFold 负责：NFKC + 保留字母数字和下划线 + 合并非法段为单下划线 + 去首尾下划线。
func normalizeAndFold(s string) string {
	if s == "" {
		return ""
	}
	s = norm.NFKC.String(s)
	var b strings.Builder
	b.Grow(len(s))
	prevUnderscore := false
	for _, r := range s {
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevUnderscore = false
			continue
		}
		if !prevUnderscore {
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	out := b.String()
	// 去首尾下划线
	for len(out) > 0 && out[0] == '_' {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == '_' {
		out = out[:len(out)-1]
	}
	return out
}

// truncateRunes 在不分配 rune 切片的前提下按 rune 计数截断。
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	runeCount := 0
	for i := range s {
		if runeCount == max {
			return s[:i]
		}
		runeCount++
	}
	return s
}

// Sanitize 规范化名称并在 providedUsed 非空时确保唯一性。
func Sanitize(filePath string, providedUsed map[string]struct{}) string {
	name := strings.TrimSpace(filePath)
	if name == "" {
		return "unnamed"
	}
	base := filepath.Base(name)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if stem == "" {
		stem = base
	}

	normalized := normalizeAndFold(stem)
	if normalized == "" {
		normalized = "unnamed"
	}

	lower := strings.ToLower(normalized)
	if r, _ := utf8.DecodeRuneInString(normalized); unicode.IsDigit(r) || len(lower) > 0 && blacklist[lower] != struct{}{} { // 保留原逻辑判断黑名单
		if _, exists := blacklist[lower]; exists || unicode.IsDigit(r) {
			normalized = "_" + normalized
		}
	} else if _, exists := blacklist[lower]; exists {
		normalized = "_" + normalized
	}

	if DefaultMaxNameLength > 0 {
		normalized = truncateRunes(normalized, DefaultMaxNameLength)
		normalized = strings.TrimRight(normalized, "_")
		if normalized == "" {
			normalized = "unnamed"
		}
	}

	if providedUsed == nil {
		return normalized
	}

	// Fast path: 无占用直接返回
	if _, exists := providedUsed[normalized]; !exists {
		providedUsed[normalized] = struct{}{}
		return normalized
	}

	original := normalized
	for i := 1; ; i++ { // 从 1 开始更直观
		cand := fmt.Sprintf("%s_%d", original, i)
		if _, exists := providedUsed[cand]; !exists {
			providedUsed[cand] = struct{}{}
			return cand
		}
	}
}
