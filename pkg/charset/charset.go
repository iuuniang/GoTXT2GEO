/*
Copyright © 2025 TheMachine <592858548@qq.com>
*/
package charset

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// 该文件实现对常见中文文本文件编码 (UTF-8/BOM, UTF-16LE/BE, GB18030) 的轻量检测与统一解码。
// 设计目标：
//   * 避免引入重量级探测库；
//   * 提供确定性（非概率排名）结果，无法判定时返回 EncodingUnknown；
//   * 对截断/少量损坏的 UTF-8/UTF-16 具备一定容错能力并做替换统计。
//
// 公开函数：
//   Detect(data) -> 粗略检测编码标识；
//   Decode(data)  -> 返回 UTF-8 文本及原编码标识，并在必要时返回警告错误。
//
// 注意：探测是启发式的，极端短样本或混合编码内容可能仍得到 Unknown。
// 调用方如需更强能力，可在 Unknown 分支再接入外部库。

// Supported encodings 标识字符串常量。
const (
	EncodingUTF8    = "utf-8"
	EncodingUTF8BOM = "utf-8-sig"
	EncodingUTF16LE = "utf-16-le"
	EncodingUTF16BE = "utf-16-be"
	EncodingGB18030 = "gb18030"
	EncodingUnknown = "unknown"
)

// Detect 通过 BOM、UTF-8/UTF-16/GB18030 的字节模式与启发式规则检测给定字节切片的编码。
// 支持的编码包括：utf-8-sig, utf-8, utf-16-le, utf-16-be, gb18030。
// 若无法确定，返回 EncodingUnknown。空数据视作 UTF-8。
func Detect(data []byte) string {
	if len(data) == 0 {
		return EncodingUTF8 // treat empty as utf-8
	}

	// 1. BOM detection
	if bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}) {
		return EncodingUTF8BOM
	}
	if len(data) >= 2 {
		if data[0] == 0xFF && data[1] == 0xFE { // LE BOM
			return EncodingUTF16LE
		}
		if data[0] == 0xFE && data[1] == 0xFF { // BE BOM
			return EncodingUTF16BE
		}
	}

	// Early binary noise exclusion: if many zero bytes but not plausible UTF-16 layout
	zeroBytes := 0
	for _, b := range data {
		if b == 0 {
			zeroBytes++
		}
	}
	if zeroBytes > len(data)/2 {
		// fallthrough but we will rely on distribution checks
	}

	// 2. Manual UTF-8 validation (tolerate truncated final sequence)
	if validUTF8StrictOrTrunc(data) {
		return EncodingUTF8
	}

	// 3. Try UTF-16 without BOM
	utf16Guess := guessUTF16(data)
	if utf16Guess != "" {
		return utf16Guess
	}

	// 4. Fallback: attempt GB18030 strict decode
	if isGB18030(data) {
		return EncodingGB18030
	}

	return EncodingUnknown
}

