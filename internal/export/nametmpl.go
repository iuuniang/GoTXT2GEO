/*
Copyright © 2025 TheMachine <592858548@qq.com>
*/
package export

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"txt2geo/internal/util"
)

// renderNameTemplate 负责将名称模板中的占位符替换为实际值。
// 支持占位符:
//
//	{name}   				基础名称 (分散: 源文件名规范化; 合并: merged_output 或外部传入)
//	{index[:width]}        	当前序号。
//	{count}                	总数量
//	{date[:layout]} 		日期 (默认 20060102, 可指定 Go time layout)
//	{uuid}                 	随机 UUID v4
//	{rand[:len]} 			随机字符串 (默认 8 位)
//  :lower|upper|title    可用于所有占位符，表示转换结果的大小写。

func renderNameTemplate(tmpl string, baseName string, index int, count int) string {
	var out strings.Builder
	for len(tmpl) > 0 {
		start := strings.IndexByte(tmpl, '{')
		if start == -1 {
			out.WriteString(tmpl)
			break
		}
		out.WriteString(tmpl[:start])
		tmpl = tmpl[start+1:]
		end := strings.IndexByte(tmpl, '}')
		if end == -1 { // 无闭合，原样输出剩余
			out.WriteString("{" + tmpl)
			break
		}
		full := tmpl[:end]
		tmpl = tmpl[end+1:]
		out.WriteString(resolveToken(full, baseName, index, count))
	}
	return out.String()
}

func resolveToken(token string, baseName string, index int, count int) string {
	parts := strings.Split(token, ":")
	if len(parts) == 0 {
		return "{" + token + "}"
	}
	nameRaw := parts[0]
	if nameRaw == "" {
		return "{" + token + "}"
	}
	name := strings.ToLower(nameRaw)

	rawArgs := parts[1:]
	caseTransform := ""
	// 允许 lower/upper 出现在任意位置：取最后一次出现并移除它
	filtered := rawArgs[:0]
	for _, a := range rawArgs {
		la := strings.ToLower(a)
		if la == "lower" || la == "upper" || la == "title" {
			caseTransform = la
			continue
		}
		filtered = append(filtered, a)
	}
	args := filtered

	firstArg := ""
	if len(args) > 0 {
		firstArg = args[0]
	}

	var result string

	switch name {
	case "name":
		result = baseName
	case "index":
		width := len(firstArg)
		offset := 0
		if firstArg != "" {
			if v, err := strconv.Atoi(firstArg); err == nil {
				offset = v
			}
		}
		result = fmt.Sprintf("%0*d", width, index+offset)
	case "count":
		result = fmt.Sprintf("%d", count)
	case "date":
		layout := "20060102"
		if firstArg != "" {
			layout = firstArg
		}
		result = time.Now().Format(layout)
	case "uuid":
		result, _ = util.GetUUIDv4()
	case "rand":
		length := 8
		if firstArg != "" {
			if n, e := strconv.Atoi(firstArg); e == nil && n > 0 {
				length = n
			}
		}
		result = util.RandomString(length)
	default:
		// 未知 token 原样返回
		result = "{" + token + "}"
	}

	// 应用大小写转换
	switch caseTransform {
	case "lower":
		result = strings.ToLower(result)
	case "upper":
		result = strings.ToUpper(result)
	case "title":
		if len(result) > 0 {
			result = strings.ToUpper(result[:1]) + result[1:]
		}
	}
	return result
}
