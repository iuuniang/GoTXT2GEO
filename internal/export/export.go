/*
Copyright © 2025 TheMachine <592858548@qq.com>
*/
package export

import (
	"errors"
	"fmt"
	"txt2geo/internal/domain"
	"txt2geo/internal/process"
	"txt2geo/pkg/charset"
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

// ProcessedFile 存储已成功处理的文件结果
type ProcessedFile struct {
	FileCache FileCache
	Features  []map[string]any
	CRS       string
	EPSG      int
}

// Exporter 是负责执行整个导出流程的协调器。
type Exporter struct {
	Config        ExportConfig
	History       *process.ProcessHistory
	FileCache     map[string]FileCache
	ProcessedData map[string]*ProcessedFile // 存储已处理成功的文件数据
	UsedNames     map[string]struct{}
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
		Config:        config,
		History:       history,
		FileCache:     make(map[string]FileCache),
		ProcessedData: make(map[string]*ProcessedFile),
		UsedNames:     make(map[string]struct{}),
	}, nil
}

// processSingleFileResult 存储单个文件成功处理后的结果（内部使用）
type processSingleFileResult struct {
	Features []map[string]any
	CRS      string
	EPSG     int
}

// processSingleFile 封装了处理单个文件的完整逻辑。
func (e *Exporter) processSingleFile(fileData FileCache) (*processSingleFileResult, error) {
	logger.Log().Debug("正在处理文件", "path", fileData.Path, "size", len(fileData.Content))
	text, _, err := charset.Decode(fileData.Content)
	if err != nil {
		return nil, fmt.Errorf("文件解码失败: %w", err)
	}
	parsed, err := domain.Parse(text)
	if err != nil {
		return nil, fmt.Errorf("文件解析失败: %w", err)
	}
	prepData, err := domain.BuildGeometryPreprocessData(parsed, domain.GeometryOptions{Deduplicate: true, AutoClose: true})
	if err != nil {
		return nil, fmt.Errorf("几何预处理数据构建失败: %w", err)
	}

	if len(prepData.Features) == 0 {
		return nil, nil // 没有错误，但也没有要素
	}

	featList := make([]map[string]any, 0, len(prepData.Features))
	for _, feat := range prepData.Features {
		featList = append(featList, map[string]any{"wkt": feat.WKT, "properties": feat.Attributes})
	}

	return &processSingleFileResult{
		Features: featList,
		CRS:      prepData.CRS,
		EPSG:     prepData.EPSG,
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

	// 3. 预处理所有文件，只保留成功处理的文件
	var processFailed int
	for hash, fileData := range e.FileCache {
		result, err := e.processSingleFile(fileData)
		if err != nil {
			logger.Log().Error("文件预处理失败", "path", fileData.Path, "error", err)
			processFailed++
			delete(e.FileCache, hash) // 从缓存中移除失败的文件
			continue
		}
		if result == nil {
			logger.Log().Warn("文件无有效要素", "path", fileData.Path)
			processFailed++
			delete(e.FileCache, hash)
			continue
		}
		e.ProcessedData[hash] = &ProcessedFile{
			FileCache: fileData,
			Features:  result.Features,
			CRS:       result.CRS,
			EPSG:      result.EPSG,
		}
	}

	logger.Log().Info("文件预处理完成", "success", len(e.ProcessedData), "failed", processFailed)

	if len(e.ProcessedData) == 0 {
		return ErrNoInputFiles
	}

	// 4. 根据模式（合并/分散）生成导出计划
	plans, err := e.generatePlans(e.FileCache)

	if err != nil {
		return fmt.Errorf("生成计划失败: %w", err)
	}

	// 5. 预览或执行计划
	if e.Config.DryRun {
		e.previewPlans(plans)
		return nil
	}
	result, err := e.executePlans(plans)
	if err != nil {
		return fmt.Errorf("执行计划失败: %w", err)
	}

	logger.Log().Info("数据组装完成", "success", result.SuccessCount, "failure", result.FailureCount)

	//6. 调用 Python 导出器
	if len(result.Payload) > 0 {
		logger.Log().Info("> Python 导出", "files", result.LayerCount, "features", result.FeatureCount)
		err = e.InvokePythonExporter(result.Payload, result.LayerCount, result.FeatureCount)
		if err != nil {
			return fmt.Errorf("调用 Python 导出失败: %w", err)
		}
	} else {
		logger.Log().Info("没有可导出的数据，跳过 Python 调用")
	}
	return nil
}
