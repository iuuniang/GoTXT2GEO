/*
Copyright © 2025 TheMachine <592858548@qq.com>
*/
package cmd

import (
	"fmt"
	"os"
	"strings"
	"txt2geo/internal/export"
	"txt2geo/internal/version"
	"txt2geo/pkg/logger"

	"github.com/spf13/cobra"
)

var logLevel string

// rootCmd represents the base command when called without any subcommandsgo
var rootCmd = &cobra.Command{
	Use:     "txt2geo [input-paths...]",
	Short:   "文本文件转地理数据工具",
	Long:    "TXT2GEO 是一个将文本文件转换为各种地理数据格式的工具。支持多种输出格式，方便用户进行地理数据处理和分析。",
	Args:    cobra.MinimumNArgs(1),
	Version: version.Version,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		logger.Init(logLevel)
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		logger.Log().Info("正在以默认配置快速处理...")
		// 扫描并显示文件摘要
		logger.Log().Info("文件扫描完成", "总计", len(args))
		for i, path := range args {
			info, err := os.Stat(path)
			var pathType string
			if err != nil {
				pathType = " (无法访问)"
			} else if info.IsDir() {
				pathType = " (文件夹)"
			} else if strings.HasSuffix(strings.ToLower(path), ".txt") {
				pathType = " (TXT 文件)"
			} else {
				pathType = " (其他文件)"
			}
			logger.Log().Info(fmt.Sprintf("  %d. %s%s", i+1, path, pathType))
		}
		exporter, err := export.NewExporter(export.ExportConfig{
			InputPaths: args,
			Depth:      0,
			FormatKey:  "FGB",
			OutputDir:  "output",
			Merge:      false,
		})
		if err != nil {
			return fmt.Errorf("创建导出器失败: %w", err)
		}

		fmt.Println("请按下 Enter 键执行导出...")
		fmt.Scanln() // 等待用户按下 Enter

		logger.Log().Debug("开始执行导出器")

		// 执行导出，并保存可能发生的错误
		execErr := exporter.Execute()
		if execErr != nil {
			logger.Log().Error("导出失败", "error", execErr)
		} else {
			logger.Log().Info("导出成功完成！")
		}

		fmt.Println("操作已完成，请按下 Enter 键退出程序...")
		fmt.Scanln() // 等待用户按键后退出

		// 在所有交互结束后，返回执行过程中遇到的错误
		return execErr
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.MousetrapHelpText = ""
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "Set log levels (debug, info, warn, error)")
}
