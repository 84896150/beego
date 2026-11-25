# Beego v2.5.0 AI-Native 演进版说明（Go 1.27.3）

> **仓库地址**：`https://github.com/84896150/beego`  
> **模块路径**：`github.com/beego/beego/v2`（保持官方路径零摩擦兼容，下游项目仅需 `go.mod` 一行 `replace` 即可切换）  
> **工具链基线**：`Go 1.27.3` | **版本标签**：`v2.5.0`

---

## 一、 演进设计原则

本分支保留官方 Beego v2 全部开箱即用子包与经典 MVC 契约，围绕 **“消除 AI 编程二选一歧义 + 内核级填平历史暗坑 + Go 1.27 标准库全面换芯”** 进行手术式升级：

1. **单一正确入口（消灭二选一）**：所有易诱发连接泄漏、生命周期跳过或反射恐慌的旧导出 API 一律物理切除或包内私有化，让错误写法在 `go build` 编译阶段即被阻断。
2. **标准库换芯去轮子化**：用 Go 1.27 官方标准库（`embed.FS` / `io/fs`、原生 `"uuid"`、`log/slog`、`crypto/rand`、`math/rand/v2`、`slices`、`sync.Pool`、`http.Server.Shutdown` + `signal.NotifyContext`、`errors.Is` 错误链）全面替代历史过时轮子与冗余第三方库。
3. **默认安全化**：`CopyRequestBody = true` 与 `EnableErrorsRender = false` 内置为框架默认配置，禁止默认向外部吐出 HTML 堆栈页。
4. **编译期 & 启动期左移防线**：通过泛型 ORM `orm.Query[T]` 在编译期锁定模型与切片类型，配合 `orm.SetStrictModelGuard(true)` 在启动第 0 秒拦截 32 位 ID 截断、缺少 `json` 标签与敏感凭据字段泄露风险。

---

## 二、 物理移除的“二选一”与过时依赖清单

### 1. 已移除/私有化的旧入口

| 领域 | 已移除/私有化的旧入口 | 唯一标准写法 | 收益与防线 |
| :--- | :--- | :--- | :--- |
| **请求中断** | `c.StopRun()` | 写入响应（如 `ServeJSON()`）后显式 `return` | 彻底消灭 panic 跳过 `Finish()` 与监控统计；内部 `CustomAbort` 触发 `ErrAbort` 亦已加兜底保护确保执行 `Finish()` |
| **事务管理** | `o.Begin*()`、`tx.Commit()`、`tx.Rollback()`、`tx.RollbackUnlessCommit()` | `o.DoTx(func(ctx, txOrm) error { ... })` | 编译期物理封杀手写事务漏 `Rollback` 导致的连接池泄漏 |
| **ORM 查询** | `o.QueryTable(...)`、`QueryTableWithCtx`、`QueryM2MWithCtx` | `orm.Query[models.Article](o)...`、`orm.GetByID[models.Article](o, id)` | 消除字符串表名拼错与切片漏传 `&` panic；`All()` 返回非 `nil` 空切片（JSON 恒为 `[]`） |
| **连接池配置** | `orm.SetMaxIdleConns(...)`、`orm.SetMaxOpenConns(...)` | `orm.RegisterDataBase(..., orm.MaxIdleConnections(n), orm.MaxOpenConnections(n))` | 消除已废弃全局 API 时序隐患 |
| **隐式路由** | `Include` / `NSInclude` / `AutoRouter` / `NSAutoRouter` / `AutoPrefix` / `NSAutoPrefix` / `AddAuto` / `AddAutoPrefix` | 显式 `web.Router` / `web.NSNamespace` + `web.NSRouter` | 彻底消除注释路由与反射自动路由导致的黑盒 404 |
| **并发集合** | `utils.BeeMap` / `utils.NewBeeMap` | Go 标准库 `sync.Map` | 消除历史废包二选一 |

### 2. 已剥离的冗余第三方依赖与历史包

