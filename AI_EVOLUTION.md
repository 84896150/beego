# Beego v2.4.0 AI-Native 演进版说明（Go 1.27.3）

> **仓库地址**：`https://github.com/84896150/beego`  
> **模块路径**：`github.com/beego/beego/v2`（保持官方路径零摩擦兼容，下游项目仅需 `go.mod` 一行 `replace` 即可切换）  
> **工具链基线**：`Go 1.27.3` | **版本标签**：`v2.4.0`

---

## 一、 演进设计原则

本分支保留官方 Beego v2 全部开箱即用子包与经典 MVC 契约，围绕 **“消除 AI 编程二选一歧义 + 内核级填平历史暗坑 + Go 1.27 现代化底座”** 进行手术式升级：

1. **单一正确入口（消灭二选一）**：所有易诱发连接泄漏、生命周期跳过或反射恐慌的旧导出 API 一律物理切除或包内私有化，让错误写法在 `go build` 编译阶段即被阻断。
2. **默认安全化**：`CopyRequestBody = true` 与 `EnableErrorsRender = false` 内置为框架默认配置，禁止默认向外部吐出 HTML 堆栈页。
3. **编译期 & 启动期左移防线**：通过泛型 ORM `orm.Query[T]` 在编译期锁定模型与切片类型，配合 `orm.SetStrictModelGuard(true)` 在启动第 0 秒拦截 32 位 ID 截断、缺少 `json` 标签与敏感凭据字段泄露风险。

---

## 二、 物理移除的“二选一”反模式对照表

| 领域 | 已移除/私有化的旧入口 | 唯一标准写法 | 收益与防线 |
| :--- | :--- | :--- | :--- |
| **请求中断** | `c.StopRun()` | 写入响应（如 `ServeJSON()`）后显式 `return` | 彻底消灭 panic 跳过 `Finish()` 与监控统计；内部 `CustomAbort` 触发 `ErrAbort` 亦已加兜底保护确保执行 `Finish()` |
| **事务管理** | `o.Begin*()`、`tx.Commit()`、`tx.Rollback()`、`tx.RollbackUnlessCommit()` | `o.DoTx(func(ctx, txOrm) error { ... })` | 编译期物理封杀手写事务漏 `Rollback` 导致的连接池泄漏 |
| **ORM 查询** | `o.QueryTable(...)`、`QueryTableWithCtx`、`QueryM2MWithCtx` | `orm.Query[models.Article](o)...`、`orm.GetByID[models.Article](o, id)` | 消除字符串表名拼错与切片漏传 `&` panic；`All()` 返回非 `nil` 空切片（JSON 恒为 `[]`） |
| **连接池配置** | `orm.SetMaxIdleConns(...)`、`orm.SetMaxOpenConns(...)` | `orm.RegisterDataBase(..., orm.MaxIdleConnections(n), orm.MaxOpenConnections(n))` | 消除已废弃全局 API 时序隐患 |
| **隐式路由** | `Include` / `NSInclude` / `AutoRouter` / `NSAutoRouter` / `AutoPrefix` / `NSAutoPrefix` / `AddAuto` / `AddAutoPrefix` | 显式 `web.Router` / `web.NSNamespace` + `web.NSRouter` | 彻底消除注释路由与反射自动路由导致的黑盒 404 |

---

## 三、 核心新增与增强能力速查

### 1. 编译期类型安全泛型 ORM（`client/orm`）

```go
// 单主键强类型查询（自动识别模型 PK 字段名，默认 Id）
user, err := orm.GetByIDWithCtx[models.User](ctx, o, id)

// 一行安全分页（内置先 Count 判零短路，且 Count() 底层已修复 Limit/Offset 污染截断暗坑）
items, total, err := orm.Query[models.Article](o).
    WithCtx(ctx).
    Filter("status", "published").
    OrderBy("-id").
    Paginate(page, perPage)

// Exist 保留底层数据库 error，不再静默吞错
exists, err := orm.Query[models.User](o).Filter("username", name).Exist()
```

### 2. 启动期模型规范自检器（`orm.SetStrictModelGuard`）

在 `main.go` 或 `models/init.go` 执行 `orm.RegisterModel(...)` 之前开启：

```go
orm.SetStrictModelGuard(true)
```

自检器将在注册模型时立即校验以下规则（不合规直接 `panic` 并给出精确字段修复指引）：
- 主键及 `*Id` / `*ID` 整数字段必须为 `int64`（禁止 `int` / `int32` / `uint` / `uint32`）；
- 所有导出持久化字段（排除 `orm:"-"`）必须显式声明 `json` 标签；
- 字段名包含 `password`、`secret`、`sessiontoken`（忽略大小写与下划线）时，必须设置 `json:"-"`。

### 3. 智能自适应入参绑定与强类型路由提取（`server/web`）

- **`c.Bind(&dto)`**：自动识别 `Content-Type` 参数后缀（如 `; charset=utf-8`）；未携带 `Content-Type` 时，若请求体以 `{` 或 `[` 开头走 JSON 解析，否则（含 GET、空 Body POST/DELETE）统一走 `BindForm` 解析 Query/Form，彻底终结 `unexpected end of JSON input`；原生支持 `multipart/form-data`；`protobuf` 类型不匹配返回明确 error 而非裸 panic。
- **`c.ParamInt64(":id")`**：直接解析 64 位整数路径参数，缺参或非法格式返回清晰错误。

### 4. `core/logs` 幂等关闭与标准库 `log/slog` 桥接

- **幂等安全关闭**：`BeeLogger.Reset()` 与 `Close()` 已增加并发锁与关闭态保护，多次调用不再触发 `close of closed channel` 恐慌。
- **原生 `slog` 适配器**：
  ```go
  _ = logs.SetLogger(logs.AdapterSlog, `{"format":"json","level":7}`)
  ```
- **Context 链路结构化日志**（自动提取 `WithRequestID` 或 `"request_id"` / `"X-Request-Id"`）：
  ```go
  ctx = logs.WithRequestID(ctx, reqID)
  logs.InfoCtx(ctx, "order paid", "order_id", orderID, "amount", amount)
  // 或直接获取标准库 *slog.Logger 句柄：
  slogger := logs.AsSlog()
  ```

### 5. `task` 后台手动触发修复

- 修复管理端手动执行任务（`runTaskCommand.Execute`）未更新 `Prev` 执行时间导致监控页面 `Last Time` 停滞的问题。

---

## 四、 业务项目接入方式

保持现有业务代码中的所有官方包导入路径（`"github.com/beego/beego/v2/..."`）完全不变，仅在业务项目 `go.mod` 末尾添加一行版本映射即可：

```go
replace github.com/beego/beego/v2 => github.com/84896150/beego/v2 v2.4.0
```

本地跨仓库联合调试时，可在工作区根目录创建 `go.work`（避免污染业务项目 `go.mod`）：

```go
go 1.27.3

use (
    ./beego
    ./your-app
)
```
