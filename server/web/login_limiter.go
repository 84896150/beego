package web

import (
	"strings"
	"sync"
	"time"

	"github.com/beego/beego/v2/server/web/context"
)

const maxLimiterEntries = 10000

type loginFailBucket struct {
	count       int
	firstFailAt time.Time
	lockedUntil time.Time
}

type loginRateLimiter struct {
	mu      sync.Mutex
	records map[string]*loginFailBucket
}

var globalLoginLimiter = &loginRateLimiter{
	records: make(map[string]*loginFailBucket),
}

func init() {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			globalLoginLimiter.purgeExpired(time.Now(), resolveLoginWindow())
		}
	}()
}

func isLoginLimiterEnabled() bool {
	if BConfig != nil && BConfig.RunMode == "test" {
		if AppConfig != nil {
			if force, err := AppConfig.Bool("forceLoginRateLimitInTest"); err == nil && force {
				return true
			}
		}
		return false
	}
	if AppConfig != nil {
		if dis, err := AppConfig.Bool("disableLoginRateLimit"); err == nil && dis {
			return false
		}
		if on, err := AppConfig.Bool("loginRateLimitOn"); err == nil {
			return on
		}
	}
	if BConfig != nil {
		return BConfig.WebConfig.Session.LoginRateLimitOn
	}
	return true
}

func resolveLoginWindow() time.Duration {
	sec := 300
	if AppConfig != nil {
		if v, err := AppConfig.Int("loginWindowSec"); err == nil && v > 0 {
			sec = v
		}
	} else if BConfig != nil && BConfig.WebConfig.Session.LoginWindowSec > 0 {
		sec = BConfig.WebConfig.Session.LoginWindowSec
	}
	if BConfig != nil && BConfig.WebConfig.Session.LoginWindowSec > 0 && (AppConfig == nil || AppConfig.DefaultInt("loginWindowSec", 0) == 0) {
		sec = BConfig.WebConfig.Session.LoginWindowSec
	}
	return time.Duration(sec) * time.Second
}

func resolveLoginLockDuration() time.Duration {
	sec := 900
	if AppConfig != nil {
		if v, err := AppConfig.Int("loginLockSec"); err == nil && v > 0 {
			sec = v
		}
	}
	if BConfig != nil && BConfig.WebConfig.Session.LoginLockSec > 0 && (AppConfig == nil || AppConfig.DefaultInt("loginLockSec", 0) == 0) {
		sec = BConfig.WebConfig.Session.LoginLockSec
	}
	return time.Duration(sec) * time.Second
}

func resolveLoginMaxFails() int {
	n := 5
	if AppConfig != nil {
		if v, err := AppConfig.Int("loginMaxFails"); err == nil && v > 0 {
			return v
		}
	}
	if BConfig != nil && BConfig.WebConfig.Session.LoginMaxFails > 0 {
		n = BConfig.WebConfig.Session.LoginMaxFails
	}
	return n
}

func resolveLoginIpMaxFails() int {
	n := 30
	if AppConfig != nil {
		if v, err := AppConfig.Int("loginIpMaxFails"); err == nil && v > 0 {
			return v
		}
	}
	if BConfig != nil && BConfig.WebConfig.Session.LoginIpMaxFails > 0 {
		n = BConfig.WebConfig.Session.LoginIpMaxFails
	}
	return n
}

func normalizeLimiterIP(ip string) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return "unknown"
	}
	return ip
}

func buildAccountKey(ip, username string) string {
	return "acct:" + ip + "|" + strings.ToLower(strings.TrimSpace(username))
}

func buildIPKey(ip string) string {
	return "ip:" + ip
}

func (l *loginRateLimiter) purgeExpired(now time.Time, window time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.purgeExpiredLocked(now, window)
}

func (l *loginRateLimiter) purgeExpiredLocked(now time.Time, window time.Duration) {
	stale := 2 * window
	if stale < 10*time.Minute {
		stale = 10 * time.Minute
	}
	for k, rec := range l.records {
		if (!rec.lockedUntil.IsZero() && !now.Before(rec.lockedUntil)) ||
			(rec.lockedUntil.IsZero() && now.Sub(rec.firstFailAt) > stale) {
			delete(l.records, k)
		}
	}
}

func (l *loginRateLimiter) checkKeyLocked(key string, now time.Time, window time.Duration) (bool, time.Duration) {
	rec, exists := l.records[key]
	if !exists {
		return true, 0
	}
	if !rec.lockedUntil.IsZero() {
		if now.Before(rec.lockedUntil) {
			return false, rec.lockedUntil.Sub(now)
		}
		delete(l.records, key)
		return true, 0
	}
	if now.Sub(rec.firstFailAt) > window {
		delete(l.records, key)
	}
	return true, 0
}

