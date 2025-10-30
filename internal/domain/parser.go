/*
Copyright © 2025 TheMachine <592858548@qq.com>
*/
package domain

import (
	"bufio"
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"
)

// --- 公共数据结构 ---

// Point 表示一个二维平面坐标点 (X,Y)。单位与输入文件一致。
type Point struct {
	// Point 表示一个二维平面坐标点，包含点号、圈号、X、Y。点号和圈号不能混用。
	ID     int     // 点号（唯一标识该点，通常与原始数据点号一致）
	RingID int     // 圈号（标识该点所属的环）
	X      float64 // X坐标
	Y      float64 // Y坐标
}

// Ring 表示一个多边形环（首尾相连的点序列），每个点有独立点号和圈号。最后一个点与第一个点可相同以显式闭合。
type Ring []Point

// Parcel 表示一个地块实体，包含其属性字段与几何（一个或多个环）。
type Parcel struct {
	Attributes map[string]string
	Rings      []Ring
}

// ParsedData 是 ParseGeoContent 返回的完整结构化结果。
type ParsedData struct {
	// Parcels 解析出的地块集合。
	Parcels []Parcel
	// FileAttributes 文件级属性键值对（来自 [属性描述] 部分）。
	// 至少包含: "坐标系", "投影类型", "几度分带", "带号", "精度"（若源文件提供）。
	FileAttributes map[string]string
}

// --- 解析器实现 ---

const (
	secAttr = "[属性描述]"
	secGeom = "[地块坐标]"
)

// Parcel attribute short keys (<=10 chars)
const (
	KeyBPCnt = "bp_cnt" // 界址点数
	KeyArea  = "area"   // 地块面积
	KeyPID   = "pid"    // 地块编号
	KeyPName = "pname"  // 地块名称
	KeyGType = "gtype"  // 记录图形属性(点/线/面)
	KeySheet = "sheet"  // 图幅号
	KeyUsage = "usage"  // 地块用途
	KeyCode  = "code"   // 地块编码
)

// 统一的地块属性键顺序（用于解析 header 行）；使用数组可避免每次分配新切片。
var parcelAttrKeys = [...]string{KeyBPCnt, KeyArea, KeyPID, KeyPName, KeyGType, KeySheet, KeyUsage, KeyCode}

// 统一错误 / 诊断代码常量，便于调用方做分类处理或统计。

const (
	CodeMissingParcelHeader = "MISSING_PARCEL_HEADER"
	CodeInvalidPointFormat  = "INVALID_POINT_FORMAT"
)

type parseState int

const (
	stateInitial     parseState = iota // 初始状态，寻找 [属性描述]
	stateAttributes                    // 正在解析属性
	stateCoordinates                   // 正在解析坐标
)

// parseContext 用于在解析过程中维护状态
type parseContext struct {
	state         parseState
	lineNo        int
	attrs         map[string]string
	parcels       []Parcel
	currentParcel *Parcel
	ringPoints    map[int][]Point // 临时存储当前地块的环点，key是圈号
	ringFirstLine map[int]int     // 记录每个环首个坐标出现的行号
}

