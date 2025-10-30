"""
高性能地理数据导出脚本
"""
# Copyright © 2025 TheMachine <592858548@qq.com>

# pylint: disable=W0611,E0611,W0718,C0301
from __future__ import annotations

import sys
import json
import logging
import time
from dataclasses import dataclass
from pathlib import Path

from qgis.core import (
    QgsApplication,
    QgsCoordinateReferenceSystem,
    QgsCoordinateTransform,
    QgsCoordinateTransformContext,
    QgsFeature,
    QgsField,
    QgsFields,
    QgsGeometry,
    QgsProject,
    QgsVectorFileWriter,
    QgsWkbTypes,
)
from qgis.PyQt.QtCore import QMetaType

# --- 配置和常量 ---

# 配置日志记录到 stderr，使用更独特的分隔符
LOG_SEPARATOR = " - "
# 配置日志记录到 stderr
logging.basicConfig(
    level=logging.INFO,
    stream=sys.stderr,
    format=f"%(levelname)s{LOG_SEPARATOR}%(message)s",
)


@dataclass
class FieldDef:
    """字段定义数据类"""

    name: str
    type: QMetaType.Type
    alias: str
    comment: str
    length: int = 0
    precision: int = 0

    def to_qgs_field(self) -> QgsField:
        """将字段定义转换为 QgsField 对象"""
        field = QgsField(
            self.name, self.type, "", self.length, self.precision, self.comment
        )
        if self.alias:
            field.setAlias(self.alias)
        return field


# 字段定义保持为模块级常量，因为它们是静态的
FIELD_DEFINITIONS: list[FieldDef] = [
    FieldDef("JZD", QMetaType.Type.Int, "界址点数", "几何节点数量"),
    FieldDef(
        "AREA", QMetaType.Type.Double, "地块面积", "平方米", length=32, precision=6
    ),
    FieldDef("DKBH", QMetaType.Type.QString, "地块编号", "地块编号", length=32),
    FieldDef("DKMC", QMetaType.Type.QString, "地块名称", "地块名称", length=64),
    FieldDef("TXSX", QMetaType.Type.QString, "图形属性", "点线面类型", length=16),
    FieldDef("TFH", QMetaType.Type.QString, "图幅号", "图幅号", length=32),
    FieldDef("DKYT", QMetaType.Type.QString, "地块用途", "用途", length=64),
    FieldDef("DLBM", QMetaType.Type.QString, "地类编码", "分类编码", length=32),
    FieldDef("WJLJ", QMetaType.Type.QString, "文件路径", "原始文件路径", length=254),
]


# 字段映射
FIELD_MAPPING: dict[str, list[str]] = {
    "JZD": ["bp_cnt"],
    "AREA": ["area"],
    "DKBH": ["pid"],
    "DKMC": ["pname"],
    "TXSX": ["gtype"],
    "TFH": ["sheet"],
    "DKYT": ["usage"],
    "DLBM": ["code"],
    "WJLJ": ["source_path"],
}
# region --- 数据模型 ---
# 使用 dataclasses 将输入的 JSON 结构化为 Python 对象
# 不可轻易修改的数据结构
@dataclass
class Feature:
    """代表一个地理要素"""

    properties: dict
    wkt: str


@dataclass
class Dataset:
    """代表一个要处理的数据集"""

    features: list[Feature]
    hash: str
    layer_name: str
    source_crs: str
    source_path: str
    total_features: int


@dataclass
class ExportPayload:
    """代表从 Go 接收到的完整任务负载"""

    datasets: list[Dataset]
    driver: str
    merge: bool
    output_dir: str
    overwrite: bool
    target_crs: str

    @classmethod
    def from_dict(cls, data: dict) -> ExportPayload:
        """从字典递归创建 ExportPayload 对象"""
        datasets = [
            Dataset(
                features=[Feature(**f) for f in ds.get("features", [])],
                **{k: v for k, v in ds.items() if k != "features"},
            )
            for ds in data.get("datasets", [])
        ]
        return cls(
            datasets=datasets, **{k: v for k, v in data.items() if k != "datasets"}
        )
# endregion

