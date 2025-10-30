/*
Copyright © 2025 TheMachine <592858548@qq.com>
*/
package environ

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"txt2geo/pkg/pathx"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// 本文件提供在 Windows 平台自动发现 QGIS 安装目录并设置运行所需环境变量的能力。
// 仅做本地进程级别 (os.Setenv) 修改，不对系统永久环境产生影响。

// 哨兵错误（Sentinel Errors），调用方可使用 errors.Is 进行判定：
var (
	// ErrQGISNotFound 表示未检测到可用的 QGIS 安装目录。
	ErrQGISNotFound = errors.New("qgis not installed")
	// ErrQGISEnvSetup 表示找到了安装目录但环境变量配置失败。
	ErrQGISEnvSetup = errors.New("qgis environment setup failed")
)

// InitializeQGISEnvironment 自动查找 QGIS 安装路径并为当前进程设置必要的环境变量。
//
// 此函数会依次尝试从注册表和常见安装位置查找 QGIS。
// 成功找到并设置环境变量后，会更新 PATH 和 PYTHONPATH 等，以便后续操作能正确调用 QGIS 相关工具。
//
// 返回:
//   - prefixPath: QGIS 的prefixPath路径。
//   - pythonPath: 解析到的 Python 解释器可执行文件路径（通常位于 QGIS 安装目录下的 bin/python*.exe）。
//   - ErrQGISNotFound: 如果未找到 QGIS 安装。
//   - ErrQGISEnvSetup: 如果找到了 QGIS 但在设置环境变量时出错。
func InitializeQGISEnvironment() (string, string, error) {
	qgisPath, err := findQGISPath()
	if err != nil {
		return "", "", ErrQGISNotFound
	}
	prefixPath := filepath.Join(qgisPath, "apps", "qgis")

	pythonPath, err := resolvePythonExecutable(qgisPath)
	if err != nil {
		return prefixPath, "", err
	}

	if err := setupQGISEnvironment(qgisPath); err != nil {
		// 不记录日志，由上层决定是否提示用户
		return prefixPath, pythonPath, fmt.Errorf("%w: %v", ErrQGISEnvSetup, err)
	}
	return prefixPath, pythonPath, nil
}

// findQGISPath 负责按顺序从不同来源查找 QGIS 的安装根目录。
// 它首先检查 Windows 注册表，如果找不到，则会搜索常见的安装目录。
// 返回找到的路径或一个错误。
func findQGISPath() (string, error) {
	// 1. 注册表
	registryKeys := []string{
		"QGIS Project\\Shell\\open\\command",
		"QGIS Project\\DefaultIcon",
	}
	if path, err := findFromRegistry(registryKeys); err == nil {
		return path, nil
	}
	// 2. 常见安装位置（含通配符）
	commonPaths := []string{"OSGeo4W", "Program Files\\QGIS*"}
	if path, err := findFromCommonPaths(commonPaths); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("未找到QGIS安装路径，请确保QGIS已正确安装")
}

// findFromRegistry 通过查询 Windows 注册表中的预定义键来定位 QGIS 安装路径。
// 它会遍历 `keyPaths` 中的每个键，直到找到一个有效路径为止。
func findFromRegistry(keyPaths []string) (string, error) {
	for _, keyPath := range keyPaths {
		key, err := registry.OpenKey(registry.CLASSES_ROOT, keyPath, windows.KEY_READ)
		if err != nil {
			continue
		}
		defer key.Close()

		value, _, err := key.GetStringValue("")
		if err != nil {
			continue
		}

		// 清理路径并验证
		trimmedPath := strings.Trim(value, "\"")
		if trimmedPath == "" {
			continue
		}
		// 提取QGIS根目录路径
		qgisRootDir := extractQGISRootPath(trimmedPath)
		if qgisRootDir == "" {
			continue
		}

		// 转换为标准长路径格式
		longPath, err := pathx.GetLongPathName(qgisRootDir)
		if err != nil {
			// 转换失败，直接用原路径，库代码不输出
			longPath = qgisRootDir
		}

		// 验证是否为有效的QGIS根目录
		if isValidQGISPath(longPath) {
			return longPath, nil
		}
	}
	return "", fmt.Errorf("未在注册表中找到QGIS安装路径")
}

// findFromCommonPaths 遍历系统的所有逻辑驱动器和一组常见的 QGIS 安装目录名来查找 QGIS。
// 支持通配符路径，例如 "Program Files\\QGIS*"。
func findFromCommonPaths(commonPaths []string) (string, error) {
	drivers, err := pathx.GetLogicalDrives()
	if err != nil {
		return "", fmt.Errorf("获取逻辑驱动器失败: %w", err)
	}

	for _, drive := range drivers {
		for _, commonPath := range commonPaths {
			fullPath := filepath.Join(drive, commonPath)

			// 处理通配符路径
			if strings.Contains(commonPath, "*") {
				matches, err := filepath.Glob(fullPath)
				if err != nil {
					continue
				}
				for _, match := range matches {
					if isValidQGISPath(match) {
						return match, nil
					}
				}
			} else {
				// 直接路径检查
				if isValidQGISPath(fullPath) {
					return fullPath, nil
				}
			}
		}
	}
	return "", fmt.Errorf("未在常见路径中找到QGIS安装")
}

