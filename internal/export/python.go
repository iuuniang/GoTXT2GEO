/*
Copyright © 2025 TheMachine <592858548@qq.com>
*/
package export

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"txt2geo/internal/pyscript"
	"txt2geo/pkg/environ"
	"txt2geo/pkg/logger"
)

const (
	// 定义子进程执行的超时时间
	executionTimeout = 60 * time.Second
)

// mapPythonLogLevel 将从 Python 日志中解析出的级别字符串映射到 slog.Level。
func mapPythonLogLevel(levelStr string) slog.Level {
	switch strings.ToUpper(levelStr) {
	case "DEBUG":
		return slog.LevelDebug
	case "INFO":
		return slog.LevelInfo
	case "WARNING", "WARN":
		return slog.LevelWarn
	case "ERROR", "CRITICAL":
		return slog.LevelError
	default:
		return slog.LevelInfo // 默认为 Info 级别
	}
}

func (e *Exporter) InvokePythonExporter(payload []byte, totalFiles, totalFeatures int) error {
	logger.Log().Debug("准备通过标准 I/O 调用 Python 脚本", "payloadSize", len(payload))

	// 1. 配置运行环境
	prefixPath, pythonPath, err := environ.InitializeQGISEnvironment()
	if err != nil {
		return fmt.Errorf("初始化 QGIS 环境失败: %w", err)
	}

	// 2. 设置带超时的上下文
	ctx, cancel := context.WithTimeout(context.Background(), executionTimeout)
	defer cancel()

	// 3. 创建执行命令，使用 -c 标志
	// 第一个参数是 "-c"，第二个参数是脚本的完整内容
	cmd := exec.CommandContext(ctx, pythonPath, "-c", pyscript.GeoExport, prefixPath)

	// 4. 获取标准输出和标准错误的管道
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("创建 stdout 管道失败: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("创建 stderr 管道失败: %w", err)
	}
	cmd.Stdin = bytes.NewReader(payload)
	// 5. 启动命令（非阻塞）
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 Python 脚本失败: %w", err)
	}

	var wg sync.WaitGroup
	var resultsCount atomic.Int64

	// 6. 并发、实时地处理 stderr
	wg.Go(func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			line := scanner.Text()
			parts := strings.SplitN(line, " - ", 2)
			if len(parts) == 2 {
				levelStr, message := parts[0], parts[1]
				slogLevel := mapPythonLogLevel(levelStr)
				logger.Log().Log(context.Background(), slogLevel, fmt.Sprintf("  >> [Python] %s", message))

			} else {
				logger.Log().Warn(fmt.Sprintf("  >> [Python] %s", line))
			}
		}
	})

	// 7. 并发、实时地处理 stdout
	wg.Go(func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			var res map[string]any
			line := scanner.Bytes() // 使用 Bytes() 避免不必要的字符串转换

			if err := json.Unmarshal(line, &res); err != nil {
				logger.Log().Error("解析 Python stdout 行失败", "error", err, "line", string(line))
				continue
			}

			// 实时处理 hash
			if hash, ok := res["hash"].(string); ok && hash != "" {
				if e.History != nil {
					e.History.CheckAndRecord(hash)
				}
			}
			resultsCount.Add(1)
		}
	})

	// 8. 等待所有流处理完成
	wg.Wait()

	// 9. 等待命令执行结束并获取最终错误状态
	err = cmd.Wait()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("python 脚本执行超时 (%v)", executionTimeout)
		}
		return fmt.Errorf("执行 Python 脚本失败: %w", err)
	}
	count := resultsCount.Load()
	if count > 0 {
		logger.Log().Info(fmt.Sprintf("> Python 脚本成功处理了 %d 个文件，共 %d 个要素。", totalFiles, totalFeatures))
	} else {
		logger.Log().Info("> Python 脚本执行完成，没有文件需要处理。")
	}
	return nil
}
