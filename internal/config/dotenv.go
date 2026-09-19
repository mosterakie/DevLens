package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// LoadDotEnv 读取 .env 文件并把其中的键值对设置为环境变量。
//
// 已存在的环境变量优先，不会被文件覆盖。这样 CI 或生产环境里
// 真正导出的变量总是生效，仓库里的 .env 只作为本地的默认值。
//
// 文件不存在不算错误：这是正常情况，配置全部来自真实环境变量。
func LoadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// 允许较长的值：PEM 之类的配置可能很长。
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := splitAssignment(line)
		if !ok {
			return fmt.Errorf("%s:%d: expected KEY=VALUE", path, lineNo)
		}
		if key == "" {
			return fmt.Errorf("%s:%d: empty key", path, lineNo)
		}

		// 环境里已经有就不覆盖。
		if _, exists := os.LookupEnv(key); exists {
			continue
		}

		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("%s:%d: set %s: %w", path, lineNo, key, err)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}

// splitAssignment 按第一个 = 拆开，并去掉值的包裹引号和行尾注释。
func splitAssignment(line string) (key, value string, ok bool) {
	i := strings.IndexByte(line, '=')
	if i < 0 {
		return "", "", false
	}

	key = strings.TrimSpace(line[:i])
	value = strings.TrimSpace(line[i+1:])

	// 去掉成对的引号。引号内的内容原样保留，包括 # 和空格。
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'') {
			return key, value[1 : len(value)-1], true
		}
	}

	// 未加引号时，行尾的 # 视为注释。
	//
	// 只在 # 前面是空格时才当作注释，否则会误伤
	// "P@ss#word" 这类含 # 的密码。
	if j := strings.Index(value, " #"); j >= 0 {
		value = strings.TrimSpace(value[:j])
	} else if j := strings.Index(value, "\t#"); j >= 0 {
		value = strings.TrimSpace(value[:j])
	}

	return key, value, true
}