// guessUTF16 尝试通过零字节分布、高字节模式及解码评估分数来探测无 BOM 的 UTF-16 编码。
// 这是一个内部辅助函数，具有较高的防误判门槛。
func guessUTF16(data []byte) string {
	if len(data) < 4 { // 太短不判断无 BOM UTF-16
		return ""
	}

	// 旧的零字节分布启发：仍保留，用于快速高置信度路径
	evenZeros, oddZeros := 0, 0
	for i, b := range data {
		if b == 0 {
			if i%2 == 0 {
				evenZeros++
			} else {
				oddZeros++
			}
		}
	}
	half := len(data) / 2
	if half == 0 {
		return ""
	}
	evenRatio := float64(evenZeros) / float64(half)
	oddRatio := float64(oddZeros) / float64(half)

	const high = 0.30
	const low = 0.05

	forceDecode := false
	leCandidate, beCandidate := false, false
	if oddRatio > high && evenRatio < low {
		leCandidate = true
	}
	if evenRatio > high && oddRatio < low {
		beCandidate = true
	}

	// 短中文文本（纯 CJK）零字节往往很少：通过“高字节落在 CJK 常用区”模式识别
	if !leCandidate && !beCandidate && len(data) >= 4 && len(data)%2 == 0 {
		halfBytes := len(data) / 2
		leHigh, beHigh := 0, 0
		for i := 0; i+1 < len(data); i += 2 { // (low, high) 假设为 LE
			highLE := data[i+1]
			if highLE >= 0x4E && highLE <= 0x9F {
				leHigh++
			}
			highBE := data[i] // (high, low) 假设为 BE
			if highBE >= 0x4E && highBE <= 0x9F {
				beHigh++
			}
		}
		leHighRatio := float64(leHigh) / float64(halfBytes)
		beHighRatio := float64(beHigh) / float64(halfBytes)
		// 典型 UTF-16LE 中文：奇数位(高字节)集中在 0x4E~0x9F；BE 则偶数位集中。
		// 设阈值：一侧 ≥0.75 且另一侧 <0.60 作为显著指示。
		if leHighRatio >= 0.75 && beHighRatio < 0.60 {
			leCandidate = true
		}
		if beHighRatio >= 0.75 && leHighRatio < 0.60 {
			beCandidate = true
		}
	}

	// 如果仍未触发，但长度稍微大一些（≥8）且为偶数，尝试双向解码评分。
	if !leCandidate && !beCandidate && len(data) >= 8 && len(data)%2 == 0 {
		forceDecode = true
		leCandidate, beCandidate = true, true
	}

	if !leCandidate && !beCandidate {
		return ""
	}

	leEval := evaluateUTF16(data, true)
	beEval := evaluateUTF16(data, false)

	// 最低可接受条件（防止把随机二进制当作 UTF-16）
	const (
		minPrintableRatio = 0.80 // 可打印+中文占比
		maxControlRatio   = 0.05
		maxWeirdRatio     = 0.02 // 非字符/孤立代理
	)

	pick := func(ev utf16Eval, encoding string) string {
		if !ev.validStructure {
			return ""
		}
		if ev.printableRatio < minPrintableRatio {
			return ""
		}
		if ev.controlRatio > maxControlRatio {
			return ""
		}
		if ev.weirdRatio > maxWeirdRatio {
			return ""
		}
		// 在强制双分支模式下，为避免与 GB18030 竞争，再加一个区分度：
		// UTF-16 典型模式：ASCII+中文覆盖率较高且单字节分布不呈现典型 GB18030 的高频双字节范围。
		// 粗略用 ascii 或 中文任一占比 > 5% 来增强可信度。
		if forceDecode && ev.asciiRatio < 0.05 && ev.cjkRatio < 0.05 {
			return ""
		}
		// 额外短样本防误判：针对短 (<24 字节) 且无零字节分布、纯 UTF-8 中文被拆成伪 UTF-16 的情况
		// 条件：强制解码路径 + 样本字节长度 <24 + zero bytes ==0 + CJK 比例处于中间值 (0< cjk <0.6)
		if forceDecode && len(data) < 24 && evenZeros+oddZeros == 0 {
			if ev.cjkRatio > 0 && ev.cjkRatio < 0.60 {
				return ""
			}
		}
		// 如果备选也满足且分数更高，后续再比较。
		return encoding
	}

	var leOk, beOk string
	if leCandidate {
		leOk = pick(leEval, EncodingUTF16LE)
	}
	if beCandidate {
		beOk = pick(beEval, EncodingUTF16BE)
	}

	// 计算 GB18030 模式置信度（仅在无 BOM & 零字节稀少时才有意义）
	gbPairRatio, asciiRunRatio := gb18030PatternConfidence(data)

	// 新增第一道拦截：若 UTF-16 两端候选均为低分、且 GB18030 有较高双字节合法对比例，则优先 GB18030
	// 条件：
	//   1. (leOk 或 beOk 存在) 且其 compositeScore < 0.90
	//   2. 零字节总数极低（≤1%）
	//   3. gbPairRatio ≥ 0.28 （经验阈值）
	//   4. asciiRunRatio < 0.40 （避免把大量 ASCII + 少量高字节当 GB）
	lowZero := float64(evenZeros+oddZeros)/float64(len(data)) <= 0.01
	utf16LowScore := (leOk != "" && leEval.compositeScore < 0.90) || (beOk != "" && beEval.compositeScore < 0.90)
	if utf16LowScore && lowZero && gbPairRatio >= 0.28 && asciiRunRatio < 0.40 {
		if isGB18030(data) {
			return EncodingGB18030
		}
	}

	// 第二道拦截（原始逻辑增强版）：UTF-16 低分 + GB18030 可严格解码
	minScore := 0.90
	if (leOk != "" && leEval.compositeScore < minScore) || (beOk != "" && beEval.compositeScore < minScore) {
		if isGB18030(data) {
			return EncodingGB18030
		}
	}

	if leOk == "" && beOk == "" {
		return ""
	}
	if leOk != "" && beOk == "" {
		return leOk
	}
	if beOk != "" && leOk == "" {
		return beOk
	}

	// 都有效，按综合分决定；分数接近时优先零字节模式匹配的那个
	if leEval.compositeScore > beEval.compositeScore {
		return EncodingUTF16LE
	}
	if beEval.compositeScore > leEval.compositeScore {
		return EncodingUTF16BE
	}
	// 分数相同：如果某个触发了明确零字节模式则优先它
	if oddRatio > high && evenRatio < low {
		return EncodingUTF16LE
	}
	if evenRatio > high && oddRatio < low {
		return EncodingUTF16BE
	}
	return "" // 模棱两可，放弃，交给后续 UTF-8/GB18030 逻辑
}

