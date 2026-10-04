package store

import "testing"

// 配额必须能存进去、读回来。
//
// 这个测试是为了锁住一个真实踩过的坑：加配额字段时改了 INSERT 和 ListAPIKeys，
// 但漏了 GetAPIKey 和 VerifyAPIKey 的 SELECT。表现是——
// 创建接口返回 201、字段看着都在，但回读全是 0，
// **限流器拿到的永远是「不限」，整个配额功能静默失效**。
func TestAPIKeyQuotaRoundTrip(t *testing.T) {
	db := newTestDB(t)

	created, plain, err := db.CreateAPIKey(CreateAPIKeyInput{
		Name:          "带配额",
		Remark:        "测试",
		RatePerMinute: 3,
		MaxConcurrent: 1,
		DailyQuota:    2,
	})
	if err != nil {
		t.Fatalf("创建密钥失败: %v", err)
	}
	if plain == "" {
		t.Fatal("应当返回一次性明文")
	}

	assertQuota := func(stage string, k *APIKey) {
		t.Helper()
		if k.RatePerMinute != 3 {
			t.Errorf("%s: RatePerMinute = %d，期望 3", stage, k.RatePerMinute)
		}
		if k.MaxConcurrent != 1 {
			t.Errorf("%s: MaxConcurrent = %d，期望 1", stage, k.MaxConcurrent)
		}
		if k.DailyQuota != 2 {
			t.Errorf("%s: DailyQuota = %d，期望 2", stage, k.DailyQuota)
		}
	}

	assertQuota("创建返回值", created)

	// 回读路径
	got, err := db.GetAPIKey(created.ID)
	if err != nil {
		t.Fatalf("回读密钥失败: %v", err)
	}
	assertQuota("GetAPIKey", got)

	// 校验路径——限流器走的就是这条，漏了它配额等于没有
	verified, err := db.VerifyAPIKey(plain)
	if err != nil {
		t.Fatalf("校验密钥失败: %v", err)
	}
	assertQuota("VerifyAPIKey", verified)

	// 列表路径
	list, err := db.ListAPIKeys()
	if err != nil {
		t.Fatalf("列出密钥失败: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("应当有 1 把密钥，实际 %d", len(list))
	}
	assertQuota("ListAPIKeys", &list[0])
}

// 每日配额按天累计，且用满后不再放行。
func TestAPIKeyDailyQuota(t *testing.T) {
	db := newTestDB(t)

	key, _, err := db.CreateAPIKey(CreateAPIKeyInput{Name: "限额2", DailyQuota: 2})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	for i := 1; i <= 2; i++ {
		used, ok, err := db.ConsumeQuota(key.ID, 2)
		if err != nil {
			t.Fatalf("第 %d 次扣减出错: %v", i, err)
		}
		if !ok {
			t.Fatalf("第 %d 次应当放行", i)
		}
		if used != int64(i) {
			t.Fatalf("第 %d 次用量应当为 %d，实际 %d", i, i, used)
		}
	}

	used, ok, err := db.ConsumeQuota(key.ID, 2)
	if err != nil {
		t.Fatalf("超额扣减出错: %v", err)
	}
	if ok {
		t.Fatal("超出配额后不应放行")
	}
	if used != 2 {
		t.Fatalf("超额时用量应当仍为 2，实际 %d", used)
	}

	if got := db.TodayUsage(key.ID); got != 2 {
		t.Fatalf("今日用量应当为 2，实际 %d", got)
	}
}

// 配额为 0 表示不限量，但仍要计数（界面上要能显示用量）。
func TestAPIKeyQuotaZeroMeansUnlimited(t *testing.T) {
	db := newTestDB(t)

	key, _, err := db.CreateAPIKey(CreateAPIKeyInput{Name: "不限"})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	for i := 0; i < 50; i++ {
		_, ok, err := db.ConsumeQuota(key.ID, 0)
		if err != nil {
			t.Fatalf("扣减出错: %v", err)
		}
		if !ok {
			t.Fatalf("配额为 0 时第 %d 次就被拦了", i+1)
		}
	}
	if got := db.TodayUsage(key.ID); got != 50 {
		t.Fatalf("即便不限量也应当计数，实际 %d", got)
	}
}

// 吊销后校验必须失败，且吊销的密钥不能通过校验拿到配额。
func TestVerifyAPIKeyRejectsRevoked(t *testing.T) {
	db := newTestDB(t)

	key, plain, err := db.CreateAPIKey(CreateAPIKeyInput{Name: "待吊销", RatePerMinute: 5})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if _, err := db.VerifyAPIKey(plain); err != nil {
		t.Fatalf("吊销前应当能校验通过: %v", err)
	}

	if err := db.SetAPIKeyEnabled(key.ID, false); err != nil {
		t.Fatalf("吊销失败: %v", err)
	}
	if _, err := db.VerifyAPIKey(plain); err == nil {
		t.Fatal("吊销后校验应当失败")
	}
	if _, err := db.VerifyAPIKey("sk-dm-nonexistent"); err == nil {
		t.Fatal("不存在的密钥应当校验失败")
	}
}
