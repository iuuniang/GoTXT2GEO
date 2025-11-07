/*
Copyright © 2025 TheMachine <592858548@qq.com>
*/
package export

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"txt2geo/internal/util"
	"txt2geo/pkg/logger"
	"txt2geo/pkg/namex"
	"txt2geo/pkg/pathx"
)

const defaultMergeName = "merged_output"

// ExportPlan 定义了单个导出任务的源和目标。
type ExportPlan struct {
	SourceHashes []string // 源文件哈希
	OutputTarget string   // 目标容器路径（文件或数据库）
	OutputName   string   // 目标名称（文件名或图层名）
}

// displayTarget 返回用于日志展示的目标字符串：
//   - 容器格式： containerPath|layerName
//   - 非容器格式： 完整文件路径
func (p ExportPlan) displayTarget(isContainer bool) string {
	if isContainer {
		return fmt.Sprintf("%s|%s", p.OutputTarget, p.OutputName)
	}
	return filepath.Join(p.OutputTarget, p.OutputName)
}

// generatePlans 根据源文件和配置创建导出计划列表。
func (e *Exporter) generatePlans(fileCache map[string]FileCache) ([]ExportPlan, error) {
	tmpl := strings.TrimSpace(e.Config.NameTemplate)
	formatDetails := e.Config.FormatDetails
	if e.UsedNames == nil {
		e.UsedNames = make(map[string]struct{})
	}

	// 构造一个统一的 item 列表，每个 item 提供源切片与输出名称基底
	type item struct {
		sourceHashes []string
		baseName     string
		index        int
	}
	items := make([]item, 0, len(fileCache))

	if e.Config.Merge {
		// 合并模式：单一计划，所有文件合并
		hashes := make([]string, 0, len(fileCache))
		for hash := range fileCache {
			hashes = append(hashes, hash)
		}
		items = append(items, item{sourceHashes: hashes, baseName: defaultMergeName, index: 1})
	} else {
		// 分散模式：每个文件一个计划
		for hash, cache := range fileCache {
			i := len(items) // 实际上可以使用一个单独的计数器，为了保持代码清晰
			stem, serr := pathx.Stem(cache.Path)
			if serr != nil || strings.TrimSpace(stem) == "" {
				stem = fmt.Sprintf("file_%d", i+1)
			}
			items = append(items, item{sourceHashes: []string{hash}, baseName: stem, index: i + 1})
		}
	}
	total := len(items)
	plans := make([]ExportPlan, 0, total)

	for _, it := range items {
		outputName := renderNameTemplate(tmpl, it.baseName, it.index, total)
		outputName = namex.Sanitize(outputName, e.UsedNames)

		if !formatDetails.IsContainer {
			outputName += formatDetails.Extension
		}
		plans = append(plans, ExportPlan{SourceHashes: it.sourceHashes, OutputTarget: e.Config.OutputDir, OutputName: outputName})
	}
	return plans, nil
}

// previewPlans 打印导出计划的预览信息。
func (e *Exporter) previewPlans(plans []ExportPlan) {
	total := len(plans)
	logger.Log().Info("预览导出计划", "merge", e.Config.Merge, "totalPlans", total, "format", e.Config.FormatKey)
	isContainer := e.Config.FormatDetails.IsContainer
	width := util.IntDigits(total)
	for i, plan := range plans {
		var src slog.Attr
		if len(plan.SourceHashes) > 1 {
			src = slog.Int("totalFiles", len(plan.SourceHashes))
		} else if len(plan.SourceHashes) == 1 {
			// 从 FileCache 获取原始路径用于显示
			if cache, ok := e.FileCache[plan.SourceHashes[0]]; ok {
				src = slog.String("source", cache.Path)
			}
		}
		progress := fmt.Sprintf("[%0*d/%d]", width, i+1, total)
		message := fmt.Sprintf("%12s", progress)
		logger.Log().Info(message, src, "target", plan.displayTarget(isContainer))
	}
}

// ExecutionResult 保存计划执行的结果。
type ExecutionResult struct {
	Payload      []byte
	SuccessCount int
	FailureCount int
	LayerCount   int
	FeatureCount int
}

// executePlans 实际执行所有导出任务。
func (e *Exporter) executePlans(plans []ExportPlan) (*ExecutionResult, error) {
	total := len(plans)
	logger.Log().Info("执行导出计划", "merge", e.Config.Merge, "totalPlans", total, "format", e.Config.FormatKey)
	isContainer := e.Config.FormatDetails.IsContainer
	width := util.IntDigits(total)

	var (
		targetCRS    string // 所有文件的目标坐标系
		featureTotal int    // 总要素图形（地块）数量
	)
	datasets := make([]map[string]any, 0, total)

	for i, plan := range plans {
		layerName := plan.OutputName
		for _, hash := range plan.SourceHashes {
			if processedFile, ok := e.ProcessedData[hash]; ok {
				if targetCRS == "" && processedFile.EPSG > 0 {
					targetCRS = fmt.Sprintf("EPSG:%d", processedFile.EPSG)
				}

				featureTotal += len(processedFile.Features) // 统计要素数量
				datasets = append(datasets, map[string]any{
					"layer_name":     layerName,
					"source_path":    processedFile.FileCache.Path,
					"source_crs":     processedFile.CRS,
					"features":       processedFile.Features,
					"total_features": len(processedFile.Features),
					"hash":           processedFile.FileCache.Hash,
				})
			}
		}

		var src slog.Attr
		if len(plan.SourceHashes) > 1 {
			src = slog.Int("totalFiles", len(plan.SourceHashes))
		} else if len(plan.SourceHashes) == 1 {
			// 从 ProcessedData 获取原始路径用于显示
			if processedFile, ok := e.ProcessedData[plan.SourceHashes[0]]; ok {
				src = slog.String("source", processedFile.FileCache.Path)
			}
		}
		progress := fmt.Sprintf("[%0*d/%d]", width, i+1, total)
		message := fmt.Sprintf("%12s", progress)
		logger.Log().Info(message, src, "target", plan.displayTarget(isContainer))
	}

	if len(datasets) == 0 {
		return &ExecutionResult{
			SuccessCount: 0,
			FailureCount: 0,
		}, nil
	}

	root := map[string]any{
		"output_dir": e.Config.OutputDir,
		"driver":     e.Config.FormatDetails.Driver,
		"target_crs": targetCRS,
		"merge":      e.Config.Merge,
		"overwrite":  e.Config.Overwrite,
		"datasets":   datasets,
	}
	data, err := json.Marshal(root)
	if err != nil {
		return nil, err
	}
	return &ExecutionResult{
		Payload:      data,
		SuccessCount: len(datasets),
		FailureCount: 0,
		LayerCount:   total,
		FeatureCount: featureTotal,
	}, nil
}
