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
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type genericArticle struct {
	Id     int64  `orm:"auto;pk" json:"id"`
	Title  string `orm:"size(128)" json:"title"`
	Status string `orm:"size(32)" json:"status"`
	Views  int    `json:"views"`
}

func (a *genericArticle) TableName() string {
	return "generic_article"
}

var genericArticleRegisterOnce sync.Once

func setupGenericTestDB(t *testing.T) Ormer {
	t.Helper()
	genericArticleRegisterOnce.Do(func() {
		RegisterModel(new(genericArticle))
	})

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	alias := "gen_" + t.Name()
	err := RegisterDataBase(alias, "sqlite3", dsn)
	require.NoError(t, err)
	err = RunSyncdb(alias, true, false)
	require.NoError(t, err)

	return NewOrmUsingDB(alias)
}

func TestTypedQuery_CRUD_And_NonNilSlice(t *testing.T) {
	o := setupGenericTestDB(t)

	// Empty query should return non-nil empty slice that marshals to "[]"
	emptyList, err := Query[genericArticle](o).All()
	require.NoError(t, err)
	assert.NotNil(t, emptyList)
	assert.Len(t, emptyList, 0)
	rawJSON, err := json.Marshal(emptyList)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(rawJSON))

	// Insert sample records
	for i := 1; i <= 5; i++ {
		art := &genericArticle{
			Title:  fmt.Sprintf("Article %d", i),
			Status: "published",
			Views:  i * 10,
		}
		if i == 5 {
			art.Status = "draft"
		}
		_, err := o.Insert(art)
		require.NoError(t, err)
	}

	// Filter + OrderBy + All
	published, err := Query[genericArticle](o).
		Filter("status", "published").
		OrderBy("-views").
		All()
	require.NoError(t, err)
	require.Len(t, published, 4)
	assert.Equal(t, 40, published[0].Views)

	// Exist
	exists, err := Query[genericArticle](o).Filter("status", "draft").Exist()
	require.NoError(t, err)
	assert.True(t, exists)

	notExists, err := Query[genericArticle](o).Filter("status", "archived").Exist()
	require.NoError(t, err)
	assert.False(t, notExists)

	// One
	one, err := Query[genericArticle](o).Filter("title", "Article 2").One()
	require.NoError(t, err)
	require.NotNil(t, one)
	assert.Equal(t, 20, one.Views)

	// Update + Delete
	affected, err := Query[genericArticle](o).Filter("status", "draft").Update(Params{"status": "published"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), affected)

	deleted, err := Query[genericArticle](o).Filter("views__lte", 10).Delete()
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)
}

func TestTypedQuery_Paginate_And_CountIgnoreLimit(t *testing.T) {
	o := setupGenericTestDB(t)

	for i := 1; i <= 12; i++ {
		_, err := o.Insert(&genericArticle{
			Title:  fmt.Sprintf("Item %02d", i),
			Status: "published",
			Views:  i,
		})
		require.NoError(t, err)
	}

	// Verify Count() ignores prior Limit/Offset/OrderBy
	qsWithLimit := Query[genericArticle](o).Filter("status", "published").OrderBy("-id").Limit(3, 5)
	cnt, err := qsWithLimit.Count()
	require.NoError(t, err)
	assert.Equal(t, int64(12), cnt)

	// Ensure original query still honors its Limit/Offset on All()
	sliced, err := qsWithLimit.All()
	require.NoError(t, err)
	assert.Len(t, sliced, 3)

	// Verify Paginate
	page2, total, err := Query[genericArticle](o).Filter("status", "published").OrderBy("id").Paginate(2, 5)
	require.NoError(t, err)
	assert.Equal(t, int64(12), total)
	require.Len(t, page2, 5)
	assert.Equal(t, "Item 06", page2[0].Title)
}

func TestGetByID(t *testing.T) {
	o := setupGenericTestDB(t)
	art := &genericArticle{Title: "Single PK", Status: "published", Views: 99}
	id, err := o.Insert(art)
	require.NoError(t, err)

	got, err := GetByID[genericArticle](o, id)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "Single PK", got.Title)

	_, err = GetByIDWithCtx[genericArticle](context.Background(), o, id+999)
	assert.ErrorIs(t, err, ErrNoRows)
	assert.ErrorIs(t, err, sql.ErrNoRows)
	assert.True(t, err == ErrNoRows)
}

func TestTypedQuery_RejectsPointerTypeParam(t *testing.T) {
	o := &DoNothingOrm{}
	assert.Panics(t, func() {
		Query[*genericArticle](o)
	})
}

func TestDoTx_CommitAndRollback(t *testing.T) {
	o := setupGenericTestDB(t)

	// Rollback on returned error
	sentinel := errors.New("abort tx")
	err := o.DoTx(func(ctx context.Context, txOrm TxOrmer) error {
		_, insErr := txOrm.Insert(&genericArticle{Title: "Will Rollback", Status: "draft"})
		require.NoError(t, insErr)
		return sentinel
	})
	assert.ErrorIs(t, err, sentinel)

	cnt, err := Query[genericArticle](o).Filter("title", "Will Rollback").Count()
	require.NoError(t, err)
	assert.Equal(t, int64(0), cnt)

	// Commit on nil return
	err = o.DoTx(func(ctx context.Context, txOrm TxOrmer) error {
		_, insErr := txOrm.Insert(&genericArticle{Title: "Will Commit", Status: "published"})
		return insErr
	})
	require.NoError(t, err)

	cnt, err = Query[genericArticle](o).Filter("title", "Will Commit").Count()
	require.NoError(t, err)
	assert.Equal(t, int64(1), cnt)
}
