package tasks_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dreamina-manager/internal/crypto"
	"dreamina-manager/internal/provider"
	"dreamina-manager/internal/store"
	"dreamina-manager/internal/tasks"
)

/* ------------------------------ 假适配器 ------------------------------ */

// behavior 描述某个账号在这一次测试里该怎么表现。
type behavior struct {
	workspaceErr error
	submitErr    error
	pollErr      error
	urls         []string
}

// fakeProvider 是一个可编排的 Provider。
//
// 行为按**凭据**索引——测试里每个账号的凭据都是唯一的，
// 而 provider.Account.ID 是截断过的，不如凭据好认。
type fakeProvider struct {
	mu      sync.Mutex
	behave  map[string]*behavior
	calls   []string
	pollSeq int
}

func newFakeProvider() *fakeProvider {
	return &fakeProvider{behave: map[string]*behavior{}}
}

func (f *fakeProvider) set(credential string, b behavior) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.behave[credential] = &b
}

func (f *fakeProvider) lookup(credential string) *behavior {
	f.mu.Lock()
	defer f.mu.Unlock()
	if b, ok := f.behave[credential]; ok {
		return b
	}
	return &behavior{}
}

func (f *fakeProvider) record(entry string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, entry)
}

// callLog 返回调用序列，形如 ["submit:A", "poll:A"]。
func (f *fakeProvider) callLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeProvider) submittedTo() []string {
	var out []string
	for _, call := range f.callLog() {
		if strings.HasPrefix(call, "submit:") {
			out = append(out, strings.TrimPrefix(call, "submit:"))
		}
	}
	return out
}

func (f *fakeProvider) Name() string { return "dreamina" }

func (f *fakeProvider) Probe(context.Context, provider.Account) (*provider.ProbeResult, error) {
	return &provider.ProbeResult{}, nil
}

func (f *fakeProvider) GetCredit(context.Context, provider.Account) (*provider.Credit, error) {
	return &provider.Credit{}, nil
}

func (f *fakeProvider) Models(context.Context, provider.Account) ([]provider.ImageModel, error) {
	return []provider.ImageModel{{ID: "fake-model", Label: "Fake"}}, nil
}

func (f *fakeProvider) ListWorkspaces(_ context.Context, acct provider.Account) ([]int64, error) {
	b := f.lookup(acct.Credential)
	if b.workspaceErr != nil {
		return nil, b.workspaceErr
	}
	return []int64{1}, nil
}

func (f *fakeProvider) SubmitImage(_ context.Context, acct provider.Account, _ provider.ImageRequest) (*provider.SubmitResult, error) {
	f.record("submit:" + acct.Credential)
	b := f.lookup(acct.Credential)
	if b.submitErr != nil {
		return nil, b.submitErr
	}
	return &provider.SubmitResult{HistoryID: "hist-" + acct.Credential}, nil
}

func (f *fakeProvider) PollImage(_ context.Context, acct provider.Account, _ string, _ time.Duration, onTick provider.TickFunc) (*provider.ImageResult, error) {
	f.record("poll:" + acct.Credential)
	if onTick != nil {
		onTick(1, 50)
	}
	b := f.lookup(acct.Credential)
	if b.pollErr != nil {
		return nil, b.pollErr
	}
	urls := b.urls
	if urls == nil {
		urls = []string{"https://example.test/" + acct.Credential + ".jpg"}
	}
	return &provider.ImageResult{Status: 50, URLs: urls}, nil
}

/* ------------------------------ 测试脚手架 ------------------------------ */

type fixture struct {
	db     *store.DB
	fake   *fakeProvider
	engine *tasks.Manager
	names  []string
}

// newFixture 建一个临时库和若干账号，账号名就是 accountNames。
func newFixture(t *testing.T, opts tasks.Options, accountNames ...string) *fixture {
	t.Helper()

	dir := t.TempDir()
	key, err := crypto.LoadOrCreateKey(filepath.Join(dir, "secret.key"), "test-secret")
	if err != nil {
		t.Fatalf("创建密钥失败: %v", err)
	}
	db, err := store.Open(filepath.Join(dir, "test.db"), key)
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for _, name := range accountNames {
		if _, err := db.CreateAccount(store.CreateAccountInput{
			Name:       name,
			Credential: name, // 凭据就是账号名，方便假适配器索引
			Provider:   store.ProviderDreamina,
			Kind:       store.KindSessionID,
			Enabled:    true,
		}); err != nil {
			t.Fatalf("创建账号 %s 失败: %v", name, err)
		}
	}

	fake := newFakeProvider()
	registry := provider.NewRegistry()
	registry.Register(fake)

	return &fixture{
		db:     db,
		fake:   fake,
		engine: tasks.NewManagerWithOptions(db, registry, opts),
		names:  accountNames,
	}
}