// gb18030PatternConfidence 粗略评估原始字节序列中“看起来像 GB/GB18030 双字节模式”的比例与 ASCII 连续度：
// 返回：
//
//	pairRatio: 识别出的合法 (lead, trail) 双字节对数量 / 潜在对数量（扫描步进1）
//	asciiRunRatio: 最长 ASCII 连续段长度 / 总长度
//
// 说明：不做完整 GB18030 四字节合法性验证，仅区分常见双字节模式，避免与伪 UTF-16 冲突。
func gb18030PatternConfidence(data []byte) (pairRatio float64, asciiRunRatio float64) {
	if len(data) < 4 { // 太短无意义
		return 0, 0
	}
	totalPairs := 0
	validPairs := 0
	longestASCII := 0
	currentASCII := 0
	for i := 0; i < len(data)-1; i++ {
		b := data[i]
		if b < 0x80 { // ASCII
			currentASCII++
		} else {
			if currentASCII > longestASCII {
				longestASCII = currentASCII
			}
			currentASCII = 0
		}
		lead := data[i]
		trail := data[i+1]
		// 典型 GBK/GB18030 双字节：lead 0x81-0xFE, trail 0x40-0xFE 且 !=0x7F
		if lead >= 0x81 && lead <= 0xFE {
			totalPairs++
			if (trail >= 0x40 && trail <= 0xFE && trail != 0x7F) || (trail >= 0x30 && trail <= 0x39) { // 兼容部分类路径
				validPairs++
			}
		}
	}
	if currentASCII > longestASCII {
		longestASCII = currentASCII
	}
	if totalPairs > 0 {
		pairRatio = float64(validPairs) / float64(totalPairs)
	}
	asciiRunRatio = float64(longestASCII) / float64(len(data))
	return
}

type utf16Eval struct {
	validStructure bool
	printableRatio float64
	controlRatio   float64
	weirdRatio     float64
	asciiRatio     float64
	cjkRatio       float64
	compositeScore float64
}

