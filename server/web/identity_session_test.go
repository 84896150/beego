package web

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/beego/beego/v2/core/dbbridge"
	"github.com/beego/beego/v2/server/web/context"
)

func newTestControllerWithReq(req *http.Request) (*Controller, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	ctx := context.NewContext()
	ctx.Reset(rec, req)
	ctrl := &Controller{}
	ctrl.Init(ctx, "TestCtrl", "Action", ctrl)
	return ctrl, rec
}

func extractCookieFromRecorder(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	res := rec.Result()
	defer res.Body.Close()
	for _, ck := range res.Cookies() {
		if ck.Name == name {
			return ck
		}
	}
	return nil
}

func TestIdentitySession_SingleDeviceKickAndInstantBan(t *testing.T) {
	dbbridge.ResetDefault()
	ResetSessions()
	defer ResetSessions()

	BConfig.AppName = "edurms-demo"
	BConfig.WebConfig.Session.SessionCookieName = ""
	BConfig.WebConfig.Session.SessionMaxDevices = 1
	BConfig.WebConfig.Session.SessionTTL = 3600
	BConfig.WebConfig.Session.SessionAutoRefresh = true

	assert.Equal(t, "edurms_demo_session", ResolveSessionCookieName())

	// 设备 A 登录
	reqALogin := httptest.NewRequest(http.MethodPost, "http://localhost/login", nil)
	reqALogin.Header.Set("X-Forwarded-Proto", "https")
	reqALogin.RemoteAddr = "127.0.0.1:50001"
	ctrlA, recA := newTestControllerWithReq(reqALogin)

	sessA, err := ctrlA.LoginSession(42, map[string]any{"username": "teacher1", "role": "teacher"})
	require.NoError(t, err)
	require.NotNil(t, sessA)

	ckA := extractCookieFromRecorder(recA, "edurms_demo_session")
	require.NotNil(t, ckA)
	assert.True(t, ckA.HttpOnly)
	assert.True(t, ckA.Secure)
	assert.Equal(t, http.SameSiteLaxMode, ckA.SameSite)

	// 设备 A 正常访问有效
	reqAVerify := httptest.NewRequest(http.MethodGet, "http://localhost/api/profile", nil)
	reqAVerify.AddCookie(ckA)
	ctrlAVerify, _ := newTestControllerWithReq(reqAVerify)
	gotA, statusA := ctrlAVerify.VerifySession()
	require.Equal(t, SessionValid, statusA)
	assert.Equal(t, int64(42), gotA.UID)
	assert.Equal(t, "teacher", gotA.GetString("role"))

	// 设备 B 用同一账号登录 -> 触发天然单设备互踢
	reqBLogin := httptest.NewRequest(http.MethodPost, "http://localhost/login", nil)
	ctrlB, recB := newTestControllerWithReq(reqBLogin)
	_, err = ctrlB.LoginSession(42, map[string]any{"username": "teacher1", "role": "teacher"})
	require.NoError(t, err)
	ckB := extractCookieFromRecorder(recB, "edurms_demo_session")
	require.NotNil(t, ckB)

	// 设备 A 再次请求 -> 精确识别为 SessionKicked (被顶号)，且自动清理失效 Cookie
	reqAKicked := httptest.NewRequest(http.MethodGet, "http://localhost/api/profile", nil)
	reqAKicked.AddCookie(ckA)
	ctrlAKicked, recAKicked := newTestControllerWithReq(reqAKicked)
	gotAfterKick, statusAfterKick := ctrlAKicked.VerifySession()
	assert.Nil(t, gotAfterKick)
	assert.Equal(t, SessionKicked, statusAfterKick)
	clearedCk := extractCookieFromRecorder(recAKicked, "edurms_demo_session")
	require.NotNil(t, clearedCk)
	assert.Less(t, clearedCk.MaxAge, 0)

	// 设备 B 当前保持正常在线
	reqBVerify := httptest.NewRequest(http.MethodGet, "http://localhost/api/profile", nil)
	reqBVerify.AddCookie(ckB)
	ctrlBVerify, _ := newTestControllerWithReq(reqBVerify)
	_, statusB := ctrlBVerify.VerifySession()
	assert.Equal(t, SessionValid, statusB)

	// 管理员后台执行秒级封号 KickUserSessions(42) -> 设备 B 下一秒立刻失效
	require.NoError(t, KickUserSessions(42))
	reqBBanned := httptest.NewRequest(http.MethodGet, "http://localhost/api/profile", nil)
	reqBBanned.AddCookie(ckB)
	ctrlBBanned, _ := newTestControllerWithReq(reqBBanned)
	gotBanned, statusBanned := ctrlBBanned.VerifySession()
	assert.Nil(t, gotBanned)
	assert.Equal(t, SessionInvalid, statusBanned)
}

