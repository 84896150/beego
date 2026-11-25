package web

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/beego/beego/v2/core/dbbridge"
	"github.com/beego/beego/v2/core/logs"
	"github.com/beego/beego/v2/server/web/context"
)

// SessionStatus 表示 Web 身份会话的核验结果状态
type SessionStatus int

const (
	// SessionMissing 请求未携带会话 Cookie
	SessionMissing SessionStatus = iota
	// SessionValid 会话有效
	SessionValid
	// SessionInvalid 会话无效、已退出或已被管理员强制下线
	SessionInvalid
	// SessionExpired 会话已自然过期
	SessionExpired
	// SessionKicked 账号已在其他设备登录（被顶号互踢）
	SessionKicked
)

const (
	recordStatusActive = 1
	recordStatusKicked = 2
	sessionCtxSIDKey   = "__beego_identity_sid__"
)

var validTableIdentRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// UserSession 表示与具体业务用户 UID 强绑定的 Web 会话实体
type UserSession struct {
	SID       string         `json:"sid"`
	UID       int64          `json:"uid"`
	Data      map[string]any `json:"data,omitempty"`
	ClientIP  string         `json:"client_ip,omitempty"`
	CreatedAt int64          `json:"created_at"`
	ExpiresAt int64          `json:"expires_at"`
}

// GetString 安全读取会话附加字段中的字符串值
func (s *UserSession) GetString(key string) string {
	if s == nil || s.Data == nil {
		return ""
	}
	v, ok := s.Data[key]
	if !ok || v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	default:
		return fmt.Sprint(val)
	}
}

// GetInt64 安全读取会话附加字段中的 64 位整型值（兼容 JSON 反序列化后的 float64）
func (s *UserSession) GetInt64(key string) int64 {
	if s == nil || s.Data == nil {
		return 0
	}
	v, ok := s.Data[key]
	if !ok || v == nil {
		return 0
	}
	switch val := v.(type) {
	case int64:
		return val
	case int:
		return int64(val)
	case float64:
		return int64(val)
	case json.Number:
		n, _ := val.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(val), 10, 64)
		return n
	default:
		return 0
	}
}

// GetBool 安全读取会话附加字段中的布尔值
func (s *UserSession) GetBool(key string) bool {
	if s == nil || s.Data == nil {
		return false
	}
	v, ok := s.Data[key]
	if !ok || v == nil {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		b, _ := strconv.ParseBool(strings.TrimSpace(val))
		return b
	default:
		return false
	}
}

type sessionRecord struct {
	sid       string
	uid       int64
	data      map[string]any
	status    int
	clientIP  string
	createdAt int64
	expiresAt int64
}

func (r *sessionRecord) toUserSession() *UserSession {
	var copied map[string]any
	if len(r.data) > 0 {
		copied = make(map[string]any, len(r.data))
		for k, v := range r.data {
			copied[k] = v
		}
	}
	createdSec := r.createdAt
	if createdSec > 1e12 {
		createdSec = createdSec / 1e9
	}
	return &UserSession{
		SID:       r.sid,
		UID:       r.uid,
		Data:      copied,
		ClientIP:  r.clientIP,
		CreatedAt: createdSec,
		ExpiresAt: r.expiresAt,
	}
}

type identitySessionEngine struct {
	mu          sync.RWMutex
	records     map[string]*sessionRecord
	userIndex   map[int64][]string
	lastOrderNs int64
	db          *sql.DB
	driverName  string
	tableReady  string
	gcStarted   bool
}

var (
	defaultSessionEngineOnce sync.Once
	defaultSessionEngine     *identitySessionEngine
)

func getIdentitySessionEngine() *identitySessionEngine {
	defaultSessionEngineOnce.Do(func() {
		eng := &identitySessionEngine{
			records:   make(map[string]*sessionRecord),
			userIndex: make(map[int64][]string),
		}
		dbbridge.OnRegisterDefault(func(db *sql.DB, driverName string) {
			eng.attachDB(db, driverName)
		})
		eng.startBackgroundGC()
		defaultSessionEngine = eng
	})
	return defaultSessionEngine
}

func (e *identitySessionEngine) startBackgroundGC() {
	e.mu.Lock()
	if e.gcStarted {
		e.mu.Unlock()
		return
	}
	e.gcStarted = true
	e.mu.Unlock()

	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			e.PurgeExpired()
		}
	}()
}

