package migrate

import (
	"os"
	"path/filepath"
	"testing"
)

func writeMigration(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadSortsByVersion(t *testing.T) {
	dir := t.TempDir()
	// 故意乱序写入，确认加载后按版本排好。
	writeMigration(t, dir, "0003_c.up.sql", "SELECT 3;")
	writeMigration(t, dir, "0001_a.up.sql", "SELECT 1;")
	writeMigration(t, dir, "0002_b.up.sql", "SELECT 2;")

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("加载 %d 条, want 3", len(got))
	}
	for i, want := range []int{1, 2, 3} {
		if got[i].Version != want {
			t.Errorf("第 %d 条版本 = %d, want %d", i, got[i].Version, want)
		}
	}
}

// TestLoadIgnoresDownFiles 确认只加载 up 迁移。
// 把 down 也当迁移执行会把刚建好的表删掉。
func TestLoadIgnoresDownFiles(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "0001_a.up.sql", "SELECT 1;")
	writeMigration(t, dir, "0001_a.down.sql", "DROP TABLE a;")

	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("加载 %d 条, want 1（down 不应被加载）", len(got))
	}
	if got[0].SQL != "SELECT 1;" {
		t.Errorf("加载了错误的内容: %q", got[0].SQL)
	}
}

// TestLoadRejectsBadNames 确认命名不合规的 .sql 会被拒绝。
//
// 静默忽略会让"我以为加了个迁移但其实没生效"变成很难查的问题。
func TestLoadRejectsBadNames(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "add_something.sql", "SELECT 1;")

	if _, err := Load(dir); err == nil {
		t.Error("命名不合规的迁移应当报错")
	}
}

func TestLoadRejectsDuplicateVersion(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "0001_a.up.sql", "SELECT 1;")
	writeMigration(t, dir, "0001_b.up.sql", "SELECT 2;")

	if _, err := Load(dir); err == nil {
		t.Error("版本号重复应当报错")
	}
}

func TestLoadChecksumChangesWithContent(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "0001_a.up.sql", "SELECT 1;")
	first, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	writeMigration(t, dir, "0001_a.up.sql", "SELECT 2;")
	second, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	if first[0].Checksum == second[0].Checksum {
		t.Error("内容变了校验和应当变化，否则无法发现历史迁移被改动")
	}
}

func TestLoadEmptyDir(t *testing.T) {
	got, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("空目录不应报错: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("空目录应返回 0 条, got %d", len(got))
	}
}

func TestLoadMissingDir(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("目录不存在应当报错")
	}
}

// TestLoadIgnoresNonSQLFiles 确认 README 之类的文件不会干扰。
func TestLoadIgnoresNonSQLFiles(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "0001_a.up.sql", "SELECT 1;")
	writeMigration(t, dir, "README.md", "# 迁移说明")

	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("应只加载 1 条, got %d", len(got))
	}
}

// TestRepoMigrationsAreValid 确认仓库里真实的迁移文件能被正确加载。
//
// 这条用的是实际文件而不是临时构造的，能发现命名写错这类问题。
func TestRepoMigrationsAreValid(t *testing.T) {
	dir := findRepoMigrations(t)
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("加载仓库迁移失败: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("仓库里没有迁移文件")
	}
	// 版本号必须递增且不重复。
	for i := 1; i < len(got); i++ {
		if got[i].Version <= got[i-1].Version {
			t.Errorf("版本号未递增: %d 之后是 %d", got[i-1].Version, got[i].Version)
		}
	}
}

func findRepoMigrations(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := wd
	for i := 0; i < 6; i++ {
		candidate := filepath.Join(dir, "migrations")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("未找到 migrations 目录（从 %s 向上查找）", wd)
	return ""
}
