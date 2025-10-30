/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"txt2geo/internal/export"
	"txt2geo/pkg/logger"

	"github.com/spf13/cobra"
)

// 命令行参数变量
var (
	exportInputPaths   []string
	exportDepth        int
	exportFormatKey    string
	exportOutputDir    string
	exportMerge        bool
	exportNameTemplate string
	exportDryRun       bool
	exportOverwrite    bool
	exportForceRefresh bool
)

// exportCmd represents the export command
var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export geographic data to vector format",
	Long: `根据输入文件/目录，生成矢量数据导出计划，支持合并/分散模式、名称模板与多格式输出。

名称模板占位符:
  * {name}: 基础名称 (分散模式下为源文件名，合并模式下为 "merged_output" 或外部传入)。
  * {index[:width]}: 当前处理文件的序号，支持用 :width 指定补零宽度 (如 {index:03})。
  * {count}: 本次任务处理的总文件数。
  * {date[:layout]}: 当前日期，支持用 :layout 指定 Go 时间格式 (默认 20060102)。
  * {uuid}: 一个随机的 UUID v4 字符串。
  * {rand[:len]}: 一个随机的字母数字字符串，支持用 :len 指定长度 (默认 8 位)。

所有占位符都支持大小写修饰符，例如 {name:upper} 会将名称转换为大写。

模式:
  * 默认: 分散模式，即每个输入文件生成一个独立的输出文件。
  * --merge: 合并模式，将所有输入合并处理，生成单一输出。

示例:
  # 分散导出 data 目录下的文件，输出名如 "file1_001.fgb"
  geoflow export -i data -o out --format FGB --name "{name:lower}_{index:03}" --dry-run

  # 合并 a.txt 和 b.txt，输出名为 "blocks_2025-10-29.gpkg"
  geoflow export -i a.txt -i b.txt -o out --merge --format GPKG --name "blocks_{date:2006-01-02}" --dry-run
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		exporter, err := export.NewExporter(export.ExportConfig{
			InputPaths:   exportInputPaths,
			Depth:        exportDepth,
			FormatKey:    exportFormatKey,
			OutputDir:    exportOutputDir,
			Merge:        exportMerge,
			NameTemplate: exportNameTemplate,
			DryRun:       exportDryRun,
			Overwrite:    exportOverwrite,
			ForceRefresh: exportForceRefresh,
		})
		if err != nil {
			logger.Log().Error("创建导出器失败", "error", err)
			return fmt.Errorf("创建导出器失败: %w", err)
		}
		logger.Log().Debug("开始执行导出器")
		return exporter.Execute()
	},
}

func init() {
	rootCmd.AddCommand(exportCmd)

	exportCmd.Flags().StringArrayVarP(&exportInputPaths, "input", "i", nil, "输入文件或目录，可重复指定")
	exportCmd.Flags().IntVar(&exportDepth, "depth", -1, "递归深度：0=仅当前目录，正数=最大层级，-1=无限")
	exportCmd.Flags().StringVar(&exportFormatKey, "format", "FGB", "输出格式：SHP|FGB|GPKG|GDB，默认 FGB")
	exportCmd.Flags().StringVarP(&exportOutputDir, "output", "o", "", "输出目录")
	exportCmd.Flags().BoolVar(&exportMerge, "merge", false, "合并导出")
	exportCmd.Flags().StringVar(&exportNameTemplate, "name", "", "文件名模板，支持占位符 {name}{index}{date}{uuid}{rand}{count}")
	exportCmd.Flags().BoolVar(&exportDryRun, "dry-run", false, "仅预览导出计划，不执行写入")
	exportCmd.Flags().BoolVar(&exportOverwrite, "overwrite", false, "允许覆盖已存在的目标文件")
	exportCmd.Flags().BoolVar(&exportForceRefresh, "force-refresh", false, "强制重新处理，忽视processed已存在的条目")

	_ = exportCmd.MarkFlagRequired("input")
	_ = exportCmd.MarkFlagRequired("output")
}
