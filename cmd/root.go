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
		// 交互式选择输出格式
		var formatKey string
		fmt.Println("请选择输出格式:")
		fmt.Println("1. SHP (ESRI Shapefile)")
		fmt.Println("2. FGB (FlatGeobuf)")
		fmt.Println("3. GPKG (GeoPackage)")
		fmt.Println("4. GDB (OpenFileGDB)")
		fmt.Print("输入序号并回车: ")
		var choice int
		fmt.Scanln(&choice)
		switch choice {
		case 1:
			formatKey = "SHP"
		case 2:
			formatKey = "FGB"
		case 3:
			formatKey = "GPKG"
		case 4:
			formatKey = "GDB"
		default:
			fmt.Println("无效选择，默认使用 FGB 格式。")
			formatKey = "FGB"
		}

		fmt.Printf("已选择格式: %s\n", formatKey)
		fmt.Println("请按下 Enter 键执行导出...")
		fmt.Scanln()

		exporter, err := export.NewExporter(export.ExportConfig{
			InputPaths: args,
			Depth:      0,
			FormatKey:  formatKey,
			OutputDir:  "output",
			Merge:      false,
		})
		if err != nil {
			return fmt.Errorf("创建导出器失败: %w", err)
		}

		logger.Log().Debug("开始执行导出器")

		execErr := exporter.Execute()
		if execErr != nil {
			logger.Log().Error("导出失败", "error", execErr)
		} else {
			logger.Log().Info("导出成功完成！")
		}

		fmt.Println("操作已完成，请按下 Enter 键退出程序...")
		fmt.Scanln()

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