// evaluateUTF16 评估字节切片作为 UTF-16 (LE/BE) 的质量，返回包含多维度分数的评估结构。
// 维度包括：结构有效性（代理对）、可打印/控制/非字符/ASCII/CJK 字符比例及综合分。
func evaluateUTF16(data []byte, littleEndian bool) utf16Eval {
	ev := utf16Eval{}
	if len(data) < 2 {
		return ev
	}
	// 允许末尾单字节截断
	if len(data)%2 == 1 {
		data = data[:len(data)-1]
	}
	units := make([]uint16, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		var v uint16
		if littleEndian {
			v = uint16(data[i]) | uint16(data[i+1])<<8
		} else {
			v = uint16(data[i])<<8 | uint16(data[i+1])
		}
		units = append(units, v)
	}
	if len(units) == 0 {
		return ev
	}

	total := len(units)
	printable := 0
	control := 0
	weird := 0
	ascii := 0
	cjk := 0
	surrogateSingles := 0

	suspiciousHighBytes := 0
	for i := 0; i < total; i++ {
		u := units[i]
		// surrogate structure check
		if u >= 0xD800 && u <= 0xDBFF { // high surrogate
			if i+1 >= total || units[i+1] < 0xDC00 || units[i+1] > 0xDFFF {
				surrogateSingles++
			} else {
				// valid pair, skip low in iteration but still treat as printable composite
				i++
				printable++
				continue
			}
		} else if u >= 0xDC00 && u <= 0xDFFF { // low without preceding high
			surrogateSingles++
		}

		highByte := byte(u >> 8)
		lowByte := byte(u & 0xFF)
		// 统计“可疑高字节”：既不是 0x00 / ASCII 空白 / 常见 CJK 前缀 (0x4E-0x9F) / 0x30-0x39 (数字高字节不应出现) 且长度短
		if highByte != 0x00 && !(highByte >= 0x4E && highByte <= 0x9F) && highByte < 0xE0 {
			// 如果低字节是 0x30 且重复模式出现，增加惩罚（如 0x81 0x30 构造）
			if lowByte == 0x30 {
				suspiciousHighBytes++
			}
		}
		switch {
		case u == 0x0009 || u == 0x000A || u == 0x000D:
			printable++
		case u < 0x0020: // control
			control++
		case u <= 0x007F: // ASCII
			printable++
			ascii++
		case (u >= 0x4E00 && u <= 0x9FFF) || (u >= 0x3400 && u <= 0x4DBF): // CJK 基本 + 扩展A
			printable++
			cjk++
		case u == 0xFFFE || u == 0xFFFF || (u >= 0xFDD0 && u <= 0xFDEF): // 非字符
			weird++
		default:
			printable++
		}
	}

	// 结构有效性：不允许大量孤立代理
	if surrogateSingles > total/100 { // 允许极少量（截断边界）
		return ev
	}

	ev.validStructure = true
	ev.printableRatio = float64(printable) / float64(total)
	ev.controlRatio = float64(control) / float64(total)
	ev.weirdRatio = float64(weird+surrogateSingles) / float64(total)
	ev.asciiRatio = float64(ascii) / float64(total)
	ev.cjkRatio = float64(cjk) / float64(total)
	ev.compositeScore = ev.printableRatio*0.7 + (ev.cjkRatio+ev.asciiRatio)*0.3
	if total < 16 && suspiciousHighBytes > total/4 { // 短样本且出现较多可疑高字节模式 => 降低结构有效性
		ev.validStructure = false
	}
	return ev
}

