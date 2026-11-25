// Copyright 2024 beego Author. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package orm

import (
	"context"
	"fmt"
	"reflect"

	"github.com/beego/beego/v2/client/orm/clauses/order_clause"
	imodels "github.com/beego/beego/v2/client/orm/internal/models"
)

// TypedQuery provides a compile-time type-safe query builder for model struct T.
// Every builder method returns a new immutable copy so chaining cannot leak state.
type TypedQuery[T any] struct {
	qs  QuerySeter
	ctx context.Context
}

// Query creates a compile-time type-safe query builder for model struct value type T.
// Example: orm.Query[models.Article](o).Filter("status", "published").All()
func Query[T any](exec QueryExecutor) *TypedQuery[T] {
	if exec == nil {
		panic("orm.Query[T]: QueryExecutor must not be nil")
	}
	typ := reflect.TypeFor[T]()
	if typ.Kind() != reflect.Struct {
		panic(fmt.Sprintf("orm.Query[T]: T must be a model struct value type (e.g. orm.Query[models.User](o)), got %s", typ.String()))
	}
	tq, ok := exec.(tableQuerier)
	if !ok {
		panic(fmt.Sprintf("orm.Query[T]: QueryExecutor %T does not support internal table query", exec))
	}
	var zero T
	return &TypedQuery[T]{
		qs:  tq.queryTable(&zero),
		ctx: context.Background(),
	}
}

// GetByID fetches a single record of T by its int64 primary key.
func GetByID[T any](exec QueryExecutor, id int64) (*T, error) {
	return GetByIDWithCtx[T](context.Background(), exec, id)
}

// GetByIDWithCtx fetches a single record of T by its int64 primary key with context.
func GetByIDWithCtx[T any](ctx context.Context, exec QueryExecutor, id int64) (*T, error) {
	typ := reflect.TypeFor[T]()
	if typ.Kind() != reflect.Struct {
		panic(fmt.Sprintf("orm.GetByIDWithCtx[T]: T must be a model struct value type, got %s", typ.String()))
	}
	pkField := "Id"
	if mi, ok := defaultModelCache.GetByFullName(imodels.GetFullName(typ)); ok && mi != nil && mi.Fields != nil && mi.Fields.Pk != nil {
		pkField = mi.Fields.Pk.Name
	}
	return Query[T](exec).WithCtx(ctx).Filter(pkField, id).One()
}

func (q *TypedQuery[T]) ctxOrBg() context.Context {
	if q.ctx == nil {
		return context.Background()
	}
	return q.ctx
}

func (q *TypedQuery[T]) clone(qs QuerySeter) *TypedQuery[T] {
	return &TypedQuery[T]{
		qs:  qs,
		ctx: q.ctx,
	}
}

// WithCtx attaches a context.Context to the typed query.
func (q *TypedQuery[T]) WithCtx(ctx context.Context) *TypedQuery[T] {
	if ctx == nil {
		ctx = context.Background()
	}
	return &TypedQuery[T]{
		qs:  q.qs,
		ctx: ctx,
	}
}

// Filter adds an AND condition expression.
func (q *TypedQuery[T]) Filter(expr string, args ...any) *TypedQuery[T] {
	return q.clone(q.qs.Filter(expr, args...))
}

// Exclude adds an AND NOT condition expression.
func (q *TypedQuery[T]) Exclude(expr string, args ...any) *TypedQuery[T] {
	return q.clone(q.qs.Exclude(expr, args...))
}

// FilterRaw adds a raw WHERE expression.
func (q *TypedQuery[T]) FilterRaw(expr, sql string) *TypedQuery[T] {
	return q.clone(q.qs.FilterRaw(expr, sql))
}

// SetCond sets a custom *Condition on the query.
func (q *TypedQuery[T]) SetCond(cond *Condition) *TypedQuery[T] {
	return q.clone(q.qs.SetCond(cond))
}

// GetCond returns the current *Condition of the query.
func (q *TypedQuery[T]) GetCond() *Condition {
	return q.qs.GetCond()
}

// OrderBy sets ORDER BY expressions ("col" ASC, "-col" DESC).
func (q *TypedQuery[T]) OrderBy(exprs ...string) *TypedQuery[T] {
	return q.clone(q.qs.OrderBy(exprs...))
}

// OrderClauses sets structured ORDER BY clauses.
func (q *TypedQuery[T]) OrderClauses(orders ...*order_clause.Order) *TypedQuery[T] {
	return q.clone(q.qs.OrderClauses(orders...))
}

