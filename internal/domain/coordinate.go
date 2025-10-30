/*
Copyright © 2025 TheMachine <592858548@qq.com>
*/
package domain

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// CoordinateSystem 汇总由属性与几何推导出的坐标系统信息。
// 用于描述 CGCS2000 高斯-克吕格投影参数，包括分带、带号、中央经线、EPSG 码及 WKT。
type CoordinateSystem struct {
	Name             string  // 投影坐标系名称（ESRI WKT 中的 PROJCS 名称）
	Degree           int     // 几度分带（3 或 6）
	Band             int     // 带号
	CentralMeridian  float64 // 中央经线（单位：度）
	EPSG             int     // EPSG 代码；若为 0 表示不存在标准 EPSG
	IsCustomMeridian bool    // 是否来源于 "坐标系" 字段自定义的中央经线
	WKT              string  // ESRI Well Known Text 描述
}

// BuildCoordinateSystem 根据解析结果构建 CGCS2000 高斯-克吕格投影定义。
// 规则：
//  1. 坐标系字段必须包含 "2000国家大地坐标系"，括号内数字表示自定义中央经线。
//  2. 仅支持 3 度或 6 度分带，3 度带号范围 [25,45]，6 度带号范围 [13,23]。
//  3. 标准中央经线输出 EPSG 码和 WKT，自定义中央经线仅输出 WKT。
//  4. 若属性分带/带号与几何推断不一致，优先采用几何。
//
// 参数：pd 解析后的地块数据
// 返回：坐标系统结构体或错误
func BuildCoordinateSystem(pd *ParsedData) (*CoordinateSystem, error) {
	if pd == nil {
		return nil, fmt.Errorf("parsed data is nil")
	}
	if len(pd.Parcels) == 0 {
		return nil, fmt.Errorf("parsed data contains no parcels")
	}
	attrs := pd.FileAttributes
	if attrs == nil {
		return nil, fmt.Errorf("file attributes missing")
	}

	coordName := strings.TrimSpace(attrs["坐标系"])
	if coordName == "" {
		return nil, fmt.Errorf("缺少坐标系字段")
	}
	if !strings.Contains(coordName, "2000国家大地坐标系") {
		return nil, fmt.Errorf("坐标系必须为\"2000国家大地坐标系\"")
	}

	// 1. 先用属性分带和带号
	degreeAttr, err := strconv.Atoi(strings.TrimSpace(attrs["几度分带"]))
	if err != nil {
		return nil, fmt.Errorf("几度分带无效: %v", err)
	}
	if degreeAttr != 3 && degreeAttr != 6 {
		return nil, fmt.Errorf("几度分带必须为 3 或 6")
	}
	bandAttr, err := strconv.Atoi(strings.TrimSpace(attrs["带号"]))
	if err != nil {
		return nil, fmt.Errorf("带号无效: %v", err)
	}

	// 2. 再用几何样本点推断带号
	bandGeom := deriveBandFromFirstPoint(pd)
	degreeGeom := normalizeDegreeForBand(degreeAttr, bandGeom)

	// 3. 判断是否有自定义中央经线
	customCM, hasCustom := extractCustomCentralMeridian(coordName)

	var degree, band int

	if bandGeom > 0 && bandGeom != bandAttr {
		// 实际坐标能推断带号且与属性不一致，以实际为准
		degree = degreeGeom
		band = bandGeom

	} else {
		// 否则用文件属性定义
		degree = degreeAttr
		band = bandAttr
	}

	// 最终校验分带与带号是否匹配
	degree = normalizeDegreeForBand(degree, band)

	if degree == 0 {
		switch degreeAttr {
		case 3:
			return nil, fmt.Errorf("3度带带号必须在[25,45]范围内，当前带号：%d", band)
		case 6:
			return nil, fmt.Errorf("6度带带号必须在[13,23]范围内，当前带号：%d", band)
		default:
			return nil, fmt.Errorf("带号 %d 与分带配置不匹配", band)
		}
	}

	var central float64
	if hasCustom {
		central = customCM
	} else {
		var errCM error
		central, errCM = computeStandardCentral(degree, band)
		if errCM != nil {
			return nil, errCM
		}
	}

	if central < 75 || central > 135 {
		return nil, fmt.Errorf("中央经线 %.6f 超出中国区间 [75,135]", central)
	}

	// 计算 EPSG 代码，判断中央经线是否为标准（能被3整除，允许浮点误差）
	isStandardCentral := math.Abs(math.Mod(central, 3)) < 1e-8
	var epsg int
	hasBand := bandGeom > 0
	if isStandardCentral {
		epsg = computeEPSGCode(band, hasBand)
	} else {
		epsg = 0
	}

	projName := buildProjectionName(band, central, hasBand, isStandardCentral)
	wkt := buildCGCS2000WKT(projName, central, band, hasBand)

	return &CoordinateSystem{
		Name:             projName,
		Degree:           degree,
		Band:             band,
		CentralMeridian:  central,
		EPSG:             epsg,
		IsCustomMeridian: hasCustom,
		WKT:              wkt,
	}, nil
}