// Parse 解析原始文本为结构化地块数据（语法层面）。
// 解析原则：
//  1. 坐标行格式（字段数、数值可解析性）一旦出错立即返回错误（精确到行号）。
//  2. 仅负责把同一地块（以以 @ 结尾的起始行标识）下按“圈号”分组的点序列收集为 Ring；不做任何几何质量修正：
//     - 不去重；不自动闭合；不验证最少点数；不剔除未闭合 / 点数不足的环。
//  3. 文件级必填属性缺失在全部扫描结束后统一校验并一次性返回。
//  4. 返回的每个 Parcel.Rings 中的 Ring 即源文件的原始顺序点列表（可能：未闭合 / 包含重复点 / 点数过少 / 存在多余噪声环）。
//  5. 几何合法性验证、去重及自动闭合请在后处理中调用 PostProcessGeometry / ValidateGeometry。
//
// 成功返回时（error == nil）：语法有效；属性完整；几何仍为“原始形态”。
func Parse(content string) (*ParsedData, error) {
	ctx := &parseContext{
		state:         stateInitial,
		attrs:         make(map[string]string),
		ringPoints:    make(map[int][]Point),
		ringFirstLine: make(map[int]int),
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		ctx.lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if err := ctx.processLine(line); err != nil {
			return nil, fmt.Errorf("line %d: %w", ctx.lineNo, err)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading content failed: %w", err)
	}

	// 文件结束时，处理最后一个地块
	if err := ctx.finalizeCurrentParcel(); err != nil {
		return nil, err
	}

	// 检查并报告缺失的关键部分
	switch ctx.state {
	case stateInitial:
		return nil, fmt.Errorf("文件缺少 %s 部分", secAttr)
	case stateAttributes:
		return nil, fmt.Errorf("文件缺少 %s 部分", secGeom)
	}

	// 验证必需的文件属性（键名在属性阶段已即时规范化）
	if err := validateFileAttributes(ctx.attrs); err != nil {
		return nil, err
	}

	// 复制文件级属性（防止调用方修改内部原 map）
	copied := make(map[string]string, len(ctx.attrs))

	maps.Copy(copied, ctx.attrs)
	return &ParsedData{
		Parcels:        ctx.parcels,
		FileAttributes: copied,
	}, nil
}

// processLine 根据当前解析状态处理单行文本。
// 非导出：实现状态机主逻辑。
func (c *parseContext) processLine(line string) error {
	switch c.state {
	case stateInitial:
		if line == secAttr {
			c.state = stateAttributes
		}
		// 在找到[属性描述]之前忽略所有其他行
	case stateAttributes:
		if line == secGeom {
			c.state = stateCoordinates
			return nil
		}
		// 如果再次遇到 [属性描述] 说明是重复的文件头，按照新需求：忽略其内容，不再重置 attrs。
		if line == secAttr { // 再次出现，停留在 attributes 状态但不做任何处理
			return nil
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			val = FullWidthStrToHalfWidthStr(val) // 全角转半角

			// 即时纠正：将键名中的“产生”替换为“生产”；若规范键已存在则忽略误写版本
			canonical := strings.ReplaceAll(key, "产生", "生产")
			if canonical != key {
				if _, exists := c.attrs[canonical]; exists {
					// 已有规范键，忽略此次误写
					return nil
				}
				key = canonical
			}
			c.attrs[key] = val
		}
	case stateCoordinates:
		// 新需求：后续再次出现 [属性描述] / [地块坐标] 均忽略（不再解析新的文件属性也不改变现有状态）
		if line == secAttr || line == secGeom {
			return nil
		}
		// 逻辑：坐标行必须含逗号；重复属性行一般是 key=value 且不含逗号
		if strings.Contains(line, "=") && !strings.Contains(line, ",") {
			return nil
		}
		if strings.HasSuffix(line, ",@") {
			// 这是一个新的地块属性行，严格模式下若上一个地块存在错误直接返回
			if err := c.finalizeCurrentParcel(); err != nil {
				return err
			}
			c.startNewParcel(line)
		} else {
			// 尝试解析为坐标点
			if err := c.addPointToCurrentParcel(line); err != nil {
				// 严格模式：直接返回错误终止
				return fmt.Errorf("line %d: %w", c.lineNo, err)
			}
		}
	}
	return nil
}

// finalizeCurrentParcel 将暂存的 ringPoints 按圈号排序转换为 Ring 并附加到当前地块：
//   - 不做任何几何修补（不去重/不闭合/不判定合法性）；
//   - 空点集的圈号跳过；
//   - 即使生成的环潜在无效也照样保留，交由后处理阶段决策；
//   - 完成后把地块写入结果并重置缓存。
func (c *parseContext) finalizeCurrentParcel() error {
	if c.currentParcel == nil || len(c.ringPoints) == 0 {
		return nil
	}

	ids := make([]int, 0, len(c.ringPoints))
	for id := range c.ringPoints {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	// 直接按圈号顺序附加原始点序列（不做任何几何修正）
	for _, id := range ids {
		pts := c.ringPoints[id]
		if len(pts) == 0 { // 空 ring 忽略
			continue
		}
		c.currentParcel.Rings = append(c.currentParcel.Rings, Ring(pts))
	}

	// 即使某些 ring 不满足最小点数或未闭合，也先保留，由后处理决定取舍
	c.parcels = append(c.parcels, *c.currentParcel)
	c.currentParcel = nil
	c.ringPoints = make(map[int][]Point)
	c.ringFirstLine = make(map[int]int)
	return nil
}

// startNewParcel 初始化一个新地块并重置环缓存。
func (c *parseContext) startNewParcel(line string) {
	attrs := parseParcelAttributes(line)
	p := &Parcel{Attributes: attrs, Rings: []Ring{}}
	c.currentParcel = p
	c.ringPoints = make(map[int][]Point)
	c.ringFirstLine = make(map[int]int)
}

// addPointToCurrentParcel 解析一条坐标记录并加入当前地块环缓存。
// 期望格式: 点号,ringID,x,y,... 至少 4 个逗号分隔字段。
// 错误：圈号或坐标无法解析时返回格式错误。
func (c *parseContext) addPointToCurrentParcel(line string) error {
	if c.currentParcel == nil {
		// 严格模式：直接返回错误
		return fmt.Errorf("%s: 在[地块坐标]部分发现坐标点，但之前缺少以@结尾的地块起始行", CodeMissingParcelHeader)
	}

	parts := strings.Split(line, ",")
	if len(parts) < 4 {
		return fmt.Errorf("%s: 坐标行格式错误，字段不足", CodeInvalidPointFormat)
	}
	// 点号支持任意前缀，提取数字部分，圈号为环分组依据，点号和圈号不能混用
	pointID := extractFirstInt(parts[0])
	ringID, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return fmt.Errorf("%s: 无效的圈号: %s", CodeInvalidPointFormat, parts[1])
	}
	x, err := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
	if err != nil {
		return fmt.Errorf("%s: 无效的X坐标: %s", CodeInvalidPointFormat, parts[2])
	}
	y, err := strconv.ParseFloat(strings.TrimSpace(parts[3]), 64)
	if err != nil {
		return fmt.Errorf("%s: 无效的Y坐标: %s", CodeInvalidPointFormat, parts[3])
	}
	if c.ringPoints[ringID] == nil {
		c.ringPoints[ringID] = make([]Point, 0)
	}
	c.ringPoints[ringID] = append(c.ringPoints[ringID], Point{ID: pointID, RingID: ringID, X: x, Y: y})
	return nil
}

// --- 辅助函数 ---
// extractFirstInt 提取字符串中的第一个连续数字，未找到返回0
func extractFirstInt(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			num := ""
			for j := i; j < len(s) && s[j] >= '0' && s[j] <= '9'; j++ {
				num += string(s[j])
			}
			id, _ := strconv.Atoi(num)
			return id
		}
	}
	return 0
}

