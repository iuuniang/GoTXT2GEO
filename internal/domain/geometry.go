/*
Copyright © 2025 TheMachine <592858548@qq.com>
*/
package domain

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Feature 预处理阶段单个要素
type Feature struct {
	WKT        string         `json:"wkt"`
	Attributes map[string]any `json:"attributes"`
}

// PreprocessData 预处理结果集合
type PreprocessData struct {
	CRS      string    `json:"crs"`
	EPSG     int       `json:"epsg,omitempty"`
	Features []Feature `json:"features"`
}

// MaxTolerance 最大允许容差（数字越小精度越高，容差越小精度越高）
const MaxTolerance = 0.0001

// -------- 精度 / 容差工具函数 --------

// normalizePrecision 归一化用户/文件提供的精度；不合法或超出范围时回退到 MaxTolerance。
func normalizePrecision(p float64) float64 {
	if p <= 0 || p > MaxTolerance {
		return MaxTolerance
	}
	return p
}

// parsePrecision 解析精度字符串，返回归一化结果。
func parsePrecision(s string) float64 {
	if s == "" {
		return MaxTolerance
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return normalizePrecision(v)
	}
	return MaxTolerance
}

// decimalPlacesFromPrecision 根据容差求建议小数位（限制 4~6）。
func decimalPlacesFromPrecision(p float64) int {
	p = normalizePrecision(p)
	dec := int(math.Ceil(-math.Log10(p)))
	if dec < 4 {
		dec = 4
	} else if dec > 6 {
		dec = 6
	}
	return dec
}

// precisionToScale 根据容差推导用于离散化的整型比例（至少与 MaxTolerance 对应精度一致）。
func precisionToScale(p float64) float64 {
	p = normalizePrecision(p)
	dec := int(math.Ceil(-math.Log10(p)))
	minDec := int(math.Ceil(-math.Log10(MaxTolerance)))
	if dec < minDec {
		dec = minDec
	}
	return math.Pow10(dec)
}

// 离散网格坐标类型（用于八邻域去重）
type gridKey struct{ x, y int64 }

type GeometryOptions struct {
	Precision   float64 // 容差（<=MaxTolerance）
	Deduplicate bool    // 是否去重（按坐标+容差）
	AutoClose   bool    // 是否自动闭合
}

// BuildGeometryPreprocessData 生成预处理数据：解析精度 -> 几何后处理 -> WKT+属性 -> CRS
func BuildGeometryPreprocessData(parsed *ParsedData, opts GeometryOptions) (*PreprocessData, error) {
	if parsed == nil || len(parsed.Parcels) == 0 {
		return nil, fmt.Errorf("无可用地块数据")
	}

	// 精度优先级：opts.Precision -> 文件属性 "精度" -> 默认 MaxTolerance
	if opts.Precision <= 0 && parsed.FileAttributes != nil {
		opts.Precision = parsePrecision(parsed.FileAttributes["精度"])
	}
	opts.Precision = normalizePrecision(opts.Precision)
	dec := decimalPlacesFromPrecision(opts.Precision)

	postProcessGeometry(parsed, opts)

	coordSystem, err := BuildCoordinateSystem(parsed)
	if err != nil {
		return nil, fmt.Errorf("坐标系构建失败: %w", err)
	}

	features := make([]Feature, 0, len(parsed.Parcels))
	for _, parcel := range parsed.Parcels {
		wkt, err := buildPolygonWKTInternal(parcel, dec)
		if err != nil {
			return nil, fmt.Errorf("地块 %s WKT构建失败: %w", parcel.Attributes[KeyPID], err)
		}
		attrs := mapAttributes(parcel.Attributes)
		features = append(features, Feature{
			WKT:        wkt,
			Attributes: attrs,
		})
	}

	crs := coordSystem.WKT
	epsg := 0
	if coordSystem.EPSG > 0 {
		epsg = coordSystem.EPSG
		crs = fmt.Sprintf("EPSG:%d", coordSystem.EPSG)
	}

	return &PreprocessData{
		CRS:      crs,
		EPSG:     epsg,
		Features: features,
	}, nil
}

