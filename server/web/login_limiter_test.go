package web

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/beego/beego/v2/server/web/context"
)

func TestLoginLimiter_AccountIsolationAndAntiSpoofProxy(t *testing.T) {
	origMode := BConfig.RunMode
	BConfig.RunMode = PROD
	defer func() {
		BConfig.RunMode = origMode
		context.SetTrustedProxies("loopback,private")
		ResetLoginLimiter()
	}()

	context.SetTrustedProxies("loopback,private")
	BConfig.WebConfig.Session.LoginRateLimitOn = true
	BConfig.WebConfig.Session.LoginWindowSec = 300
	BConfig.WebConfig.Session.LoginMaxFails = 3
	BConfig.WebConfig.Session.LoginIpMaxFails = 6
	BConfig.WebConfig.Session.LoginLockSec = 600
	ResetLoginLimiter()

	// 1. 公网直连攻击者伪造 X-Forwarded-For 试图绕过限流 -> 因直连 IP 非内网反代，伪造头被无视
	for i := 0; i < 3; i++ {
		req := makeSpoofedReq("203.0.113.88:41234", "1.2.3."+string(rune('1'+i)))
		ctrl, _ := newTestControllerWithReq(req)
		assert.Equal(t, "203.0.113.88", ctrl.ClientIP())
		ok, _ := ctrl.CheckLoginLimit("studentA")
		require.True(t, ok)
		ctrl.RecordLoginFail("studentA")
	}

	// 第 4 次尝试登录 studentA（哪怕继续换伪造 X-Forwarded-For）依然被精准封禁
	reqBlocked := makeSpoofedReq("203.0.113.88:55555", "9.9.9.9")
	ctrlBlocked, _ := newTestControllerWithReq(reqBlocked)
	okA, waitA := ctrlBlocked.CheckLoginLimit("studentA")
	assert.False(t, okA)
	assert.Greater(t, waitA.Seconds(), float64(500))

	// 2. 同机房共用 IP (203.0.113.88) 下的另一名正常学生 studentB 不受误伤，仍可正常登录
	okB, _ := ctrlBlocked.CheckLoginLimit("studentB")
	assert.True(t, okB, "同机房其他账号不应被单人输错密码连坐锁定")

	// 3. 若该 IP 继续恶意枚举不同账号累计达到 LoginIpMaxFails(6) -> 整个 IP 被全局封禁
	ctrlBlocked.RecordLoginFail("studentC")
	ctrlBlocked.RecordLoginFail("studentD")
	ctrlBlocked.RecordLoginFail("studentE")

	okAny, _ := ctrlBlocked.CheckLoginLimit("brandNewUser")
	assert.False(t, okAny, "超出单 IP 全局失败上限后应封禁整个 IP")
}

func makeSpoofedReq(remoteAddr, fakeXFF string) *http.Request {
	req, _ := http.NewRequest(http.MethodPost, "http://example.com/login", nil)
	req.RemoteAddr = remoteAddr
	req.Header.Set("X-Forwarded-For", fakeXFF)
	req.Header.Set("X-Real-IP", fakeXFF)
	return req
}
