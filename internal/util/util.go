/*
Copyright © 2025 TheMachine <592858548@qq.com>
*/
package util

import (
	"crypto/rand"
	"fmt"
)

// 计算有符号整数的位数（忽略负号）
func IntDigits(n int) int {
	if n == 0 {
		return 1 // 边界值：0 的位数是 1
	}
	count := 0
	// 处理负数：先取绝对值
	if n < 0 {
		n = -n
	}
	// 循环除以 10 计数
	for n > 0 {
		n = n / 10
		count++
	}
	return count
}

func RandomString(n int) string {
	letters := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range n {
		b[i] = letters[int(b[i])%len(letters)]
	}
	return string(b)
}

func GetUUIDv4() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
