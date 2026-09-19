-- 记录每条 Incident 的诊断语言。
--
-- 存在 incidents 而不是 incident_analysis：语言是提交时的属性，
-- 需要 worker 在处理时读出来，而那时 analysis 还没写。
ALTER TABLE incidents
    ADD COLUMN lang TEXT NOT NULL DEFAULT 'zh-Hans';

-- 已有记录按默认语言处理。
COMMENT ON COLUMN incidents.lang IS '诊断内容的语言，如 zh-Hans / en';