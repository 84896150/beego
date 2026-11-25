package context

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type trustedProxyPolicy struct {
	trustAll     bool
	trustNone    bool
	allowLoop    bool
	allowPrivate bool
	ips          map[string]struct{}
	cidrs        []*net.IPNet
}

var (
	proxyMu          sync.RWMutex
	trustedSpecRaw   = "loopback,private"
	trustedProxyRule = parseTrustedProxyPolicy("loopback,private")
)

func parseTrustedProxyPolicy(spec string) trustedProxyPolicy {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		return trustedProxyPolicy{allowLoop: true, allowPrivate: true}
	}
	lower := strings.ToLower(trimmed)
	if lower == "none" || lower == "false" || lower == "off" {
		return trustedProxyPolicy{trustNone: true}
	}
	if trimmed == "*" || lower == "all" || lower == "true" {
		return trustedProxyPolicy{trustAll: true}
	}

	p := trustedProxyPolicy{
		ips: make(map[string]struct{}),
	}
	for _, token := range strings.Split(trimmed, ",") {
		part := strings.TrimSpace(token)
		if part == "" {
			continue
		}
		switch strings.ToLower(part) {
		case "loopback", "localhost":
			p.allowLoop = true
		case "private", "intranet", "lan":
			p.allowPrivate = true
		case "*":
			return trustedProxyPolicy{trustAll: true}
		case "none":
			return trustedProxyPolicy{trustNone: true}
		default:
			if strings.Contains(part, "/") {
				if _, network, err := net.ParseCIDR(part); err == nil && network != nil {
					p.cidrs = append(p.cidrs, network)
				}
			} else if ip := net.ParseIP(part); ip != nil {
				p.ips[ip.String()] = struct{}{}
			}
		}
	}
	return p
}

// SetTrustedProxies 设置全局可信反向代理策略（如 "loopback,private"、"*"、"none" 或逗号分隔的 IP/CIDR）
func SetTrustedProxies(spec string) {
	proxyMu.Lock()
	trustedSpecRaw = spec
	trustedProxyRule = parseTrustedProxyPolicy(spec)
	proxyMu.Unlock()
}

// GetTrustedProxies 返回当前生效的可信反向代理策略描述串
func GetTrustedProxies() string {
	proxyMu.RLock()
	defer proxyMu.RUnlock()
	return trustedSpecRaw
}

func isTrustedRemoteAddr(remoteAddr string) bool {
	trimmed := strings.TrimSpace(remoteAddr)
	proxyMu.RLock()
	rule := trustedProxyRule
	proxyMu.RUnlock()

	if rule.trustNone {
		return false
	}
	if rule.trustAll || trimmed == "" {
		return true
	}

	host, _, err := net.SplitHostPort(trimmed)
	if err != nil {
		host = trimmed
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return false
	}
	if rule.allowLoop && ip.IsLoopback() {
		return true
	}
	if rule.allowPrivate && ip.IsPrivate() {
		return true
	}
	if _, ok := rule.ips[ip.String()]; ok {
		return true
	}
	for _, network := range rule.cidrs {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func extractCleanIP(raw string) string {
	val := strings.TrimSpace(raw)
	if val == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(val); err == nil && host != "" {
		return strings.TrimSpace(host)
	}
	return val
}

// IsTrustedProxy 判断当前请求的直连对端是否处于受信任反向代理边界内
func (ctx *Context) IsTrustedProxy() bool {
	if ctx == nil || ctx.Request == nil {
		return true
	}
	return isTrustedRemoteAddr(ctx.Request.RemoteAddr)
}

// ClientIP 安全提取客户端真实 IP（仅当直连对端为可信反代时才采信转发头，严防公网伪造请求头绕过限流）
func (ctx *Context) ClientIP() string {
	if ctx == nil {
		return ""
	}
	if ctx.IsTrustedProxy() && ctx.Request != nil {
		if eo := extractCleanIP(ctx.Request.Header.Get("EO-Connecting-IP")); eo != "" {
			return eo
		}
		if xff := ctx.Request.Header.Get("X-Forwarded-For"); xff != "" {
			for _, part := range strings.Split(xff, ",") {
				if ip := extractCleanIP(part); ip != "" {
					return ip
				}
			}
		}
		if xri := extractCleanIP(ctx.Request.Header.Get("X-Real-IP")); xri != "" {
			return xri
		}
	}
	if ctx.Request != nil {
		if ip := extractCleanIP(ctx.Request.RemoteAddr); ip != "" {
			return ip
		}
	}
	return ""
}

// IsHTTPS 判断当前请求是否处于安全 HTTPS 链路（含可信反代 X-Forwarded-Proto: https）
func (ctx *Context) IsHTTPS() bool {
	if ctx == nil || ctx.Request == nil {
		return false
	}
	if ctx.Request.TLS != nil || strings.EqualFold(ctx.Request.URL.Scheme, "https") {
		return true
	}
	if ctx.IsTrustedProxy() {
		proto := strings.TrimSpace(ctx.Request.Header.Get("X-Forwarded-Proto"))
		if idx := strings.IndexByte(proto, ','); idx >= 0 {
			proto = strings.TrimSpace(proto[:idx])
		}
		if strings.EqualFold(proto, "https") {
			return true
		}
	}
	return false
}

// SetHttpOnlyCookie 使用标准库 http.SetCookie 下发加固版 HttpOnly 会话 Cookie（强制 Path=/, SameSite=Lax, 自动识别 Secure）
func (ctx *Context) SetHttpOnlyCookie(name, value string, maxAgeSec int) {
	if ctx == nil || ctx.ResponseWriter == nil || strings.TrimSpace(name) == "" {
		return
	}
	ck := &http.Cookie{
		Name:     sanitizeName(strings.TrimSpace(name)),
		Value:    sanitizeValue(value),
		Path:     "/",
		HttpOnly: true,
		Secure:   ctx.IsHTTPS(),
		SameSite: http.SameSiteLaxMode,
	}
	if maxAgeSec > 0 {
		ck.MaxAge = maxAgeSec
		ck.Expires = time.Now().Add(time.Duration(maxAgeSec) * time.Second).UTC()
	}
	http.SetCookie(ctx.ResponseWriter, ck)
}

// ClearHttpOnlyCookie 清除指定名称的 HttpOnly 会话 Cookie（保持与下发时完全对称的安全属性）
func (ctx *Context) ClearHttpOnlyCookie(name string) {
	if ctx == nil || ctx.ResponseWriter == nil || strings.TrimSpace(name) == "" {
		return
	}
	ck := &http.Cookie{
		Name:     sanitizeName(strings.TrimSpace(name)),
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
		HttpOnly: true,
		Secure:   ctx.IsHTTPS(),
		SameSite: http.SameSiteLaxMode,
	}
	http.SetCookie(ctx.ResponseWriter, ck)
}
