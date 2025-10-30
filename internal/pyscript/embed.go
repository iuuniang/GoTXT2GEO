/*
Copyright © 2025 TheMachine <592858548@qq.com>
*/
package pyscript

import (
	_ "embed"
	"fmt"
	"os"
)

// GeoExport 是实际的地理导出实现脚本。
//
//go:embed geoexport.py
var GeoExport string

// WriteToTempFile 将嵌入的 geoexport.py 脚本写入一个临时文件并返回其路径。
// 调用者有责任在使用后删除该文件。

func WriteToTempFile() (string, error) {
	// 创建一个临时文件，文件名以 "geoexport_" 开头，以 ".py" 结尾
	tmpFile, err := os.CreateTemp("", "geoexport_*.py")
	if err != nil {
		return "", fmt.Errorf("无法创建临时脚本文件: %w", err)
	}
	defer tmpFile.Close()

	// 将嵌入的脚本内容写入文件
	if _, err := tmpFile.WriteString(GeoExport); err != nil {
		// 如果写入失败，尝试关闭并删除文件
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("无法将脚本写入临时文件: %w", err)
	}

	// 返回临时文件的完整路径
	return tmpFile.Name(), nil
}