// resolvePythonExecutable 在给定的 QGIS 安装目录下定位 Python 解释器可执行文件。
func resolvePythonExecutable(qgisPath string) (string, error) {
	candidates := []string{
		filepath.Join(qgisPath, "bin", "python3.exe"),
		filepath.Join(qgisPath, "bin", "python.exe"),
	}
	for _, candidate := range candidates {
		if exists, _ := pathx.Exists(candidate); exists {
			return candidate, nil
		}
	}

	pattern := filepath.Join(qgisPath, "bin", "python*.exe")
	matches, err := filepath.Glob(pattern)
	if err == nil {
		for _, match := range matches {
			if exists, _ := pathx.Exists(match); exists {
				return match, nil
			}
		}
	}

	return "", fmt.Errorf("未在 QGIS 安装目录中找到 python 可执行文件: %s", qgisPath)
}

// extractQGISRootPath 从一个给定的路径（通常是注册表中的可执行文件路径）向上追溯，
// 直到找到一个被识别为有效 QGIS 根目录的父目录。
func extractQGISRootPath(registryValue string) string {
	qgisBatPath := strings.Trim(registryValue, "\"")
	for dir := filepath.Dir(qgisBatPath); ; dir = filepath.Dir(dir) {
		if isValidQGISPath(dir) {
			return dir
		}
		if parent := filepath.Dir(dir); parent == dir {
			return ""
		}
	}
}

// isValidQGISPath 检查给定路径是否是一个有效的 QGIS 安装目录。
// 它通过检查目录下是否存在一些关键文件（如 "OSGeo4W.bat"）来做出判断。
func isValidQGISPath(path string) bool {
	if exists, _ := pathx.Exists(path); !exists {
		return false
	}

	// 检查其他关键文件
	keyFiles := []string{
		"OSGeo4W.bat",
		"bin/python3.exe",
		"apps/qgis/python",
		"bin/qgis.bat",
	}
	for _, keyFile := range keyFiles {
		if exists, _ := pathx.Exists(filepath.Join(path, keyFile)); exists {
			return true
		}
	}
	return false
}

// ===== 环境变量相关函数 =====

// parseEnvFile 解析 QGIS 的环境配置文件 (如 qgis-bin.env)，
// 并将其中定义的键值对提取到一个 map 中。
func parseEnvFile(filePath string) (map[string]string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("读取环境变量文件失败: %w", err)
	}
	envVars := make(map[string]string)
	lines := strings.Split(string(data), "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if parts := strings.SplitN(line, "=", 2); len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			if key != "" {
				envVars[strings.ToUpper(key)] = value
			}
		}
	}
	return envVars, nil
}

// setupQGISEnvironment 根据找到的 QGIS 安装路径，为当前进程配置所需的环境变量。
// 它会读取 .env 文件，并合并 PATH 和 PYTHONPATH 等路径变量。
func setupQGISEnvironment(qgisPath string) error {
	// 读取QGIS环境变量文件
	envFile := filepath.Join(qgisPath, "bin", "qgis-bin.env")
	envVars, err := parseEnvFile(envFile)
	if err != nil {
		return fmt.Errorf("解析环境文件失败: %w", err)
	}

	// 添加QGIS Python路径到PYTHONPATH
	pythonPath := filepath.Join(qgisPath, "apps", "qgis", "python")
	if _, err := os.Stat(pythonPath); err == nil {
		currentPythonPath := os.Getenv("PYTHONPATH")
		if currentPythonPath == "" {
			envVars["PYTHONPATH"] = pythonPath
		} else {
			envVars["PYTHONPATH"] = pythonPath + ";" + currentPythonPath
		}
	}

	// 需要合并追加的路径型环境变量
	pathVars := map[string]struct{}{
		"PATH":       {},
		"PYTHONPATH": {},
	}

	for key, value := range envVars {
		upperKey := strings.ToUpper(key)
		if _, ok := pathVars[upperKey]; ok {
			// 路径型变量，合并去重
			oldVal := os.Getenv(upperKey)
			merged := mergePathEnv(value, oldVal)
			if err := os.Setenv(upperKey, merged); err != nil {
				// 设置失败，库代码不输出
			}
		} else {
			// 普通变量，直接覆盖
			if err := os.Setenv(upperKey, value); err != nil {
				// 设置失败，库代码不输出
			}
		}
	}

	// QGIS环境设置完成，库代码不输出
	return nil
}

// mergePathEnv 合并新旧两个路径字符串，去除重复项，并返回一个单一的、合并后的路径字符串。
// 路径项会进行标准化和去重处理。
func mergePathEnv(newVal, oldVal string) string {
	sep := ";"
	if os.PathListSeparator != ';' {
		sep = string(os.PathListSeparator)
	}

	pathMap := make(map[string]struct{})
	var result []string

	appendPath := func(val string) {
		for _, raw := range strings.Split(val, sep) {
			p := strings.TrimSpace(raw)
			if p == "" {
				continue
			}
			if absP, err := filepath.Abs(p); err == nil {
				p = absP
			}
			key := strings.ToLower(filepath.Clean(p))
			if _, exists := pathMap[key]; !exists {
				pathMap[key] = struct{}{}
				result = append(result, p)
			}
		}
	}

	appendPath(newVal)
	appendPath(oldVal)
	return strings.Join(result, sep)
}
