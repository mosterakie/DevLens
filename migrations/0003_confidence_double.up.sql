-- confidence 由 REAL 改为 DOUBLE PRECISION。
--
-- REAL 是 4 字节浮点，无法精确表示 0.8 这类十进制小数，
-- 写入 0.8 读出来是 0.800000011920929。Go 侧的字段是 float64，
-- 两边精度不一致会让等值比较、排序和聚合都不可靠。
ALTER TABLE incident_analysis
    ALTER COLUMN confidence TYPE DOUBLE PRECISION;

COMMENT ON COLUMN incident_analysis.confidence IS '置信度 0-1，模型自评';