// run 提交一次生成并等到进入终态。
func (f *fixture) run(t *testing.T, prompt string) *tasks.Record {
	t.Helper()
	record, err := f.engine.Create(context.Background(), tasks.CreateInput{
		Mode:   "text2image",
		Prompt: prompt,
	})
	if err != nil {
		t.Fatalf("提交任务失败: %v", err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		got, err := f.engine.Get(record.ID)
		if err != nil {
			t.Fatalf("读取任务失败: %v", err)
		}
		switch got.Status {
		case tasks.StatusSucceeded, tasks.StatusFailed, tasks.StatusCanceled:
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("任务未在 15 秒内结束")
	return nil
}

/* ------------------------------ 用例 ------------------------------ */

// 第一个账号登录态失效，应当自动换到第二个账号并成功。
func TestRetrySwitchesAccountOnAuthFailure(t *testing.T) {
	f := newFixture(t, tasks.Options{}, "A", "B", "C")
	f.fake.set("A", behavior{submitErr: &provider.AuthError{Message: "登录态已失效：1015"}})

	record := f.run(t, "a cat")

	if record.Status != tasks.StatusSucceeded {
		t.Fatalf("期望成功，实际 %s（%v）", record.Status, deref(record.Error))
	}
	if got := f.fake.submittedTo(); len(got) != 2 || got[0] != "A" || got[1] != "B" {
		t.Fatalf("期望依次提交给 A、B，实际 %v", got)
	}
	if record.AccountName != "B" {
		t.Fatalf("最终应当落在 B，实际 %s", record.AccountName)
	}
	// account_id 也必须跟着换，否则「这次是谁干的」会记错
	bAccount := f.account(t, "B")
	if record.AccountID != bAccount.ID {
		t.Fatalf("记录的 accountId 应当是 B 的，实际 %s", record.AccountID)
	}

	// A 被标记失效，退出调度
	a := f.account(t, "A")
	if a.Health != store.HealthExpired {
		t.Fatalf("A 应当被标记为 expired，实际 %s", a.Health)
	}
	if a.FailureCount != 1 {
		t.Fatalf("A 的失败计数应当为 1，实际 %d", a.FailureCount)
	}

	// B 成功，失败计数清零
	b := f.account(t, "B")
	if b.FailureCount != 0 || b.SuccessCount != 1 {
		t.Fatalf("B 应当 0 失败 1 成功，实际 %d/%d", b.FailureCount, b.SuccessCount)
	}
}

// 连续失败时应当依次换号，且**不会重复试同一个账号**。
func TestRetryNeverRepeatsAnAccount(t *testing.T) {
	f := newFixture(t, tasks.Options{}, "A", "B", "C")
	f.fake.set("A", behavior{submitErr: &provider.AuthError{Message: "1015"}})
	f.fake.set("B", behavior{submitErr: errors.New("经代理 http://127.0.0.1:7897 请求失败: connection refused")})

	record := f.run(t, "a dog")

	if record.Status != tasks.StatusSucceeded {
		t.Fatalf("期望成功，实际 %s（%v）", record.Status, deref(record.Error))
	}
	got := f.fake.submittedTo()
	if len(got) != 3 {
		t.Fatalf("期望尝试 3 个账号，实际 %v", got)
	}
	seen := map[string]int{}
	for _, name := range got {
		seen[name]++
	}
	for name, count := range seen {
		if count != 1 {
			t.Fatalf("账号 %s 被尝试了 %d 次，不应重复", name, count)
		}
	}

	// B 是网络问题，不该被标记为登录失效
	b := f.account(t, "B")
	if b.Health == store.HealthExpired {
		t.Fatal("网络问题不应把账号标记为登录失效")
	}
	if b.CooldownUntil == "" {
		t.Fatal("失败账号应当进入冷却")
	}
}

// 平台已受理之后的失败**不能**换号——那会产生重复生成。
func TestNoRetryAfterSubmitSucceeded(t *testing.T) {
	f := newFixture(t, tasks.Options{}, "A", "B", "C")
	f.fake.set("A", behavior{pollErr: errors.New("轮询超时")})

	record := f.run(t, "a bird")

	if record.Status != tasks.StatusFailed {
		t.Fatalf("期望失败，实际 %s", record.Status)
	}
	got := f.fake.submittedTo()
	if len(got) != 1 || got[0] != "A" {
		t.Fatalf("提交成功后不应换号，实际提交记录 %v", got)
	}
	// 失败信息应当保留 history_id，便于人工去平台侧查
	if record.HistoryID == nil || *record.HistoryID == "" {
		t.Fatal("失败记录应当保留 history_id")
	}
}

// 内容审核不通过时换号没有意义，不该浪费其它账号的额度。
func TestNoRetryOnContentRejection(t *testing.T) {
	f := newFixture(t, tasks.Options{}, "A", "B", "C")
	f.fake.set("A", behavior{
		submitErr: fmt.Errorf("%w：违规过滤", provider.ErrContentRejected),
	})

	record := f.run(t, "something disallowed")

	if record.Status != tasks.StatusFailed {
		t.Fatalf("期望失败，实际 %s", record.Status)
	}
	got := f.fake.submittedTo()
	if len(got) != 1 {
		t.Fatalf("审核失败不应换号，实际尝试了 %v", got)
	}
	// B、C 必须毫发无损
	for _, name := range []string{"B", "C"} {
		account := f.account(t, name)
		if account.FailureCount != 0 {
			t.Fatalf("账号 %s 不应被牵连，实际失败计数 %d", name, account.FailureCount)
		}
	}
}

// 所有账号都失败时，任务失败，且错误信息来自最后一次尝试。
func TestAllAccountsExhausted(t *testing.T) {
	f := newFixture(t, tasks.Options{}, "A", "B", "C")
	f.fake.set("A", behavior{submitErr: &provider.AuthError{Message: "A 挂了"}})
	f.fake.set("B", behavior{submitErr: &provider.AuthError{Message: "B 挂了"}})
	f.fake.set("C", behavior{submitErr: &provider.AuthError{Message: "C 挂了"}})

	record := f.run(t, "a fish")

	if record.Status != tasks.StatusFailed {
		t.Fatalf("期望失败，实际 %s", record.Status)
	}
	if got := f.fake.submittedTo(); len(got) != 3 {
		t.Fatalf("期望把 3 个账号都试一遍，实际 %v", got)
	}
	if record.Error == nil || !strings.Contains(*record.Error, "C 挂了") {
		t.Fatalf("错误信息应当来自最后一次尝试，实际 %v", deref(record.Error))
	}
}

// MaxAttempts 生效：配成 2 就不该碰第 3 个账号。
func TestMaxAttemptsIsRespected(t *testing.T) {
	f := newFixture(t, tasks.Options{MaxAttempts: 2}, "A", "B", "C")
	f.fake.set("A", behavior{submitErr: &provider.AuthError{Message: "A 挂了"}})
	f.fake.set("B", behavior{submitErr: &provider.AuthError{Message: "B 挂了"}})

	record := f.run(t, "a tree")

	if record.Status != tasks.StatusFailed {
		t.Fatalf("期望失败，实际 %s", record.Status)
	}
	if got := f.fake.submittedTo(); len(got) != 2 {
		t.Fatalf("最多尝试 2 个账号，实际 %v", got)
	}
	// C 不该被碰
	if c := f.account(t, "C"); c.FailureCount != 0 || c.LastUsedAt != "" {
		t.Fatal("第三个账号不该被使用")
	}
}

// 没有 workspace 属于账号自身问题，应当换号。
func TestNoWorkspaceTriggersSwitch(t *testing.T) {
	f := newFixture(t, tasks.Options{}, "A", "B")
	f.fake.set("A", behavior{workspaceErr: &provider.AuthError{Message: "1015"}})

	record := f.run(t, "a house")

	if record.Status != tasks.StatusSucceeded {
		t.Fatalf("期望换号后成功，实际 %s（%v）", record.Status, deref(record.Error))
	}
	if got := f.fake.submittedTo(); len(got) != 1 || got[0] != "B" {
		t.Fatalf("A 取 workspace 失败后应当直接换 B，实际 %v", got)
	}
}

// 只有 1 个账号时不该反复重试同一个——那只是把同一个错误撞 3 遍。
func TestSingleAccountFailsImmediately(t *testing.T) {
	f := newFixture(t, tasks.Options{}, "A")
	f.fake.set("A", behavior{submitErr: &provider.AuthError{Message: "挂了"}})

	record := f.run(t, "a car")

	if record.Status != tasks.StatusFailed {
		t.Fatalf("期望失败，实际 %s", record.Status)
	}
	if got := f.fake.submittedTo(); len(got) != 1 {
		t.Fatalf("只有一个账号时不应重复尝试，实际 %v", got)
	}
}

/* ------------------------------ 辅助 ------------------------------ */

func (f *fixture) account(t *testing.T, name string) *store.Account {
	t.Helper()
	accounts, err := f.db.ListAccounts()
	if err != nil {
		t.Fatalf("列账号失败: %v", err)
	}
	for i := range accounts {
		if accounts[i].Name == name {
			return &accounts[i]
		}
	}
	t.Fatalf("找不到账号 %s", name)
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
