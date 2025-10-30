/*
Copyright © 2025 TheMachine <592858548@qq.com>
*/
package export

import (
	"errors"
	"fmt"
	"txt2geo/internal/process"
	"txt2geo/pkg/logger"
	"txt2geo/pkg/pathx"
)

var filterExtensions = []string{".txt"}

// ErrNoInputFiles 表示未找到任何可用于导出的输入文件。
var ErrNoInputFiles = errors.New("未找到可导出的输入文件")

type FileCache struct {
	Path    string
	Content []byte
	Hash    string
}

// Exporter 是负责执行整个导出流程的协调器。
type Exporter struct {
	Config    ExportConfig
	History   *process.ProcessHistory
	FileCache map[string]FileCache
	UsedNames map[string]struct{}
}

// NewExporter 创建一个新的导出器实例。
func NewExporter(config ExportConfig) (*Exporter, error) {
	if err := config.Verify(); err != nil {
		return nil, fmt.Errorf("参数验证失败: %w", err)
	}
	if err := config.Prepare(); err != nil {
		return nil, fmt.Errorf("环境配置失败: %w", err)
	}

	history, err := process.NewProcessHistory(config.ProcessFilePath())
	if err != nil {
		return nil, fmt.Errorf("无法初始化处理历史: %w", err)
	}
	return &Exporter{
		Config:    config,
		History:   history,
		FileCache: make(map[string]FileCache),
		UsedNames: make(map[string]struct{}),
	}, nil
}

func (e *Exporter) Execute() error {
	// 1. 收集所有源文件
	logger.Log().Info("开始处理任务", "preview", e.Config.DryRun, "forceRefresh", e.Config.ForceRefresh)
	sourceFiles, err := pathx.CollectFiles(e.Config.InputPaths, e.Config.Depth, filterExtensions, true)
	if err != nil {
		return fmt.Errorf("收集文件失败: %w", err)
	}
	if len(sourceFiles) == 0 {
		return ErrNoInputFiles
	}

	// 2. 读取文件，计算哈希，准备内容缓存，去重（ForceRefresh 可强制重新处理）
	var skipped, processed int
	force := e.Config.ForceRefresh

	for _, file := range sourceFiles {
		content, hash, err := pathx.ReadFile(file)
		if err != nil {
			return fmt.Errorf("读取文件 %s 失败: %w", file, err)
		}
		if !e.Config.DryRun {
			if !force { // 正常模式：检查历史决定是否跳过
				if isNew, herr := e.History.CheckAndRecord(hash); herr != nil {
					return fmt.Errorf("检查文件 %s 的历史记录失败: %w", file, herr)
				} else if !isNew { // 已存在
					logger.Log().Warn("跳过重复文件", "path", file)
					skipped++
					continue
				}
			} else { // ForceRefresh: 总是记录（写入历史），不跳过
				if _, herr := e.History.CheckAndRecord(hash); herr != nil {
					return fmt.Errorf("强制记录文件 %s 失败: %w", file, herr)
				}
			}
		}
		if _, exists := e.FileCache[hash]; exists {
			logger.Log().Warn("跳过重复文件", "path", file)
			skipped++
			continue
		}
		e.FileCache[hash] = FileCache{Path: file, Content: content, Hash: hash}
		processed++
	}
	logger.Log().Info("文件收集完成", "total", len(sourceFiles), "processed", processed, "skipped", skipped, "forceRefresh", force)

	if processed == 0 {
		return ErrNoInputFiles
	}

	// 3. 根据模式（合并/分散）生成导出计划
	plans, err := e.generatePlans(e.FileCache)

	if err != nil {
		return fmt.Errorf("生成计划失败: %w", err)
	}

	// 4. 预览或执行计划
	if e.Config.DryRun {
		e.previewPlans(plans)
		return nil
	}
	jsonPayload, layerCount, featCount, err := e.executePlans(plans)
	if err != nil {
		return fmt.Errorf("执行计划失败: %w", err)
	}

	//5. 调用 Python 导出器
	if len(jsonPayload) > 0 {
		logger.Log().Info("> Python 导出", "files", layerCount, "features", featCount)
		err = e.InvokePythonExporter(jsonPayload)
		if err != nil {
			return fmt.Errorf("调用 Python 导出失败: %w", err)
		}
	} else {
		logger.Log().Info("没有可导出的数据，跳过 Python 调用")
	}
	return nil
}