func (e *identitySessionEngine) attachDB(db *sql.DB, driverName string) {
	e.mu.Lock()
	changed := e.db != db || e.driverName != driverName
	e.db = db
	e.driverName = strings.ToLower(strings.TrimSpace(driverName))
	if changed {
		e.tableReady = ""
		e.records = make(map[string]*sessionRecord)
		e.userIndex = make(map[int64][]string)
	}
	e.mu.Unlock()

	if db != nil {
		e.ensureTable()
	}
}

func resolveSessionTableName() string {
	name := "beego_session"
	if BConfig != nil && strings.TrimSpace(BConfig.WebConfig.Session.SessionTableName) != "" {
		candidate := strings.TrimSpace(BConfig.WebConfig.Session.SessionTableName)
		if validTableIdentRegex.MatchString(candidate) {
			name = candidate
		}
	}
	return name
}

// ResolveSessionCookieName 动态解析当前应用的隔离会话 Cookie 名（默认 {appname}_session）
func ResolveSessionCookieName() string {
	if BConfig != nil && strings.TrimSpace(BConfig.WebConfig.Session.SessionCookieName) != "" {
		return strings.TrimSpace(BConfig.WebConfig.Session.SessionCookieName)
	}
	if AppConfig != nil {
		if cName, err := AppConfig.String("sessionCookieName"); err == nil && strings.TrimSpace(cName) != "" {
			return strings.TrimSpace(cName)
		}
	}
	if BConfig != nil {
		sName := strings.TrimSpace(BConfig.WebConfig.Session.SessionName)
		if sName != "" && !strings.EqualFold(sName, "beegosessionID") {
			return sName
		}
	}

	appName := ""
	if AppConfig != nil {
		appName, _ = AppConfig.String("appname")
	}
	if strings.TrimSpace(appName) == "" && BConfig != nil && strings.TrimSpace(BConfig.AppName) != "" && BConfig.AppName != "beego" {
		appName = BConfig.AppName
	}
	if strings.TrimSpace(appName) == "" {
		appName = strings.TrimSpace(os.Getenv("APP_NAME"))
	}
	if strings.TrimSpace(appName) == "" {
		appName = "beego"
	}

	safe := strings.ReplaceAll(strings.TrimSpace(appName), "-", "_")
	if strings.HasSuffix(safe, "_session") || strings.HasSuffix(safe, "_token") {
		return safe
	}
	return safe + "_session"
}

// ResolveSessionTTLSeconds 解析会话单次有效期秒数（默认 86400 秒，兼容 sessionTTLHours）
func ResolveSessionTTLSeconds() int64 {
	if AppConfig != nil {
		if v, err := AppConfig.Int64("sessionTTL"); err == nil && v > 0 {
			return v
		}
		if h, err := AppConfig.Int64("sessionTTLHours"); err == nil && h > 0 {
			return h * 3600
		}
	}
	if BConfig != nil && BConfig.WebConfig.Session.SessionTTL > 0 {
		return BConfig.WebConfig.Session.SessionTTL
	}
	return 86400
}

func resolveSessionAutoRefresh() bool {
	if AppConfig != nil {
		if v, err := AppConfig.Bool("sessionAutoRefresh"); err == nil {
			return v
		}
	}
	if BConfig != nil {
		return BConfig.WebConfig.Session.SessionAutoRefresh
	}
	return true
}

func resolveSessionMaxDevices() int {
	if AppConfig != nil {
		if v, err := AppConfig.Int("sessionMaxDevices"); err == nil {
			return v
		}
	}
	if BConfig != nil {
		return BConfig.WebConfig.Session.SessionMaxDevices
	}
	return 1
}

func (e *identitySessionEngine) rebindPlaceholders(query string, driver string) string {
	if driver != "postgres" && driver != "pgx" {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + 16)
	idx := 1
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(idx))
			idx++
		} else {
			b.WriteByte(query[i])
		}
	}
	return b.String()
}