class GeoProcessor:
    """
    封装了地理数据处理的核心逻辑。
    通过在初始化时缓存重用对象（如字段定义）来优化性能。
    """

    def __init__(self, payload: ExportPayload):
        """
        初始化处理器，接收一个 ExportPayload 对象作为任务配置。
        """
        self.payload = payload
        self.fields: QgsFields = self._build_fields()
        self.crs_cache: dict[str, QgsCoordinateReferenceSystem] = {}
        self.transform_cache: dict[tuple, QgsCoordinateTransform] = {}
        self.default_crs: QgsCoordinateReferenceSystem = self._build_crs("EPSG:4526")
        self.current_dataset: Dataset | None = None
        logging.info("GeoProcessor 初始化完成，任务负载已加载。")

    @staticmethod
    def _build_fields() -> QgsFields:
        """根据 FIELD_DEFINITIONS 构建 QgsFields 对象"""
        fields = QgsFields()
        for f_def in FIELD_DEFINITIONS:
            fields.append(f_def.to_qgs_field())
        return fields

    def _extract_attributes(self, props: dict) -> list[any]:
        """
        根据 FIELD_DEFINITIONS 从记录中提取并正确排序属性。
        这是一个静态方法，因为它不依赖于任何实例状态。
        """
        if not self.current_dataset:
            raise RuntimeError(
                "在调用 _extract_attributes 之前必须设置 current_dataset"
            )

        attributes = []
        source_path = self.current_dataset.source_path

        for field_def in FIELD_DEFINITIONS:
            target_field_name = field_def.name
            found_value = None

            if target_field_name == "WJLJ":
                found_value = source_path
            else:
                possible_source_keys = FIELD_MAPPING.get(target_field_name, [])
                for source_key in possible_source_keys:
                    if source_key in props:
                        found_value = props.get(source_key)
                        break
            attributes.append(found_value)

        return attributes

    def _build_crs(self, def_crs: str) -> QgsCoordinateReferenceSystem:
        """根据给定的 CRS 定义字符串创建 QgsCoordinateReferenceSystem 对象"""
        if not def_crs:
            logging.warning("收到了空的 CRS 定义，将使用默认 CRS。")
            return self.default_crs

        # 检查缓存
        if def_crs in self.crs_cache:
            return self.crs_cache[def_crs]

        # 创建新对象并存入缓存
        qgs_crs = QgsCoordinateReferenceSystem(def_crs)
        if not qgs_crs.isValid():
            logging.error("无效的 CRS 定义: %s，将使用默认 CRS。", def_crs)
            return self.default_crs

        self.crs_cache[def_crs] = qgs_crs
        return qgs_crs

    def _build_transform(
        self,
        src_crs: QgsCoordinateReferenceSystem,
        dest_crs: QgsCoordinateReferenceSystem,
    ) -> QgsCoordinateTransform | None:
        """根据源和目标 CRS 创建 QgsCoordinateTransform 对象"""

        # 使用 authid 或 WKT 作为可哈希的键
        key = (
            src_crs.authid() or src_crs.toWkt(),
            dest_crs.authid() or dest_crs.toWkt(),
        )

        if key in self.transform_cache:
            return self.transform_cache[key]

        if src_crs == dest_crs:
            return None
        transform_context = QgsProject.instance().transformContext()
        transform = QgsCoordinateTransform(src_crs, dest_crs, transform_context)
        self.transform_cache[key] = transform
        return transform

    def _create_feature(
        self, feature_data: Feature, transform: QgsCoordinateTransform | None
    ) -> QgsFeature | None:
        """
        根据单个 Feature 数据类创建 QgsFeature。
        """
        try:
            feature = QgsFeature(self.fields)

            # 1. 提取并设置属性
            attributes = self._extract_attributes(feature_data.properties)
            feature.setAttributes(attributes)

            # 2. 创建并设置几何
            if feature_data.wkt:
                geom = QgsGeometry.fromWkt(feature_data.wkt)
                if not geom.isEmpty():
                    if transform:
                        geom.transform(transform)
                    feature.setGeometry(geom)

            return feature
        except Exception as e:
            logging.error("创建要素失败: %s. 记录: %s", e, feature_data.properties)
            return None

    def _prepare_save_options(self) -> tuple[QgsVectorFileWriter.SaveVectorOptions, str, str]:
        """
        根据当前数据集和全局配置，准备 QgsVectorFileWriter 的保存选项。
        返回: (保存选项, 目标文件路径, 用于日志的显示路径)
        """
        driver_name = self.payload.driver
        layer_name = self.current_dataset.layer_name
        output_dir = Path(self.payload.output_dir)
        overwrite = self.payload.overwrite

        # 确定是容器格式 (如 GPKG) 还是单文件格式 (如 Shapefile)
        is_container = driver_name.upper() in {"GPKG", "OPENFILEGDB"}

        if is_container:
            target_path = output_dir
            display_path = f"{target_path.as_posix()}|layername={layer_name}"
        else:
            target_path = output_dir / layer_name
            display_path = target_path.as_posix()

        # 确定文件存在时的操作
        action = QgsVectorFileWriter.CreateOrOverwriteFile
        if target_path.exists() and overwrite:
            action = QgsVectorFileWriter.CreateOrOverwriteLayer if is_container else QgsVectorFileWriter.CreateOrOverwriteFile
        
        # 创建并配置保存选项
        save_opts = QgsVectorFileWriter.SaveVectorOptions()
        save_opts.driverName = driver_name
        save_opts.layerName = layer_name
        save_opts.fileEncoding = "UTF-8"
        save_opts.actionOnExistingFile = action
        if driver_name.upper() != "OPENFILEGDB":
            save_opts.layerOptions = ["SPATIAL_INDEX=YES"]

        return save_opts, target_path.as_posix(), display_path

    def _write_features(self, features: list[QgsFeature], dest_crs: QgsCoordinateReferenceSystem)-> bool:
        """
        将要素列表写入矢量文件。
        """
        save_opts, target_path_str, display_path = self._prepare_save_options()
        transform_context = QgsProject.instance().transformContext()

        writer = QgsVectorFileWriter.create(
            target_path_str,
            self.fields,
            QgsWkbTypes.Polygon,
            dest_crs,
            transform_context,
            save_opts,
        )
        if writer.hasError() != QgsVectorFileWriter.NoError:
            logging.error("创建矢量文件写入器失败: %s", writer.errorMessage())
            return False

        if features and not writer.addFeatures(features):
            logging.error("向图层 '%s' 添加要素失败", self.current_dataset.layer_name)
            del writer # 确保在失败时也释放写入器
            return False
        
        del writer
        logging.info(
            "成功写入 %d 条要素 -> '%s'", len(features), display_path
        )
        return True
    
    def run_export(self) -> None:
        """
        执行整个导出流程，处理 payload 中的所有数据集。
        """
        logging.info("开始处理 %d 个文件...", len(self.payload.datasets))
        dest_crs = self._build_crs(self.payload.target_crs)

        for dataset in self.payload.datasets:
            self.current_dataset = dataset  # 在处理前设置当前数据集
            start_time = time.perf_counter()
            try:
                logging.info("正在处理文件: %s", dataset.source_path)

                # 1. 准备坐标转换
                src_crs = self._build_crs(dataset.source_crs)
                transform = self._build_transform(src_crs, dest_crs)

                # 2. 创建所有 QGIS 要素
                qgs_features = [
                    qgs_feature
                    for feature_data in dataset.features
                    if (qgs_feature := self._create_feature(feature_data, transform)) is not None
                ]

                # 3. 将要素写入文件
                success = self._write_features(qgs_features, dest_crs)

                duration = (time.perf_counter() - start_time) * 1000
                logging.info("文件 %s 处理耗时 %.2f ms", dataset.source_path, duration)

                # 4. 流式输出处理结果
                status = "processed" if success else "failed"
                result = {"hash": dataset.hash, "status": status}
                print(json.dumps(result, ensure_ascii=False))

            except Exception as e:
                logging.error("处理文件 %s 时发生意外错误: %s", dataset.source_path, e)
                error_result = {"hash": dataset.hash, "status": "failed", "error": str(e)}
                print(json.dumps(error_result, ensure_ascii=False))
            finally:
                self.current_dataset = None