// validUTF8StrictOrTrunc 校验字节序列是否为严格的 UTF-8，但允许末尾存在被截断的多字节序列。
func validUTF8StrictOrTrunc(data []byte) bool {
	i := 0
	for i < len(data) {
		b := data[i]
		if b < 0x80 { // ASCII
			i++
			continue
		}
		var need int
		switch {
		case b&0xE0 == 0xC0:
			need = 1
			if b < 0xC2 { // overlong
				return false
			}
		case b&0xF0 == 0xE0:
			need = 2
		case b&0xF8 == 0xF0:
			need = 3
			if b > 0xF4 {
				return false
			}
		default:
			return false
		}
		if i+need >= len(data) { // allow truncation at end
			return true
		}
		// continuation bytes
		for j := 1; j <= need; j++ {
			c := data[i+j]
			if c&0xC0 != 0x80 {
				return false
			}
		}
		// overlong / surrogate / range checks via decoding
		r, size := utf8.DecodeRune(data[i : i+need+1])
		if r == utf8.RuneError && size == 1 {
			return false
		}
		if r >= 0xD800 && r <= 0xDFFF {
			return false
		} // UTF-16 surrogate halves invalid in UTF-8
		if r > 0x10FFFF {
			return false
		}
		i += need + 1
	}
	return true
}