func (l *loginRateLimiter) recordKeyLocked(key string, maxFails int, now time.Time, window, lockDur time.Duration) {
	if maxFails <= 0 {
		return
	}
	if len(l.records) >= maxLimiterEntries {
		l.purgeExpiredLocked(now, window)
		if _, exists := l.records[key]; !exists && len(l.records) >= maxLimiterEntries {
			return
		}
	}

	rec, exists := l.records[key]
	if !exists || now.Sub(rec.firstFailAt) > window {
		bucket := &loginFailBucket{
			count:       1,
			firstFailAt: now,
		}
		if maxFails == 1 {
			bucket.lockedUntil = now.Add(lockDur)
		}
		l.records[key] = bucket
		return
	}
	rec.count++
	if rec.count >= maxFails && rec.lockedUntil.IsZero() {
		rec.lockedUntil = now.Add(lockDur)
	}
}

// CheckLoginRateLimit 检查指定 IP（及可选账号名）是否允许尝试登录，被锁定时返回 false 与剩余等待时长
func CheckLoginRateLimit(ip string, username ...string) (bool, time.Duration) {
	if !isLoginLimiterEnabled() {
		return true, 0
	}
	cleanIP := normalizeLimiterIP(ip)
	acct := ""
	if len(username) > 0 {
		acct = strings.TrimSpace(username[0])
	}
	now := time.Now()
	window := resolveLoginWindow()

	globalLoginLimiter.mu.Lock()
	defer globalLoginLimiter.mu.Unlock()

	if ok, wait := globalLoginLimiter.checkKeyLocked(buildIPKey(cleanIP), now, window); !ok {
		return false, wait
	}
	if acct != "" {
		if ok, wait := globalLoginLimiter.checkKeyLocked(buildAccountKey(cleanIP, acct), now, window); !ok {
			return false, wait
		}
	}
	return true, 0
}

// RecordLoginFail 记录一次登录失败（若提供账号则按 IP+账号 与 单IP兜底 双维计数；未提供账号则按单 IP 计数）
func RecordLoginFail(ip string, username ...string) {
	if !isLoginLimiterEnabled() {
		return
	}
	cleanIP := normalizeLimiterIP(ip)
	acct := ""
	if len(username) > 0 {
		acct = strings.TrimSpace(username[0])
	}

	now := time.Now()
	window := resolveLoginWindow()
	lockDur := resolveLoginLockDuration()
	maxAcct := resolveLoginMaxFails()
	maxIP := resolveLoginIpMaxFails()

	globalLoginLimiter.mu.Lock()
	defer globalLoginLimiter.mu.Unlock()

	if acct != "" {
		globalLoginLimiter.recordKeyLocked(buildAccountKey(cleanIP, acct), maxAcct, now, window, lockDur)
		globalLoginLimiter.recordKeyLocked(buildIPKey(cleanIP), maxIP, now, window, lockDur)
	} else {
		globalLoginLimiter.recordKeyLocked(buildIPKey(cleanIP), maxAcct, now, window, lockDur)
	}
}

// ClearLoginFail 登录成功后清除对应的失败计数记录
func ClearLoginFail(ip string, username ...string) {
	cleanIP := normalizeLimiterIP(ip)
	acct := ""
	if len(username) > 0 {
		acct = strings.TrimSpace(username[0])
	}

	globalLoginLimiter.mu.Lock()
	defer globalLoginLimiter.mu.Unlock()

	if acct != "" {
		delete(globalLoginLimiter.records, buildAccountKey(cleanIP, acct))
	} else {
		delete(globalLoginLimiter.records, buildIPKey(cleanIP))
	}
}

// ClearLoginFailForContext 根据请求上下文清除失败记录
func ClearLoginFailForContext(ctx *context.Context, username string) {
	if ctx == nil {
		return
	}
	ip := ctx.ClientIP()
	if strings.TrimSpace(username) != "" {
		ClearLoginFail(ip, username)
	}
	// 若此前仅有纯 IP 级未锁定零散计数，不误删已被全局封禁的恶意扫描 IP，仅清理账号桶及未封禁计数
	cleanIP := normalizeLimiterIP(ip)
	globalLoginLimiter.mu.Lock()
	if rec, ok := globalLoginLimiter.records[buildIPKey(cleanIP)]; ok && rec.lockedUntil.IsZero() {
		delete(globalLoginLimiter.records, buildIPKey(cleanIP))
	}
	globalLoginLimiter.mu.Unlock()
}

// ResetLoginLimiter 清空全部登录防爆破限流状态（供单元测试隔离使用）
func ResetLoginLimiter() {
	globalLoginLimiter.mu.Lock()
	globalLoginLimiter.records = make(map[string]*loginFailBucket)
	globalLoginLimiter.mu.Unlock()
}