def main():
    """
    主函数：从 stdin 读取、处理数据、向 stdout 实时流式写入结果。
    """
    # 1. 初始化 QGIS 应用程序
    try:
        # 从命令行参数获取 QGIS 安装前缀路径
        prefix_path = sys.argv[1]
    except IndexError:
        logging.error("错误: 脚本启动时未提供 QGIS prefix path 作为命令行参数。")
        sys.exit(1)

    qgs = QgsApplication([], False)
    QgsApplication.setPrefixPath(prefix_path, True)
    qgs.initQgis()
    logging.info("QGIS 环境初始化成功。")

    try:
        # 1. 从标准输入读取所有数据
        input_data = sys.stdin.read()

        # 如果没有输入，则直接退出
        if not input_data:
            logging.warning("标准输入为空，没有数据需要处理。")
            sys.exit(0)

        # 2. 将输入数据解析为 JSON
        raw_payload = json.loads(input_data)
        payload = ExportPayload.from_dict(raw_payload)
        # 3. 创建处理器实例
        processor = GeoProcessor(payload)
        # 4. 执行导出
        processor.run_export()

    except json.JSONDecodeError as e:
        logging.error("JSON 解析失败: %s. 输入: '%s...'", e, input_data[:200])
        sys.exit(1)
    except Exception as e:
        logging.error("脚本执行期间发生未知错误: %s", e)
        sys.exit(1)
    finally:
        # 3. 确保 QGIS 环境总是被清理
        qgs.exitQgis()
        logging.info("QGIS 环境已清理。")

if __name__ == "__main__":
    main()

