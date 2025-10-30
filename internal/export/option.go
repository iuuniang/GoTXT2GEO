/*
Copyright © 2025 TheMachine <592858548@qq.com>
*/
package export

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"txt2geo/pkg/logger"
	"txt2geo/pkg/pathx"
)

// exportFormat 描述一种输出格式的特征
type exportFormat struct {
	Code        string // 简短格式代码 (SHP / FGB / GPKG / GDB)
	Driver      string // 完整驱动名称 (ESRI Shapefile / FlatGeobuf / GPKG / OpenFileGDB)
	Extension   string // 主文件扩展名 (.shp / .fgb / .gpkg / .gdb)
	IsContainer bool   // 是否容器格式（目录/单文件多图层）
}

var supportedFormats = map[string]exportFormat{
	"SHP":  {Code: "SHP", Driver: "ESRI Shapefile", Extension: ".shp", IsContainer: false},
	"FGB":  {Code: "FGB", Driver: "FlatGeobuf", Extension: ".fgb", IsContainer: false},
	"GPKG": {Code: "GPKG", Driver: "GPKG", Extension: ".gpkg", IsContainer: true},
	"GDB":  {Code: "GDB", Driver: "OpenFileGDB", Extension: ".gdb", IsContainer: true},
}

// ExportConfig 汇集了从命令行接收到的所有导出参数。
type ExportConfig struct {
	InputPaths   []string
	Depth        int
	FormatKey    string
	OutputDir    string //文件夹或数据库
	Merge        bool
	NameTemplate string
	DryRun       bool
	Overwrite    bool
	ForceRefresh bool

	//派生
	FormatDetails exportFormat
}

const ProcessedFileName = ".processed"

// GetFormatDetails 根据格式键（如 "SHP"）返回格式的详细信息。
// 如果找不到对应的格式，将返回一个零值的 exportFormat 和 false。
func GetFormatDetails(key string) (exportFormat, error) {
	switch {
	case strings.EqualFold(key, "SHAPE") || strings.EqualFold(key, "Shapefile") || strings.EqualFold(key, ".shp"):
		key = "SHP"
	case strings.EqualFold(key, "FGB") || strings.EqualFold(key, "FlatGeobuf") || strings.EqualFold(key, ".fgb"):
		key = "FGB"
	case strings.EqualFold(key, "GPKG") || strings.EqualFold(key, "GeoPackage") || strings.EqualFold(key, ".gpkg"):
		key = "GPKG"
	case strings.EqualFold(key, "GDB") || strings.EqualFold(key, "OpenFileGDB") || strings.EqualFold(key, ".gdb"):
		key = "GDB"
	}
	format, ok := supportedFormats[key]

	if !ok {
		return exportFormat{}, fmt.Errorf("不支持的输出格式: %s", key)
	}
	return format, nil
}

// Verify validates and normalizes the export configuration.
func (c *ExportConfig) Verify() error {
	// 1. 验证输入文件
	if len(c.InputPaths) == 0 {
		return errors.New("至少提供一个 --input / -i")
	}
	for i, input := range c.InputPaths {
		trimmed := strings.TrimSpace(input)
		if trimmed == "" {
			return fmt.Errorf("第 %d 个输入为空", i+1)
		}
		c.InputPaths[i] = trimmed
	}
	// 2. 验证递归深度
	if c.Depth < -1 {
		return errors.New("depth 不能小于 -1")
	}

	// 3. 验证并规范化导出格式
	formatDetails, err := GetFormatDetails(c.FormatKey)
	if err != nil {
		return fmt.Errorf("未能获取到 %s 格式的详细信息: %w", c.FormatKey, err)
	}
	c.FormatDetails = formatDetails

	// 4. 验证并规范化输出目录
	outputdir := strings.TrimSpace(c.OutputDir)
	if outputdir == "" {
		// 如果未指定，获取当前工作目录
		outputdir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("无法获取当前工作目录: %w", err)
		}
		logger.Log().Debug("未指定输出目录，使用当前目录", "dir", outputdir)
	} else {
		// 如果已指定，解析为绝对路径
		outputdir, err = pathx.Resolve(outputdir)
		if err != nil {
			return fmt.Errorf("无法解析输出目录 '%s': %w", outputdir, err)
		}
	}
	resolved, err := pathx.Dirx(outputdir)

	if err != nil {
		return fmt.Errorf("无法解析父目录: %w", err)
	}

	if c.FormatDetails.IsContainer {
		//判断是否为容器类格式
		isDir, err := pathx.IsDir(resolved)
		if err != nil {
			return fmt.Errorf("无法检查目录 '%s': %w", resolved, err)
		}
		if isDir {
			// 如果文件夹存在且是目录，则将输出路径设为该目录下的默认容器名称
			// 目录 -> 在该目录下创建一个与目录同名的容器文件。
			resolved = filepath.Join(resolved, filepath.Base(resolved))
		}
		ext := c.FormatDetails.Extension
		if !strings.HasSuffix(strings.ToLower(resolved), ext) {
			resolved += ext
		}
	}
	c.OutputDir = resolved

	// 6. 验证名称模板
	nameTemplate := strings.TrimSpace(c.NameTemplate)
	if nameTemplate == "" {
		// 用户未提供模板，使用默认值
		nameTemplate = "{name}"
	} else {
		// 用户提供了模板，进行智能修正
		// 使用 pathx.Stem 移除任何文件扩展名，获取纯净的基础名称
		stem, err := pathx.Stem(nameTemplate)
		if err != nil {
			// 如果 Stem 出错（例如路径是 "." 或 "/"），就退回使用原始模板
			stem = nameTemplate
		}
		nameTemplate = stem
	}
	c.NameTemplate = nameTemplate
	return nil
}

func (c *ExportConfig) Prepare() error {
	if c.DryRun {
		logger.Log().Debug("已启用 --dry-run 模式, 将不会写入文件")
		return nil
	}
	logger.Log().Debug("创建记录已处理文件 hash 的文件夹", "dir", c.ProcessFileDir())
	if err := os.MkdirAll(c.ProcessFileDir(), 0o755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}
	return nil
}

// ProcessFileDir 返回处理历史记录文件所在的目录路径
func (c *ExportConfig) ProcessFileDir() string {
	// 5. 验证处理文件
	var processFile string
	if c.FormatDetails.IsContainer {
		// 对于容器类格式，处理文件应该保存在输出目录的父目录中
		processFile = filepath.Dir(c.OutputDir)
	} else {
		// 对于单文件多图层格式，处理文件应该保存在输出目录中
		processFile = c.OutputDir
	}
	return processFile
}

// ProcessFilePath 返回处理历史记录文件的完整路径
func (c *ExportConfig) ProcessFilePath() string {
	return filepath.Join(c.ProcessFileDir(), ProcessedFileName)
}