// validateFileAttributes 校验文件级必选属性是否存在。
// 若缺少返回错误列出全部缺失项。
func validateFileAttributes(attrs map[string]string) error {
	// 必填属性键（中文名）
	required := []string{"坐标系", "投影类型", "几度分带", "带号"}
	missing := []string{}

	for _, key := range required {
		if _, ok := attrs[key]; !ok {
			missing = append(missing, key)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("[属性描述]部分缺少必选参数: %s", strings.Join(missing, ", "))
	}

	return nil
}

// parseParcelAttributes 解析以 "...,@" 结尾的地块起始行
func parseParcelAttributes(line string) map[string]string {
	// 直接预分配完整容量，避免 map 扩容
	attrs := make(map[string]string, len(parcelAttrKeys))
	core := strings.TrimSpace(strings.TrimSuffix(line, ",@"))

	if core == "" { // 全部为空，填充所有键为 ""
		for _, k := range parcelAttrKeys {
			attrs[k] = ""
		}
		return attrs
	}

	parts := strings.Split(core, ",")
	// 顺序: bp_cnt,area,pid,pname,gtype,sheet,usage,code
	for i, k := range parcelAttrKeys {
		if i < len(parts) {
			// Trim 每个字段；允许结果为空串
			v := strings.TrimSpace(parts[i])
			attrs[k] = v
		} else {
			attrs[k] = "" // 补齐缺失字段
		}
	}
	return attrs
}

// 高性能全角转半角字符串（支持常见全角标点和英文符号）
func FullWidthStrToHalfWidthStr(str string) string {
	var builder strings.Builder
	for _, r := range str {
		// 全角空格
		if r == 12288 {
			builder.WriteRune(32)
			continue
		}
		// 全角英文、数字、常用符号
		if r >= 65281 && r <= 65374 {
			builder.WriteRune(r - 65248)
			continue
		}
		// 常见中文全角标点
		switch r {
		case '．':
			builder.WriteRune('.')
		case '，':
			builder.WriteRune(',')
		case '（':
			builder.WriteRune('(')
		case '）':
			builder.WriteRune(')')
		case '：':
			builder.WriteRune(':')
		case '；':
			builder.WriteRune(';')
		case '！':
			builder.WriteRune('!')
		case '？':
			builder.WriteRune('?')
		case '“':
			builder.WriteRune('"')
		case '”':
			builder.WriteRune('"')
		case '‘':
			builder.WriteRune('\'')
		case '’':
			builder.WriteRune('\'')
		default:
			builder.WriteRune(r)
		}
	}
	return builder.String()
}