// Limit sets LIMIT and optional OFFSET.
func (q *TypedQuery[T]) Limit(limit int, offset ...int) *TypedQuery[T] {
	if len(offset) > 0 {
		return q.clone(q.qs.Limit(limit, offset[0]))
	}
	return q.clone(q.qs.Limit(limit))
}

// Offset sets OFFSET.
func (q *TypedQuery[T]) Offset(offset int) *TypedQuery[T] {
	return q.clone(q.qs.Offset(offset))
}

// GroupBy sets GROUP BY expressions.
func (q *TypedQuery[T]) GroupBy(exprs ...string) *TypedQuery[T] {
	return q.clone(q.qs.GroupBy(exprs...))
}

// Distinct enables SELECT DISTINCT.
func (q *TypedQuery[T]) Distinct() *TypedQuery[T] {
	return q.clone(q.qs.Distinct())
}

// ForUpdate adds FOR UPDATE lock clause.
func (q *TypedQuery[T]) ForUpdate() *TypedQuery[T] {
	return q.clone(q.qs.ForUpdate())
}

// RelatedSel loads related models together.
func (q *TypedQuery[T]) RelatedSel(params ...any) *TypedQuery[T] {
	return q.clone(q.qs.RelatedSel(params...))
}

// ForceIndex adds FORCE INDEX hints.
func (q *TypedQuery[T]) ForceIndex(indexes ...string) *TypedQuery[T] {
	return q.clone(q.qs.ForceIndex(indexes...))
}

// UseIndex adds USE INDEX hints.
func (q *TypedQuery[T]) UseIndex(indexes ...string) *TypedQuery[T] {
	return q.clone(q.qs.UseIndex(indexes...))
}

// IgnoreIndex adds IGNORE INDEX hints.
func (q *TypedQuery[T]) IgnoreIndex(indexes ...string) *TypedQuery[T] {
	return q.clone(q.qs.IgnoreIndex(indexes...))
}

// All returns matching rows as a non-nil slice []T.
func (q *TypedQuery[T]) All(cols ...string) ([]T, error) {
	list := make([]T, 0)
	if _, err := q.qs.AllWithCtx(q.ctxOrBg(), &list, cols...); err != nil {
		return make([]T, 0), err
	}
	if list == nil {
		list = make([]T, 0)
	}
	return list, nil
}

// One returns a single matching row *T, or nil and ErrNoRows/ErrMultiRows.
func (q *TypedQuery[T]) One(cols ...string) (*T, error) {
	var item T
	if err := q.qs.OneWithCtx(q.ctxOrBg(), &item, cols...); err != nil {
		return nil, err
	}
	return &item, nil
}

// Count returns total matching row count (unaffected by Limit/Offset).
func (q *TypedQuery[T]) Count() (int64, error) {
	return q.qs.CountWithCtx(q.ctxOrBg())
}

// Exist checks whether at least one matching row exists and preserves database errors.
func (q *TypedQuery[T]) Exist() (bool, error) {
	cnt, err := q.Count()
	return cnt > 0, err
}

// Paginate executes Count first (short-circuiting on 0/err) then fetches the requested page.
func (q *TypedQuery[T]) Paginate(page, perPage int, cols ...string) ([]T, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage <= 0 {
		perPage = 10
	}
	total, err := q.Count()
	if err != nil || total == 0 {
		return make([]T, 0), 0, err
	}
	offset := (page - 1) * perPage
	items, err := q.Limit(perPage, offset).All(cols...)
	if err != nil {
		return make([]T, 0), total, err
	}
	return items, total, nil
}

// Update executes batch update for matching rows.
func (q *TypedQuery[T]) Update(values Params) (int64, error) {
	return q.qs.UpdateWithCtx(q.ctxOrBg(), values)
}

// Delete executes batch delete for matching rows.
func (q *TypedQuery[T]) Delete() (int64, error) {
	return q.qs.DeleteWithCtx(q.ctxOrBg())
}

// Values queries matching rows into a non-nil slice of Params maps.
func (q *TypedQuery[T]) Values(exprs ...string) ([]Params, error) {
	res := make([]Params, 0)
	if _, err := q.qs.ValuesWithCtx(q.ctxOrBg(), &res, exprs...); err != nil {
		return make([]Params, 0), err
	}
	if res == nil {
		res = make([]Params, 0)
	}
	return res, nil
}
