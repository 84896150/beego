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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type guardValidUser struct {
	Id           int64  `orm:"auto;pk" json:"id"`
	CategoryId   int64  `json:"category_id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
	SessionToken string `json:"-"`
	Transient    string `orm:"-"`
}

type guardBad32BitID struct {
	Id   int    `orm:"auto;pk" json:"id"`
	Name string `json:"name"`
}

type guardBadForeignID struct {
	Id     int64 `orm:"auto;pk" json:"id"`
	UserID int32 `json:"user_id"`
}

type guardMissingJSONTag struct {
	Id    int64 `orm:"auto;pk" json:"id"`
	Title string
}

type guardLeakedSecret struct {
	Id           int64  `orm:"auto;pk" json:"id"`
	PasswordHash string `json:"password_hash"`
}

func TestStrictModelGuard(t *testing.T) {
	require.NoError(t, ValidateModelSpecs(new(guardValidUser)))

	err := ValidateModelSpecs(new(guardBad32BitID))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "int64")

	err = ValidateModelSpecs(new(guardBadForeignID))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "UserID")

	err = ValidateModelSpecs(new(guardMissingJSONTag))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "json")

	err = ValidateModelSpecs(new(guardLeakedSecret))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `json:"-"`)

	prev := IsStrictModelGuardEnabled()
	defer SetStrictModelGuard(prev)

	SetStrictModelGuard(true)
	assert.True(t, IsStrictModelGuardEnabled())
	assert.Panics(t, func() {
		RegisterModel(new(guardMissingJSONTag))
	})
}