func TestIdentitySession_SQLitePersistenceAndRestartRecovery(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer func() {
		dbbridge.ResetDefault()
		_ = db.Close()
	}()
	db.SetMaxOpenConns(1)

	dbbridge.RegisterDefault(db, "sqlite3")
	ResetSessions()

	BConfig.AppName = "zhykt"
	BConfig.WebConfig.Session.SessionCookieName = "zhykt_session"
	BConfig.WebConfig.Session.SessionMaxDevices = 2
	BConfig.WebConfig.Session.SessionTTL = 100
	BConfig.WebConfig.Session.SessionAutoRefresh = true

	// 登录第 1、第 2 台设备（限额 2 台均共存）
	req1 := httptest.NewRequest(http.MethodPost, "http://localhost/login", nil)
	c1, r1 := newTestControllerWithReq(req1)
	_, err = c1.LoginSession(99, map[string]any{"role": "admin", "level": 3})
	require.NoError(t, err)
	ck1 := extractCookieFromRecorder(r1, "zhykt_session")
	require.NotNil(t, ck1)

	time.Sleep(5 * time.Millisecond)
	req2 := httptest.NewRequest(http.MethodPost, "http://localhost/login", nil)
	c2, r2 := newTestControllerWithReq(req2)
	_, err = c2.LoginSession(99, map[string]any{"role": "admin", "level": 3})
	require.NoError(t, err)
	ck2 := extractCookieFromRecorder(r2, "zhykt_session")
	require.NotNil(t, ck2)

	// 模拟 Go 服务进程重启（清空进程内存缓存，仅保留 SQLite 表数据）
	eng := getIdentitySessionEngine()
	eng.mu.Lock()
	eng.records = make(map[string]*sessionRecord)
	eng.userIndex = make(map[int64][]string)
	eng.mu.Unlock()

	// 重启后两台设备无需重登，直接从 beego_session 表无感恢复
	vReq1 := httptest.NewRequest(http.MethodGet, "http://localhost/check", nil)
	vReq1.AddCookie(ck1)
	vc1, _ := newTestControllerWithReq(vReq1)
	s1, st1 := vc1.VerifySession()
	require.Equal(t, SessionValid, st1)
	assert.Equal(t, int64(3), s1.GetInt64("level"))

	// 第 3 台设备登录 -> 超出 MaxDevices=2，重启前最早的第 1 台设备被精准踢出
	req3 := httptest.NewRequest(http.MethodPost, "http://localhost/login", nil)
	c3, _ := newTestControllerWithReq(req3)
	_, err = c3.LoginSession(99, map[string]any{"role": "admin"})
	require.NoError(t, err)

	vReq1Again := httptest.NewRequest(http.MethodGet, "http://localhost/check", nil)
	vReq1Again.AddCookie(ck1)
	vc1Again, _ := newTestControllerWithReq(vReq1Again)
	_, st1Again := vc1Again.VerifySession()
	assert.Equal(t, SessionKicked, st1Again)

	vReq2Again := httptest.NewRequest(http.MethodGet, "http://localhost/check", nil)
	vReq2Again.AddCookie(ck2)
	vc2Again, _ := newTestControllerWithReq(vReq2Again)
	_, st2Again := vc2Again.VerifySession()
	assert.Equal(t, SessionValid, st2Again)
}

func TestIdentitySession_AutoRefreshSwitch(t *testing.T) {
	dbbridge.ResetDefault()
	ResetSessions()
	defer ResetSessions()

	BConfig.WebConfig.Session.SessionCookieName = "refresh_test_session"
	BConfig.WebConfig.Session.SessionTTL = 100

	// 1. 开启自动续期 (SessionAutoRefresh = true)：剩余寿命 <= 50s 时自动顺延并重发 Set-Cookie
	BConfig.WebConfig.Session.SessionAutoRefresh = true
	reqLogin := httptest.NewRequest(http.MethodPost, "http://localhost/login", nil)
	ctrl, rec := newTestControllerWithReq(reqLogin)
	sess, err := ctrl.LoginSession(7, nil)
	require.NoError(t, err)
	ck := extractCookieFromRecorder(rec, "refresh_test_session")

	// 将剩余寿命人工拨到仅剩 30 秒（小于半程 50 秒）
	nearExpire := time.Now().Unix() + 30
	eng := getIdentitySessionEngine()
	eng.mu.Lock()
	eng.records[sess.SID].expiresAt = nearExpire
	eng.mu.Unlock()

	reqVerify := httptest.NewRequest(http.MethodGet, "http://localhost/ping", nil)
	reqVerify.AddCookie(ck)
	ctrlV, recV := newTestControllerWithReq(reqVerify)
	refreshedSess, st := ctrlV.VerifySession()
	require.Equal(t, SessionValid, st)
	assert.Greater(t, refreshedSess.ExpiresAt, nearExpire+50)
	renewedCk := extractCookieFromRecorder(recV, "refresh_test_session")
	require.NotNil(t, renewedCk, "滑动续期触发时应同步补发 Set-Cookie 续命")

	// 2. 关闭自动续期 (SessionAutoRefresh = false)：即使剩余 20 秒也绝不续期
	BConfig.WebConfig.Session.SessionAutoRefresh = false
	fixedExpire := time.Now().Unix() + 20
	eng.mu.Lock()
	eng.records[sess.SID].expiresAt = fixedExpire
	eng.mu.Unlock()

	reqNoRefresh := httptest.NewRequest(http.MethodGet, "http://localhost/ping", nil)
	reqNoRefresh.AddCookie(ck)
	ctrlNR, recNR := newTestControllerWithReq(reqNoRefresh)
	notRefreshed, stNR := ctrlNR.VerifySession()
	require.Equal(t, SessionValid, stNR)
	assert.Equal(t, fixedExpire, notRefreshed.ExpiresAt)
	assert.Nil(t, extractCookieFromRecorder(recNR, "refresh_test_session"))
}
