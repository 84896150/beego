package dbbridge

import (
	"database/sql"
	"sync"
)

var (
	mu            sync.RWMutex
	defaultDB     *sql.DB
	defaultDriver string
	listeners     []func(db *sql.DB, driverName string)
)

// RegisterDefault 登记全局默认数据库连接池与驱动名称（由 client/orm 在注册 "default" 库时自动调用）
func RegisterDefault(db *sql.DB, driverName string) {
	mu.Lock()
	defaultDB = db
	defaultDriver = driverName
	cbs := make([]func(db *sql.DB, driverName string), len(listeners))
	copy(cbs, listeners)
	mu.Unlock()

	for _, cb := range cbs {
		if cb != nil {
			cb(db, driverName)
		}
	}
}

// Default 获取已登记的全局默认数据库连接池与驱动名称
func Default() (*sql.DB, string) {
	mu.RLock()
	defer mu.RUnlock()
	return defaultDB, defaultDriver
}

// ResetDefault 清空默认数据库桥接状态（仅供单元测试隔离使用）
func ResetDefault() {
	RegisterDefault(nil, "")
}

// OnRegisterDefault 注册默认数据库变更监听器
func OnRegisterDefault(fn func(db *sql.DB, driverName string)) {
	if fn == nil {
		return
	}
	mu.Lock()
	listeners = append(listeners, fn)
	db, drv := defaultDB, defaultDriver
	mu.Unlock()

	if db != nil {
		fn(db, drv)
	}
}
