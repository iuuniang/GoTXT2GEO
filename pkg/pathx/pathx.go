/*
Copyright © 2025 TheMachine <592858548@qq.com>
*/
package pathx

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/windows"
)

// Equal 判断两个路径是否逻辑相等（绝对化并忽略大小写于 Windows）。
func Equal(path1, path2 string) (bool, error) {
	if path1 == "" || path2 == "" {
		return false, fmt.Errorf("路径不能为空")
	}
	absPath1, err := Resolve(path1)
	if err != nil {
		return false, fmt.Errorf("无法获取路径1的绝对路径: %w", err)
	}
	absPath2, err := Resolve(path2)
	if err != nil {
		return false, fmt.Errorf("无法获取路径2的绝对路径: %w", err)
	}
	return strings.EqualFold(absPath1, absPath2), nil
}

// Resolve 对路径做格式与符号链接解析，不改变原语义，增强分隔符与盘符。
func Resolve(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("路径不能为空")
	}

	// 绝对化 (Abs 已含 Clean 逻辑语义；失败时回退 Clean)
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	} else {
		p = filepath.Clean(p)
	}

	// 符号链接解析（忽略错误）仅需确认存在
	if _, err := os.Lstat(p); err == nil { // 使用 Lstat 避免提前跟随，EvalSymlinks 再解析
		if real, rerr := filepath.EvalSymlinks(p); rerr == nil {
			p = real
		}
	}

	// 替换分隔符（不破坏特殊前缀）
	if !strings.HasPrefix(p, `\\?\`) {
		p = strings.ReplaceAll(p, `/`, `\\`)
	}
	// 长路径转换（失败忽略）
	if long, err := GetLongPathName(p); err == nil && long != "" {
		p = long
	}
	// 盘符规范为大写
	if len(p) >= 2 && p[1] == ':' {
		c := p[0]
		if c >= 'a' && c <= 'z' {
			p = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	// 去除尾部分隔符（根路径如 C:\ 或 \\?\C:\ 保留）
	if len(p) > 3 { // 最短可能根: C:\
		for strings.HasSuffix(p, `\\`) {
			// 保留如 C:\ 以及 \\?\C:\ 形式
			if len(p) == 3 && p[1] == ':' { // C:\ 结构（注意后续可能变成 C: ）
				break
			}
			// 处理长路径前缀根，如 \\?\C:\
			if strings.HasPrefix(p, `\\?\`) && len(p) <= 7 { // \\?\C:\ 长度=7
				break
			}
			p = strings.TrimRight(p, `\\`)
		}
	}
	return p, nil
}

// Exists 判断路径是否存在。不存在返回 (false,nil)。其它错误包装返回。
func Exists(path string) (bool, error) {
	if path == "" {
		return false, fmt.Errorf("路径不能为空")
	}
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("检查路径时出错: %w", err)
}

// IsDir 判断路径是否为目录，包含不存在场景处理。
// 如果返回错误，表示检查过程中出现严重问题。
func IsDir(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("检查目录时出错: %w", err)
	}
	return info.IsDir(), nil
}

// Stem 返回最后路径元素去除单一末尾扩展后的“主体”。
// 规则：
//   - 对多层扩展仅移除最后一段（a.tar.gz -> a.tar）
//   - 对仅前导点的隐藏文件（.gitignore）不移除 “扩展”
//   - 对根或无有效基名返回错误
func Stem(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("路径不能为空")
	}
	base := filepath.Base(p)
	if base == "." || base == string(os.PathSeparator) {
		return "", fmt.Errorf("路径 '%s' 无有效基础名称", p)
	}

	ext := filepath.Ext(base)
	if ext == "" {
		return base, nil
	}
	// 处理前导点隐藏文件：若 ext == base 说明没有真正的“扩展”
	if ext == base && strings.HasPrefix(base, ".") {
		return base, nil
	}
	stem := base[:len(base)-len(ext)]
	if stem == "" { // 理论上只会出现在类似 .gitignore 场景，已在上面处理；这里兜底
		return base, nil
	}
	return stem, nil
}

// GetLogicalDrives 返回当前系统可用的逻辑驱动器列表（仅 Windows）。
// 失败或空集都会返回明确错误。
func GetLogicalDrives() ([]string, error) {
	bufferSize, err := windows.GetLogicalDriveStrings(0, nil)
	if err != nil {
		return nil, fmt.Errorf("获取驱动器缓冲区大小失败: %w", err)
	}
	if bufferSize == 0 {
		return nil, fmt.Errorf("没有找到逻辑驱动器")
	}
	buffer := make([]uint16, bufferSize)
	actualSize, err := windows.GetLogicalDriveStrings(bufferSize, &buffer[0])
	if err != nil {
		return nil, fmt.Errorf("获取驱动器字符串失败: %w", err)
	}
	if actualSize == 0 {
		return nil, fmt.Errorf("获取驱动器字符串返回空结果")
	}
	var drives []string
	for i, start := 0, 0; i < int(actualSize); i++ {
		if buffer[i] == 0 && i > start {
			drive := windows.UTF16ToString(buffer[start:i])
			if drive != "" {
				drives = append(drives, drive)
			}
			start = i + 1
		}
	}
	return drives, nil
}

// GetLongPathName 返回规范的长路径形式（仅 Windows）。失败时回退原路径。
func GetLongPathName(shortPath string) (string, error) {
	if shortPath == "" {
		return "", fmt.Errorf("路径不能为空")
	}
	utf16Path, err := windows.UTF16PtrFromString(shortPath)
	if err != nil {
		return "", fmt.Errorf("转换路径为UTF16失败: %w", err)
	}
	buffer := make([]uint16, windows.MAX_PATH)
	length, err := windows.GetLongPathName(utf16Path, &buffer[0], uint32(len(buffer)))
	if err != nil {
		return shortPath, nil
	}
	if length == 0 {
		return shortPath, nil
	}
	return windows.UTF16ToString(buffer[:length]), nil
}

// Dirx 规范化路径，并在其指向文件或潜在文件路径时返回父目录路径。
// 如果是目录，返回本身
func Dirx(p string) (string, error) {
	norm, err := Resolve(p)
	if err != nil {
		return "", err
	}
	// 首先，检查路径是否实际存在并且是一个目录。
	// 这是最明确的情况。
	isDir, err := IsDir(norm)
	if err != nil {
		// IsDir 内部已处理 os.IsNotExist，这里只处理其他 Stat 错误
		return "", err
	}
	if isDir {
		return norm, nil
	}
	// 如果路径不存在，或者它是一个已存在的文件，我们进行启发式判断。
	// 核心逻辑：如果路径看起来像一个文件（即有文件扩展名），
	// 我们就取其父目录。否则，我们认为它意图作为一个目录，返回其自身。
	// filepath.Ext 会返回最后一个点之后的部分，例如 ".txt"。

	if filepath.Ext(norm) != "" {
		// 路径有扩展名，很可能是一个文件，返回其父目录。
		return filepath.Dir(norm), nil
	}
	// 路径没有扩展名，我们假定它意图作为一个目录路径，返回其自身。
	return norm, nil
}

// ReadFile 读取文件内容并计算 SHA-256 哈希。
// 返回内容字节切片、十六进制哈希字符串与错误。
func ReadFile(path string) ([]byte, string, error) {
	norm, _ := Resolve(path)
	content, err := os.ReadFile(norm)
	if err != nil {
		return nil, "", fmt.Errorf("无法读取文件 %s: %w", norm, err)
	}

	sum := sha256.Sum256(content)
	return content, hex.EncodeToString(sum[:]), nil
}

// WalkDir 遍历目录并按深度与扩展名过滤。
//   - maxDepth: -1 不限制；0 仅 root 文件；1 root+子目录；依次类推
//   - extensions: 允许的扩展集合（大小写不敏感，支持不带点）
//
// 返回绝对规范化后的文件列表（稳定排序）。
func WalkDir(root string, maxDepth int, sortResult bool, extensions []string) ([]string, error) {
	// 规范化根路径
	nRoot, err := Resolve(root)
	if err != nil {
		return nil, err
	}

	// 先判断存在与类型，给出更明确错误
	exist, err := Exists(nRoot)
	if err != nil {
		return nil, err
	}
	if !exist {
		return nil, fmt.Errorf("根路径不存在: %s", nRoot)
	}
	if ok, err := IsDir(nRoot); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("根路径不是目录: %s", nRoot)
	}

	// 统一构建扩展集合
	allowedSlice := normalizeExts(extensions)
	allowed := make(map[string]struct{}, len(allowedSlice))
	for _, e := range allowedSlice {
		allowed[e] = struct{}{}
	}
	filterEnabled := len(allowed) > 0

	type node struct {
		path  string
		depth int
	}
	stack := []node{{path: nRoot, depth: 0}}
	files := make([]string, 0, 128)

	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if maxDepth >= 0 && current.depth > maxDepth {
			continue
		}
		entries, readErr := os.ReadDir(current.path)
		if readErr != nil {
			return nil, fmt.Errorf("读取目录失败 %s: %w", current.path, readErr)
		}
		for _, entry := range entries {
			fullPath := filepath.Join(current.path, entry.Name())
			if entry.IsDir() {
				if maxDepth < 0 || current.depth < maxDepth {
					stack = append(stack, node{path: fullPath, depth: current.depth + 1})
				}
				continue
			}

			if !filterEnabled || hasAllowedExt(entry.Name(), allowed) {
				files = append(files, fullPath)
			}
		}
	}
	if sortResult {
		stablePathSort(files)
	}
	return files, nil
}

// hasAllowedExt 判断文件名是否匹配允许的扩展集合（集合已为小写且含点）。
func hasAllowedExt(name string, allowed map[string]struct{}) bool {
	ext := strings.ToLower(filepath.Ext(name))
	_, ok := allowed[ext]
	return ok
}

// stablePathSort 对路径进行跨平台稳定排序：主键为不区分大小写的值，次键为原值。
func stablePathSort(paths []string) {
	sort.Slice(paths, func(i, j int) bool {
		ai := strings.ToLower(paths[i])
		aj := strings.ToLower(paths[j])
		if ai == aj {
			return paths[i] < paths[j]
		}
		return ai < aj
	})
}

func normalizeExts(exts []string) []string {
	out := make([]string, 0, len(exts))
	for _, e := range exts {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		out = append(out, strings.ToLower(e))
	}
	return out
}

// CollectFiles 对输入的混合路径（文件或目录）按扩展名与深度规则进行收集。
// 参数说明：
//   - inputs: 文件或目录路径切片，可包含相对路径与重复；空元素自动跳过
//   - maxDepth: 目录递归最大深度；与 WalkDir 含义一致（-1 不限制；0 仅目录本身文件；1 包含一级子目录...）
//   - extensions: 需要匹配的扩展名集合（大小写不敏感，支持不带点；空集合表示不过滤全部文件）
//   - sortResult: 是否对最终结果进行稳定排序（不区分大小写主键 + 原值次键）
//
// 返回：满足扩展过滤的绝对规范化文件路径切片（去重）。
// 行为：
//   - 不存在的路径被自动忽略（不报错）
//   - 单个输入若是目录按目录递归处理；若是文件需扩展匹配（或未启用过滤）才加入
//   - 解析使用 Resolve，存在性与类型检查使用 Exists / IsDir
//   - 发生严重错误（例如 Stat 非不存在错误）立即返回
func CollectFiles(inputs []string, maxDepth int, extensions []string, sortResult bool) ([]string, error) {
	// 规范化扩展集合
	normExts := normalizeExts(extensions)
	allowed := make(map[string]struct{}, len(normExts))
	for _, e := range normExts {
		allowed[e] = struct{}{}
	}
	filterEnabled := len(allowed) > 0

	// 使用 map 去重
	resultSet := make(map[string]struct{}, 256)

	for _, in := range inputs {
		in = strings.TrimSpace(in)
		if in == "" {
			continue
		}
		resolved, err := Resolve(in)
		if err != nil {
			return nil, fmt.Errorf("解析路径失败 '%s': %w", in, err)
		}
		exists, err := Exists(resolved)
		if err != nil { // Stat 其它错误
			return nil, err
		}
		if !exists { // 默认忽略不存在路径
			continue
		}
		isDir, err := IsDir(resolved)
		if err != nil { // IsDir 内部可能再 Stat
			return nil, err
		}
		if isDir {
			// 目录递归收集；不在此处排序，统一最终排序
			files, werr := WalkDir(resolved, maxDepth, false, extensions)
			if werr != nil {
				return nil, werr
			}
			for _, f := range files {
				resultSet[f] = struct{}{}
			}
			continue
		}
		// 单文件路径：扩展过滤
		name := filepath.Base(resolved)
		if !filterEnabled || hasAllowedExt(name, allowed) {
			resultSet[resolved] = struct{}{}
		}
	}

	// 转换为切片
	out := make([]string, 0, len(resultSet))
	for p := range resultSet {
		out = append(out, p)
	}
	if sortResult {
		stablePathSort(out)
	}
	return out, nil
}