// deriveBandFromFirstPoint 从几何首个点推断带号（取 Y 坐标的百万位）。
// 若无有效点则返回 0。
func deriveBandFromFirstPoint(pd *ParsedData) int {
	// 只取第一个 parcel 的第一个 ring 的第一个有效点
	if pd == nil || len(pd.Parcels) == 0 {
		return 0
	}
	parcel := pd.Parcels[0]
	if len(parcel.Rings) > 0 {
		for _, pt := range parcel.Rings[0] {
			if pt.Y != 0 {
				candidate := int(math.Floor(pt.Y / 1_000_000))
				if candidate > 0 {
					return candidate
				}
			}
		}
	}
	return 0
}

// normalizeDegreeForBand 校验分带与带号是否匹配。
// 3度带号范围 [25,45]，6度带号范围 [13,23]。
func normalizeDegreeForBand(requestDegree, band int) int {
	// 3度带范围是25~45，6度带范围是13~23
	if requestDegree == 3 && band >= 25 && band <= 45 {
		return 3
	}
	if requestDegree == 6 && band >= 13 && band <= 23 {
		return 6
	}
	return 0
}

// extractCustomCentralMeridian 提取自定义中央经线（括号内数字）。
// 仅支持半角括号，已在解析环节做全角转半角。
func extractCustomCentralMeridian(name string) (float64, bool) {
	start := strings.LastIndex(name, "(")
	end := strings.LastIndex(name, ")")
	if start == -1 || end <= start+1 {
		return 0, false
	}
	raw := name[start+1 : end]
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}

	var builder strings.Builder
	for _, r := range raw {
		if (r >= '0' && r <= '9') || r == '+' || r == '-' || r == '.' {
			builder.WriteRune(r)
		}
	}
	if builder.Len() == 0 {
		return 0, false
	}
	val, err := strconv.ParseFloat(builder.String(), 64)
	if err != nil {
		return 0, false
	}
	return val, true
}

// computeStandardCentral 计算标准中央经线。
// 3度带：central = band * 3；6度带：central = band * 6 - 3。
// 输入参数需已校验。
func computeStandardCentral(degree, band int) (float64, error) {
	if degree == 3 {
		if band < 25 || band > 45 {
			return 0, fmt.Errorf("3 度带带号必须在 [25,45] 范围内")
		}
		return float64(band) * 3.0, nil
	}
	if degree == 6 {
		if band < 13 || band > 23 {
			return 0, fmt.Errorf("6 度带带号必须在 [13,23] 范围内")
		}
		return float64(band)*6.0 - 3.0, nil
	}
	return 0, fmt.Errorf("仅支持 3 度或 6 度分带")
}

// computeEPSGCode 根据带号和分带类型推断 EPSG 代码。
// 3度带号范围 [25,45]，6度带号范围 [13,23]。
// hasBand 表示是否有几何推断带号。
func computeEPSGCode(band int, hasBand bool) int {
	if band >= 13 && band <= 23 {
		if hasBand {
			return 4491 + (band - 13)
		}
		return 4502 + (band - 13)
	}
	if band >= 25 && band <= 45 {
		if hasBand {
			return 4513 + (band - 25)
		}
		return 4534 + (band - 25)
	}
	return 0 // 带号超出有效范围
}

// buildProjectionName 构造投影名称。
// 标准中央经线用整数，非标准用一位小数。
// hasBand 控制 Zone/CM 命名。
func buildProjectionName(band int, central float64, hasBand bool, isStandardCentral bool) string {
	var prefix string
	// 带号区分前缀
	if band >= 13 && band <= 23 {
		prefix = "CGCS2000_GK_"
	}
	if band >= 25 && band <= 45 {
		prefix = "CGCS2000_3_Degree_GK_"
	}
	// 中央经线显示格式
	var cmStr string
	if isStandardCentral {
		cmStr = fmt.Sprintf("%d", int(central))
	} else {
		cmStr = fmt.Sprintf("%.1f", central)
	}
	if hasBand {
		return fmt.Sprintf("%sZone_%d", prefix, band)
	}
	return fmt.Sprintf("%sCM_%sE", prefix, cmStr)
}

// buildCGCS2000WKT 构造 CGCS2000 高斯-克吕格投影 WKT。
// hasBand 控制 False_Easting。
func buildCGCS2000WKT(name string, central float64, band int, hasBand bool) string {
	var falseEasting float64
	if hasBand {
		falseEasting = float64(band)*1_000_000 + 500000
	} else {
		falseEasting = 500000.0
	}
	wkt := `PROJCS["%s",` +
		`GEOGCS["GCS_China_Geodetic_Coordinate_System_2000",` +
		`DATUM["D_China_2000",SPHEROID["CGCS2000",6378137.0,298.257222101]],` +
		`PRIMEM["Greenwich",0.0],` +
		`UNIT["Degree",0.0174532925199433]],` +
		`PROJECTION["Gauss_Kruger"],` +
		`PARAMETER["False_Easting",%.1f],` +
		`PARAMETER["False_Northing",0.0],` +
		`PARAMETER["Central_Meridian",%.1f],` +
		`PARAMETER["Scale_Factor",1.0],` +
		`PARAMETER["Latitude_Of_Origin",0.0],` +
		`UNIT["Meter",1.0]]`
	return fmt.Sprintf(wkt, name, falseEasting, central)
}