// buildPolygonWKTInternal 构建单个地块的WKT
func buildPolygonWKTInternal(parcel Parcel, decimalPlaces int) (string, error) {
	if len(parcel.Rings) == 0 {
		return "", fmt.Errorf("地块 %s 不包含任何环", parcel.Attributes[KeyPID])
	}
	var ringsWKT []string
	for _, ring := range parcel.Rings {
		if len(ring) < 4 {
			return "", fmt.Errorf("地块 %s 的一个环点数少于4，无法构成有效多边形", parcel.Attributes[KeyPID])
		}
		if ring[0].ID != ring[len(ring)-1].ID {
			return "", fmt.Errorf("地块 %s 的一个环不是闭合的", parcel.Attributes[KeyPID])
		}
		ringsWKT = append(ringsWKT, buildRingWKTInternal(ring, decimalPlaces))
	}
	return fmt.Sprintf("POLYGON (%s)", strings.Join(ringsWKT, ", ")), nil
}

// buildRingWKTInternal 构建WKT环
func buildRingWKTInternal(ring []Point, decimalPlaces int) string {
	if len(ring) == 0 {
		return "()"
	}
	var builder strings.Builder
	// 估计预留空间：每个点约包含两个坐标和分隔符
	builder.Grow(len(ring) * (decimalPlaces*2 + 10))
	builder.WriteByte('(')
	for i, p := range ring {
		if i > 0 {
			builder.WriteString(", ")
		}
		y := strconv.FormatFloat(p.Y, 'f', decimalPlaces, 64)
		x := strconv.FormatFloat(p.X, 'f', decimalPlaces, 64)
		builder.WriteString(y)
		builder.WriteByte(' ')
		builder.WriteString(x)
	}
	builder.WriteByte(')')
	return builder.String()
}

// mapAttributes 属性映射
func mapAttributes(attrs map[string]string) map[string]any {
	if attrs == nil {
		return nil
	}
	m := make(map[string]any, len(attrs))
	for k, v := range attrs {
		m[k] = v
	}
	return m
}

// postProcessGeometry 对所有地块环进行高性能去重与自动闭合（包内部方法）。
func postProcessGeometry(data *ParsedData, opts GeometryOptions) {
	prec := normalizePrecision(opts.Precision)
	scale := precisionToScale(prec)
	for pi := range data.Parcels {
		for ri, ring := range data.Parcels[pi].Rings {
			if len(ring) == 0 {
				continue
			}
			data.Parcels[pi].Rings[ri] = processRing(ring, scale, prec, opts.Deduplicate, opts.AutoClose)
		}
	}
}

// processRing 执行单个环的：可选去重 -> 可选自动闭合 -> 排序（保持闭合点最后）。
func processRing(ring []Point, scale, prec float64, dedup, autoClose bool) []Point {
	r := ring
	if dedup {
		r = deduplicateRing(r, scale)
	}
	closed := false
	if autoClose && len(r) > 1 && !pointsEqual(r[0], r[len(r)-1], prec) {
		r = autoCloseRing(r, prec)
		closed = true
	}
	sortLen := len(r)
	if closed {
		sortLen--
	}
	if sortLen > 1 {
		sort.Slice(r[:sortLen], func(i, j int) bool { return r[i].ID < r[j].ID })
	}
	return r
}

// 八邻域去重，坐标离散化后相邻格点均视为重复点
func deduplicateRing(ring []Point, scale float64) []Point {
	if len(ring) == 0 {
		return ring
	}
	seen := make(map[gridKey]struct{}, len(ring))
	result := make([]Point, 0, len(ring))
	// 预计算邻域偏移（含自身）
	neighbor := [...]gridKey{{-1, -1}, {-1, 0}, {-1, 1}, {0, -1}, {0, 0}, {0, 1}, {1, -1}, {1, 0}, {1, 1}}
	for _, pt := range ring {
		gx := int64(math.Round(pt.X * scale))
		gy := int64(math.Round(pt.Y * scale))
		skip := false
		for _, off := range neighbor {
			if _, ok := seen[gridKey{gx + off.x, gy + off.y}]; ok {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		seen[gridKey{gx, gy}] = struct{}{}
		result = append(result, pt)
	}
	return result
}

// 自动闭合环，首尾点不在容差范围内则补首点
func autoCloseRing(ring []Point, tol float64) []Point {
	n := len(ring)
	if n == 0 {
		return ring
	}
	if !pointsEqual(ring[0], ring[n-1], tol) {
		return append(ring, ring[0])
	}
	return ring
}

// pointsEqual 判断两点是否在容差范围内相等
func pointsEqual(a, b Point, tol float64) bool {
	return math.Abs(a.X-b.X) <= tol && math.Abs(a.Y-b.Y) <= tol
}