func (e *identitySessionEngine) ensureTable() (*sql.DB, string, string) {
	table := resolveSessionTableName()
	e.mu.RLock()
	db := e.db
	drv := e.driverName
	ready := e.tableReady == table
	e.mu.RUnlock()

	if db == nil {
		if bridgeDB, bridgeDrv := dbbridge.Default(); bridgeDB != nil {
			e.attachDB(bridgeDB, bridgeDrv)
			e.mu.RLock()
			db = e.db
			drv = e.driverName
			ready = e.tableReady == table
			e.mu.RUnlock()
		}
	}
	if db == nil || ready {
		return db, drv, table
	}

	createTableSQL := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		sid VARCHAR(64) PRIMARY KEY,
		uid BIGINT NOT NULL,
		data_json TEXT NOT NULL,
		status SMALLINT NOT NULL DEFAULT 1,
		client_ip VARCHAR(64) NOT NULL DEFAULT '',
		created_at BIGINT NOT NULL,
		expires_at BIGINT NOT NULL
	)`, table)
	if _, err := db.Exec(createTableSQL); err != nil {
		logs.Warn("identity session: create table %s failed: %v", table, err)
		return db, drv, table
	}

	idxUID := fmt.Sprintf("CREATE INDEX idx_%s_uid ON %s (uid)", table, table)
	idxExp := fmt.Sprintf("CREATE INDEX idx_%s_expires ON %s (expires_at)", table, table)
	if drv != "mysql" {
		idxUID = fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_uid ON %s (uid)", table, table)
		idxExp = fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_expires ON %s (expires_at)", table, table)
	}
	_, _ = db.Exec(idxUID)
	_, _ = db.Exec(idxExp)

	e.mu.Lock()
	if e.db == db {
		e.tableReady = table
	}
	e.mu.Unlock()
	return db, drv, table
}

func generateSecureSID() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func (e *identitySessionEngine) addIndexLocked(uid int64, sid string) {
	list := e.userIndex[uid]
	if !slices.Contains(list, sid) {
		e.userIndex[uid] = append(list, sid)
	}
}

func (e *identitySessionEngine) removeRecordLocked(sid string) {
	rec, ok := e.records[sid]
	if !ok {
		return
	}
	delete(e.records, sid)
	if list, exists := e.userIndex[rec.uid]; exists {
		filtered := list[:0]
		for _, item := range list {
			if item != sid {
				filtered = append(filtered, item)
			}
		}
		if len(filtered) == 0 {
			delete(e.userIndex, rec.uid)
		} else {
			e.userIndex[rec.uid] = filtered
		}
	}
}

func (e *identitySessionEngine) syncUserActiveFromDB(db *sql.DB, drv, table string, uid int64, now int64) {
	if db == nil {
		return
	}
	q := e.rebindPlaceholders(
		fmt.Sprintf("SELECT sid, uid, data_json, status, client_ip, created_at, expires_at FROM %s WHERE uid = ? AND expires_at > ?", table),
		drv,
	)
	rows, err := db.Query(q, uid, now)
	if err != nil {
		return
	}
	defer rows.Close()

	var loaded []*sessionRecord
	for rows.Next() {
		var rec sessionRecord
		var rawJSON string
		if err := rows.Scan(&rec.sid, &rec.uid, &rawJSON, &rec.status, &rec.clientIP, &rec.createdAt, &rec.expiresAt); err != nil {
			continue
		}
		if rawJSON != "" && rawJSON != "{}" {
			_ = json.Unmarshal([]byte(rawJSON), &rec.data)
		}
		loaded = append(loaded, &rec)
	}

	if len(loaded) == 0 {
		return
	}
	e.mu.Lock()
	for _, rec := range loaded {
		if _, exists := e.records[rec.sid]; !exists {
			e.records[rec.sid] = rec
			e.addIndexLocked(rec.uid, rec.sid)
		}
	}
	e.mu.Unlock()
}

func (e *identitySessionEngine) loadBySIDFromDB(db *sql.DB, drv, table, sid string) *sessionRecord {
	if db == nil || sid == "" {
		return nil
	}
	q := e.rebindPlaceholders(
		fmt.Sprintf("SELECT sid, uid, data_json, status, client_ip, created_at, expires_at FROM %s WHERE sid = ?", table),
		drv,
	)
	var rec sessionRecord
	var rawJSON string
	err := db.QueryRow(q, sid).Scan(&rec.sid, &rec.uid, &rawJSON, &rec.status, &rec.clientIP, &rec.createdAt, &rec.expiresAt)
	if err != nil {
		return nil
	}
	if rawJSON != "" && rawJSON != "{}" {
		_ = json.Unmarshal([]byte(rawJSON), &rec.data)
	}
	e.mu.Lock()
	e.records[rec.sid] = &rec
	e.addIndexLocked(rec.uid, rec.sid)
	e.mu.Unlock()
	return &rec
}

// Login 为指定用户创建新会话（内置单设备互踢 / N 设备限额控制与数据库落盘）
func (e *identitySessionEngine) Login(ctx *context.Context, uid int64, data map[string]any) (*UserSession, error) {
	if uid <= 0 {
		return nil, errors.New("session: uid must be positive")
	}
	sid, err := generateSecureSID()
	if err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	ttl := ResolveSessionTTLSeconds()
	expiresAt := now + ttl
	maxDevices := resolveSessionMaxDevices()

	clientIP := ""
	if ctx != nil {
		clientIP = ctx.ClientIP()
	}

	var copiedData map[string]any
	if len(data) > 0 {
		copiedData = make(map[string]any, len(data))
		for k, v := range data {
			copiedData[k] = v
		}
	}
	dataBytes := []byte("{}")
	if len(copiedData) > 0 {
		if b, mErr := json.Marshal(copiedData); mErr == nil {
			dataBytes = b
		}
	}

	db, drv, table := e.ensureTable()
	if db != nil && maxDevices > 0 {
		e.syncUserActiveFromDB(db, drv, table, uid, now)
	}

	var kickedSIDs []string
	rec := &sessionRecord{
		sid:       sid,
		uid:       uid,
		data:      copiedData,
		status:    recordStatusActive,
		clientIP:  clientIP,
		expiresAt: expiresAt,
	}

	e.mu.Lock()
	createdOrder := time.Now().UnixNano()
	if createdOrder <= e.lastOrderNs {
		createdOrder = e.lastOrderNs + 1
	}
	e.lastOrderNs = createdOrder
	rec.createdAt = createdOrder

	if maxDevices > 0 {
		var active []*sessionRecord
		for _, oldSID := range e.userIndex[uid] {
			if oldRec, ok := e.records[oldSID]; ok {
				if oldRec.expiresAt <= now {
					e.removeRecordLocked(oldSID)
					continue
				}
				if oldRec.status == recordStatusActive {
					active = append(active, oldRec)
				}
			}
		}
		slices.SortFunc(active, func(a, b *sessionRecord) int {
			if a.createdAt != b.createdAt {
				if a.createdAt < b.createdAt {
					return -1
				}
				return 1
			}
			return strings.Compare(a.sid, b.sid)
		})
		needKick := len(active) - maxDevices + 1
		for i := 0; i < needKick && i < len(active); i++ {
			active[i].status = recordStatusKicked
			kickedSIDs = append(kickedSIDs, active[i].sid)
		}
	}
	e.records[sid] = rec
	e.addIndexLocked(uid, sid)
	e.mu.Unlock()

	if db != nil {
		for _, ksid := range kickedSIDs {
			uq := e.rebindPlaceholders(fmt.Sprintf("UPDATE %s SET status = ? WHERE sid = ?", table), drv)
			_, _ = db.Exec(uq, recordStatusKicked, ksid)
		}
		iq := e.rebindPlaceholders(
			fmt.Sprintf("INSERT INTO %s (sid, uid, data_json, status, client_ip, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)", table),
			drv,
		)
		if _, dbErr := db.Exec(iq, sid, uid, string(dataBytes), recordStatusActive, clientIP, rec.createdAt, expiresAt); dbErr != nil {
			e.mu.Lock()
			e.removeRecordLocked(sid)
			e.mu.Unlock()
			return nil, fmt.Errorf("session: persist failed: %w", dbErr)
		}
	}

	if ctx != nil {
		ctx.SetHttpOnlyCookie(ResolveSessionCookieName(), sid, int(ttl))
		if ctx.Input != nil {
			ctx.Input.SetData(sessionCtxSIDKey, sid)
		}
		uname := ""
		if copiedData != nil {
			if u, ok := copiedData["username"].(string); ok {
				uname = u
			}
		}
		ClearLoginFailForContext(ctx, uname)
	}

	return rec.toUserSession(), nil
}

func extractRequestSessionToken(ctx *context.Context) string {
	if ctx == nil {
		return ""
	}
	if ctx.Input != nil {
		if v := ctx.Input.GetData(sessionCtxSIDKey); v != nil {
			if s, ok := v.(string); ok {
				return strings.TrimSpace(s)
			}
		}
	}
	if ctx.Request == nil {
		return ""
	}
	return strings.TrimSpace(ctx.GetCookie(ResolveSessionCookieName()))
}

// Verify 核验当前请求的会话 Cookie，返回会话实体与精确状态（含被顶号识别与半程滑动续期）
func (e *identitySessionEngine) Verify(ctx *context.Context) (*UserSession, SessionStatus) {
	if ctx == nil {
		return nil, SessionMissing
	}
	cookieName := ResolveSessionCookieName()
	sid := extractRequestSessionToken(ctx)
	if sid == "" {
		return nil, SessionMissing
	}

	db, drv, table := e.ensureTable()

	e.mu.RLock()
	rec := e.records[sid]
	e.mu.RUnlock()

	if rec == nil && db != nil {
		rec = e.loadBySIDFromDB(db, drv, table, sid)
	}
	if rec == nil {
		ctx.ClearHttpOnlyCookie(cookieName)
		return nil, SessionInvalid
	}

	now := time.Now().Unix()

	e.mu.RLock()
	status := rec.status
	expiresAt := rec.expiresAt
	snap := rec.toUserSession()
	e.mu.RUnlock()

	if status == recordStatusKicked {
		e.DeleteBySID(sid)
		ctx.ClearHttpOnlyCookie(cookieName)
		return nil, SessionKicked
	}

	if now >= expiresAt {
		e.DeleteBySID(sid)
		ctx.ClearHttpOnlyCookie(cookieName)
		return nil, SessionExpired
	}

	if resolveSessionAutoRefresh() {
		ttl := ResolveSessionTTLSeconds()
		if ttl > 0 && (expiresAt-now) <= ttl/2 {
			newExp := now + ttl
			e.mu.Lock()
			if cur, ok := e.records[sid]; ok && cur.status == recordStatusActive {
				cur.expiresAt = newExp
				snap.ExpiresAt = newExp
			}
			e.mu.Unlock()

			if db != nil {
				uq := e.rebindPlaceholders(
					fmt.Sprintf("UPDATE %s SET expires_at = ? WHERE sid = ? AND status = ?", table),
					drv,
				)
				_, _ = db.Exec(uq, newExp, sid, recordStatusActive)
			}
			ctx.SetHttpOnlyCookie(cookieName, sid, int(ttl))
		}
	}

	return snap, SessionValid
}

// DeleteBySID 删除单个会话记录
func (e *identitySessionEngine) DeleteBySID(sid string) {
	if sid == "" {
		return
	}
	e.mu.Lock()
	e.removeRecordLocked(sid)
	e.mu.Unlock()

	if db, drv, table := e.ensureTable(); db != nil {
		dq := e.rebindPlaceholders(fmt.Sprintf("DELETE FROM %s WHERE sid = ?", table), drv)
		_, _ = db.Exec(dq, sid)
	}
}

// Logout 退出当前设备的会话并清除浏览器 Cookie
func (e *identitySessionEngine) Logout(ctx *context.Context) {
	if ctx == nil {
		return
	}
	cookieName := ResolveSessionCookieName()
	if sid := extractRequestSessionToken(ctx); sid != "" {
		e.DeleteBySID(sid)
	}
	if ctx.Input != nil {
		ctx.Input.SetData(sessionCtxSIDKey, "")
	}
	ctx.ClearHttpOnlyCookie(cookieName)
}

// KickUser 立即作废指定用户名下的所有在线设备会话（秒级封号 / 改密全端强踢）
func (e *identitySessionEngine) KickUser(uid int64) error {
	if uid <= 0 {
		return nil
	}
	e.mu.Lock()
	sids := append([]string(nil), e.userIndex[uid]...)
	for _, sid := range sids {
		delete(e.records, sid)
	}
	delete(e.userIndex, uid)
	e.mu.Unlock()

	if db, drv, table := e.ensureTable(); db != nil {
		dq := e.rebindPlaceholders(fmt.Sprintf("DELETE FROM %s WHERE uid = ?", table), drv)
		if _, err := db.Exec(dq, uid); err != nil {
			return err
		}
	}
	return nil
}

// PurgeExpired 清理已过期的内存与数据库会话记录
func (e *identitySessionEngine) PurgeExpired() {
	now := time.Now().Unix()
	e.mu.Lock()
	for sid, rec := range e.records {
		if now >= rec.expiresAt {
			e.removeRecordLocked(sid)
		}
	}
	e.mu.Unlock()

	if db, drv, table := e.ensureTable(); db != nil {
		dq := e.rebindPlaceholders(fmt.Sprintf("DELETE FROM %s WHERE expires_at <= ?", table), drv)
		_, _ = db.Exec(dq, now)
	}
}

// Reset 清空全部会话状态（供单元测试隔离使用）
func (e *identitySessionEngine) Reset() {
	e.mu.Lock()
	e.records = make(map[string]*sessionRecord)
	e.userIndex = make(map[int64][]string)
	e.mu.Unlock()

	if db, _, table := e.ensureTable(); db != nil {
		_, _ = db.Exec(fmt.Sprintf("DELETE FROM %s", table))
	}
}

// KickUserSessions 秒级强制下线指定用户的所有登录设备（适用于后台封号、到期停用、修改密码等）
func KickUserSessions(uid int64) error {
	return getIdentitySessionEngine().KickUser(uid)
}

// ResetSessions 清空所有会话记录（供单元测试隔离重置使用）
func ResetSessions() {
	getIdentitySessionEngine().Reset()
}