// isGB18030 尝试以严格模式将字节序列解码为 GB18030，若无解码错误则认为其是 GB18030 编码。
func isGB18030(data []byte) bool {
	dec := simplifiedchinese.GB18030.NewDecoder()
	tr := dec.Transformer
	dst := make([]byte, len(data)*4) // worst case expansion
	_, _, err := tr.Transform(dst, data, true)
	if errors.Is(err, transform.ErrShortDst) || errors.Is(err, transform.ErrShortSrc) {
		// grow and retry once; we still ignore counts since only error presence matters
		dst2 := make([]byte, len(dst)*2)
		_, _, err = tr.Transform(dst2, data, true)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	return true
}

// Decode 自动检测输入字节的编码并将其统一转换为 UTF-8 字符串。
//
// 返回：
//   - string: 转换后的 UTF-8 字符串。
//   - string: 检测到的原始编码（来自 Supported encodings）。
//   - error:  解码过程中遇到的问题。若仅为轻微问题（如非法序列替换），则返回警告性质的错误信息，此时字符串内容仍可用。
//
// 对于未知编码，会尝试按 UTF-8 进行容错修复并返回相应提示。
func Decode(data []byte) (string, string, error) {
	enc := Detect(data)
	switch enc {
	case EncodingUTF8BOM:
		if len(data) >= 3 {
			data = data[3:] // 去掉 BOM
		}
		return string(data), EncodingUTF8BOM, nil
	case EncodingUTF8:
		if utf8.Valid(data) {
			return string(data), EncodingUTF8, nil
		}
		// 虽检测为 UTF-8 但存在非法序列（极少见，可能截断），执行修复
		fixed, replaced := sanitizeInvalidUTF8(data)
		if replaced > 0 {
			return string(fixed), EncodingUTF8, fmt.Errorf("utf-8 含有 %d 处非法序列已替换", replaced)
		}
		return string(fixed), EncodingUTF8, nil
	case EncodingUTF16LE:
		utf8Bytes, rep, err := decodeUTF16(data, true)
		if err != nil {
			return string(utf8Bytes), EncodingUTF16LE, err
		}
		if rep > 0 {
			return string(utf8Bytes), EncodingUTF16LE, fmt.Errorf("utf-16-le 含有 %d 处非法代理对已替换", rep)
		}
		return string(utf8Bytes), EncodingUTF16LE, nil
	case EncodingUTF16BE:
		utf8Bytes, rep, err := decodeUTF16(data, false)
		if err != nil {
			return string(utf8Bytes), EncodingUTF16BE, err
		}
		if rep > 0 {
			return string(utf8Bytes), EncodingUTF16BE, fmt.Errorf("utf-16-be 含有 %d 处非法代理对已替换", rep)
		}
		return string(utf8Bytes), EncodingUTF16BE, nil
	case EncodingGB18030:
		dec := simplifiedchinese.GB18030.NewDecoder()
		utf8Bytes, err := dec.Bytes(data)
		if err != nil {
			return string(utf8Bytes), EncodingGB18030, fmt.Errorf("gb18030 解码失败: %w", err)
		}
		return string(utf8Bytes), EncodingGB18030, nil
	default: // Unknown
		if utf8.Valid(data) {
			return string(data), EncodingUnknown, fmt.Errorf("编码未知，按 utf-8 返回")
		}
		fixed, replaced := sanitizeInvalidUTF8(data)
		if replaced > 0 {
			return string(fixed), EncodingUnknown, fmt.Errorf("编码未知且包含 %d 处非法字节，已替换为 U+FFFD", replaced)
		}
		return string(fixed), EncodingUnknown, fmt.Errorf("编码未知")
	}
}

// decodeUTF16 将 UTF-16 (LE 或 BE，可含 BOM) 字节流解码为 UTF-8。
// 它能处理并替换非法的代理对。
// 返回：(解码后UTF-8字节, 替换次数, 错误)。
func decodeUTF16(data []byte, littleEndian bool) ([]byte, int, error) {
	// 去掉 BOM（如果有）
	if len(data) >= 2 {
		if littleEndian && data[0] == 0xFF && data[1] == 0xFE {
			data = data[2:]
		} else if !littleEndian && data[0] == 0xFE && data[1] == 0xFF {
			data = data[2:]
		}
	}
	// 如果长度为奇数，最后一个字节不足一个单元，忽略
	if len(data)%2 == 1 {
		data = data[:len(data)-1]
	}
	// 预估容量：平均 2 字节一个 rune，UTF-8 可能 1~3 字节；用 len(data)/2*3 足够
	out := make([]byte, 0, (len(data)/2)*3)
	replaced := 0

	for i := 0; i < len(data); i += 2 {
		var u1 uint16
		if littleEndian {
			u1 = uint16(data[i]) | uint16(data[i+1])<<8
		} else {
			u1 = uint16(data[i])<<8 | uint16(data[i+1])
		}

		// 代理对处理
		if u1 >= 0xD800 && u1 <= 0xDBFF { // high surrogate
			if i+3 >= len(data) { // 不足以组成对，替换
				out = utf8.AppendRune(out, utf8.RuneError)
				replaced++
				continue
			}
			var u2 uint16
			if littleEndian {
				u2 = uint16(data[i+2]) | uint16(data[i+3])<<8
			} else {
				u2 = uint16(data[i+2])<<8 | uint16(data[i+3])
			}
			if u2 < 0xDC00 || u2 > 0xDFFF { // 非法低代理
				out = utf8.AppendRune(out, utf8.RuneError)
				replaced++
				continue
			}
			// 合成码点
			cp := 0x10000 + ((uint32(u1) - 0xD800) << 10) + (uint32(u2) - 0xDC00)
			out = utf8.AppendRune(out, rune(cp))
			i += 2 // 多前进一个单元
			continue
		}
		if u1 >= 0xDC00 && u1 <= 0xDFFF { // 孤立低代理
			out = utf8.AppendRune(out, utf8.RuneError)
			replaced++
			continue
		}
		// 普通 BMP 码点
		out = utf8.AppendRune(out, rune(u1))
	}
	return out, replaced, nil
}

// sanitizeInvalidUTF8 检查并修复（若需要）UTF-8 字节流，将所有非法序列替换为 Unicode 替换字符 (U+FFFD)。
// 返回：(修复后的字节流, 替换次数)。
func sanitizeInvalidUTF8(data []byte) ([]byte, int) {
	if utf8.Valid(data) {
		// 快速路径：返回拷贝（保持与其它路径行为一致）
		cp := make([]byte, len(data))
		copy(cp, data)
		return cp, 0
	}
	out := make([]byte, 0, len(data))
	replaced := 0
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 { // 非法前导字节
			out = utf8.AppendRune(out, utf8.RuneError)
			data = data[1:]
			replaced++
			continue
		}
		out = utf8.AppendRune(out, r)
		data = data[size:]
	}
	return out, replaced
}
