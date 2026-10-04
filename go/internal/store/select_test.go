package store

import (
	"path/filepath"
	"testing"

	"dreamina-manager/internal/crypto"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	key, err := crypto.LoadOrCreateKey(filepath.Join(dir, "secret.key"), "test-secret")
	if err != nil {
		t.Fatalf("创建测试密钥失败: %v", err)
	}
	db, err := Open(filepath.Join(dir, "test.db"), key)
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func addAccount(t *testing.T, db *DB, name string) *Account {
	t.Helper()
	account, err := db.CreateAccount(CreateAccountInput{
		Name:       name,
		Credential: "sessionid-" + name,
		Provider:   ProviderDreamina,
		Kind:       KindSessionID,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("创建账号 %s 失败: %v", name, err)
	}
	return account
}

// 重试换号的核心保证：已经试过的账号不能再被挑中。
func TestSelectAccountExcludingSkipsTried(t *testing.T) {
	db := newTestDB(t)
	a := addAccount(t, db, "A")
	b := addAccount(t, db, "B")
	c := addAccount(t, db, "C")

	exclude := map[string]bool{}
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		account, err := db.SelectAccountExcluding(StrategyLeastFailures, "dreamina", exclude)
		if err != nil {
			t.Fatalf("第 %d 次挑选失败: %v", i+1, err)
		}
		if exclude[account.ID] {
			t.Fatalf("挑到了已排除的账号 %s", account.Name)
		}
		seen[account.ID] = true
		exclude[account.ID] = true
	}

	if len(seen) != 3 {
		t.Fatalf("三次挑选应当覆盖 3 个不同账号，实际 %d 个", len(seen))
	}
	for _, id := range []string{a.ID, b.ID, c.ID} {
		if !seen[id] {
			t.Fatalf("账号 %s 从未被挑中", id)
		}
	}

	// 全排除后必须报错，而不是回退到已试过的账号
	if _, err := db.SelectAccountExcluding(StrategyLeastFailures, "dreamina", exclude); err == nil {
		t.Fatal("账号全部排除后应当返回错误")
	}
}

// 冷却中的账号不参与调度，即便没被显式排除。
func TestSelectAccountSkipsCooldownAndExpired(t *testing.T) {
	db := newTestDB(t)
	good := addAccount(t, db, "good")
	cooling := addAccount(t, db, "cooling")
	expired := addAccount(t, db, "expired")

	if err := db.MarkFailure(cooling.ID, "boom", "2999-01-01T00:00:00.000Z"); err != nil {
		t.Fatalf("标记冷却失败: %v", err)
	}
	if err := db.MarkExpired(expired.ID, "1015"); err != nil {
		t.Fatalf("标记失效失败: %v", err)
	}

	for i := 0; i < 5; i++ {
		account, err := db.SelectAccountExcluding(StrategyLeastFailures, "dreamina", nil)
		if err != nil {
			t.Fatalf("挑选失败: %v", err)
		}
		if account.ID != good.ID {
			t.Fatalf("应当只挑到 good，实际挑到 %s", account.Name)
		}
	}
}

// 成功会把失败计数清零，进而影响「失败最少优先」的排序。
func TestMarkSuccessResetsFailureCount(t *testing.T) {
	db := newTestDB(t)
	a := addAccount(t, db, "A")
	b := addAccount(t, db, "B")

	if err := db.MarkFailure(a.ID, "boom", ""); err != nil {
		t.Fatalf("标记失败: %v", err)
	}
	// A 有 1 次失败，B 是 0，应当挑 B
	picked, err := db.SelectAccountExcluding(StrategyLeastFailures, "dreamina", nil)
	if err != nil {
		t.Fatalf("挑选失败: %v", err)
	}
	if picked.ID != b.ID {
		t.Fatalf("失败最少优先应当挑 B，实际挑到 %s", picked.Name)
	}

	if err := db.MarkSuccess(a.ID); err != nil {
		t.Fatalf("标记成功: %v", err)
	}
	reloaded, err := db.GetAccount(a.ID)
	if err != nil {
		t.Fatalf("读取账号失败: %v", err)
	}
	if reloaded.FailureCount != 0 {
		t.Fatalf("成功之后失败计数应当归零，实际 %d", reloaded.FailureCount)
	}
}