| 原依赖 / 模块 | 标准库替代方案 | 收益 |
| :--- | :--- | :--- |
| `github.com/elazarl/go-bindata-assetfs` & `io/ioutil` | Go 原生 `embed.FS` + `io/fs`（`web.SetViewsFS` / `web.SetStaticFS`） | 支持单二进制编译部署 `views/*.tpl` 与 `static/pages/*.json`，修复 Windows 反斜杠路径问题 |
| `github.com/google/uuid`（直接依赖） | Go 1.27 原生标准库 `"uuid"`（`uuid.NewV7()`） | 零外部依赖生成时间单调递增 UUIDv7，并在路由层自动注入 `X-Request-Id` 与 Context |
| `github.com/valyala/bytebufferpool` | Go 标准库 `sync.Pool` + `*bytes.Buffer` | 内置 64KB 上限防大 SQL 缓冲长期驻留内存 |
| `github.com/shiena/ansicolor` | Go 标准库 `os.Stdout` + `log/slog` | 清除十年老旧终端包装，现代控制台原生兼容 ANSI |
| `server/web/grace`（Fork 式热启） | `(*http.Server).Shutdown(ctx)` + `signal.NotifyContext` | 跨平台（Windows/Linux 一致）优雅排空在途请求并触发 `BeforeShutdown` |
| `math/rand.Seed` + 有偏取模 | 密码学安全 `crypto/rand` 拒绝采样 + `math/rand/v2` + `slices` | 消除随机串取模偏差与高并发全局锁竞争 |

---

## 三、 核心新增与增强能力速查

### 1. 编译期类型安全泛型 ORM & 标准库错误链（`client/orm`）

```go
// 单主键强类型查询（自动识别模型 PK 字段名，默认 Id）
// err 同时满足 errors.Is(err, orm.ErrNoRows) 和 errors.Is(err, sql.ErrNoRows)
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

```go
orm.SetStrictModelGuard(true)
```

在 `RegisterModel` 阶段拦截：
- 主键及 `*Id` / `*ID` 整数字段非 `int64`（禁止 `int` / `int32` / `uint` / `uint32`）；
- 导出持久化字段缺失显式 `json` 标签；
- 敏感字段（包含 `password`、`secret`、`sessiontoken`）未设置 `json:"-"`。

### 3. 单二进制嵌入式模板与静态托管 + 优雅停机（`server/web`）

```go
//go:embed views static
var embeddedAssets embed.FS

// 单二进制内嵌模板与 amis 静态资源挂载
web.SetViewsFS(embeddedAssets, "views")
staticSub, _ := fs.Sub(embeddedAssets, "static")
web.SetStaticFS("/static", staticSub)

// 跨平台优雅停机（Graceful=true 自动启用 signal.NotifyContext + GracefulTimeout）
web.BConfig.Listen.Graceful = true
web.BConfig.Listen.GracefulTimeout = 30 * time.Second
```

### 4. 智能自适应入参绑定、强类型路由与自动 `X-Request-Id`

- **`c.Bind(&dto)`**：自动识别 `Content-Type` 后缀；无 `Content-Type` 时按 Body 首字符或方法智能分派 JSON / Query-Form，彻底终结 `unexpected end of JSON input`；原生支持 `multipart/form-data`。
- **`c.ParamInt64(":id")`**：直接解析 64 位整数路径参数。
- **内置 `X-Request-Id` 链路注入**：请求进入 `serveHttp` 时自动读取或签发 UUIDv7 `X-Request-Id`，同步写入响应头与 `r.Context()`，下游直接调用 `logs.InfoCtx(ctx, ...)` 即可输出完整链路日志。

### 5. `core/logs` 幂等关闭与标准库 `log/slog` 桥接

```go
_ = logs.SetLogger(logs.AdapterSlog, `{"format":"json","level":7}`)
logs.InfoCtx(ctx, "order paid", "order_id", orderID, "amount", amount)
slogger := logs.AsSlog()
```

---

## 四、 业务项目接入方式

保持现有业务代码中的所有官方包导入路径（`"github.com/beego/beego/v2/..."`）完全不变，仅在业务项目 `go.mod` 末尾添加一行版本映射即可：

```go
replace github.com/beego/beego/v2 => github.com/84896150/beego/v2 v2.5.0
```
