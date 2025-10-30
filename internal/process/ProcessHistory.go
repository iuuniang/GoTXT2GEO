/*
Copyright © 2025 TheMachine <592858548@qq.com>
*/
package process

import (
	"bufio"
	"fmt"
	"os"
	"sync"
	"txt2geo/pkg/logger"
)

// ProcessHistory 管理文件收集、内容读取（带哈希）以及已处理文件的记录（避免重复处理）。
type ProcessHistory struct {
	processedFile string
	processed     map[string]struct{}
	mu            sync.RWMutex
}

// NewProcessHistory 创建一个 ProcessHistory 并尝试加载已处理文件记录。
func NewProcessHistory(processedFile string) (*ProcessHistory, error) {
	fm := &ProcessHistory{
		processedFile: processedFile,
		processed:     make(map[string]struct{}),
	}

	if processedFile == "" {
		// 即使没有记录文件，管理器本身依然可用，用于处理会话内的重复项。
		return fm, nil
	}

	if err := fm.loadProcessed(); err != nil {
		// 如果文件不存在，我们不认为这是一个错误，而是正常启动。
		if !os.IsNotExist(err) {
			return nil, err
		}
	}
	logger.Log().Debug("ProcessHistory 初始化完成", "file", processedFile, "count", len(fm.processed))
	return fm, nil
}

// CheckAndRecord 原子地检查哈希是否存在，如果不存在则记录，并返回是否为新记录。
// 返回值 isNew 为 true 表示这是一个新的哈希，文件应该被处理。
// 返回值 isNew 为 false 表示哈希已存在（来自历史记录或本次运行），文件应被跳过。
func (fm *ProcessHistory) CheckAndRecord(hash string) (isNew bool, err error) {
	if hash == "" {
		return false, nil
	}

	// 第一步：使用读锁快速检查。这是为了在大多数情况下（文件已处理）提高并发性能。
	fm.mu.RLock()
	_, exists := fm.processed[hash]
	fm.mu.RUnlock()
	if exists {
		return false, nil // 哈希已存在，直接返回。
	}

	// 第二步：如果不存在，则获取写锁准备写入。
	fm.mu.Lock()
	defer fm.mu.Unlock()

	// 双重检查：在获取写锁后，必须再次检查哈希是否存在。
	// 这是因为在读锁释放和写锁获取的间隙，可能有另一个goroutine已经写入了相同的哈希。
	if _, exists := fm.processed[hash]; exists {
		return false, nil
	}

	// 记录到文件中（如果配置了文件路径）
	if fm.processedFile != "" {
		f, err := os.OpenFile(fm.processedFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return false, fmt.Errorf("无法打开 %s 进行写入: %w", fm.processedFile, err)
		}
		defer f.Close()

		if _, err := f.WriteString(hash + "\n"); err != nil {
			return false, fmt.Errorf("无法写入 %s: %w", fm.processedFile, err)
		}
	}

	// 在内存中标记为已处理
	fm.processed[hash] = struct{}{}
	logger.Log().Debug("记录新哈希", "hash", hash)

	// 确认是新记录
	return true, nil
}

// loadProcessed 从文件中加载已处理的哈希。
func (fm *ProcessHistory) loadProcessed() error {
	file, err := os.Open(fm.processedFile)
	if err != nil {
		// 如果文件不存在，这不是一个错误，程序将创建一个新文件。
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("无法打开 %s: %w", fm.processedFile, err)
	}
	defer file.Close()

	fm.mu.Lock()
	defer fm.mu.Unlock()

	scanner := bufio.NewScanner(file)
	var count int
	for scanner.Scan() {
		hash := scanner.Text()
		if hash == "" {
			continue
		}
		fm.processed[hash] = struct{}{}
		count++
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	logger.Log().Debug("加载已处理哈希", "file", fm.processedFile, "count", count)
	return nil
}
