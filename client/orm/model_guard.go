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
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
)

var strictModelGuard atomic.Bool

// SetStrictModelGuard enables or disables boot-time model specification checks
// during RegisterModel calls.
func SetStrictModelGuard(enabled bool) {
	strictModelGuard.Store(enabled)
}

// IsStrictModelGuardEnabled reports whether strict model guard checks are enabled.
func IsStrictModelGuardEnabled() bool {
	return strictModelGuard.Load()
}

// ValidateModelSpecs checks registered model structs against AI-native safety rules:
//  1. Primary key and *Id/*ID integer fields must use int64 (forbids int/int32/uint/uint32).
//  2. Every exported persistent field must declare an explicit `json` tag.
//  3. Sensitive credential fields (password/secret/session_token) must set `json:"-"`.
func ValidateModelSpecs(models ...any) error {
	for _, md := range models {
		if md == nil {
			return fmt.Errorf("orm.ValidateModelSpecs: nil model passed")
		}
		typ := reflect.TypeOf(md)
		for typ.Kind() == reflect.Ptr {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct {
			return fmt.Errorf("orm.ValidateModelSpecs: expected struct or pointer to struct, got %s", typ.String())
		}
		if err := validateStructSpec(typ); err != nil {
			return err
		}
	}
	return nil
}

func validateStructSpec(typ reflect.Type) error {
	structName := typ.Name()
	if structName == "" {
		structName = typ.String()
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		ormTag := field.Tag.Get("orm")
		if strings.TrimSpace(ormTag) == "-" {
			continue
		}
		if field.Anonymous {
			ft := field.Type
			for ft.Kind() == reflect.Ptr {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				if err := validateStructSpec(ft); err != nil {
					return err
				}
				continue
			}
		}

		// Rule 1: primary key or *Id/*ID integer fields must be int64.
		isPK := hasORMToken(ormTag, "pk")
		isIDName := strings.HasSuffix(field.Name, "Id") || strings.HasSuffix(field.Name, "ID")
		if isPK || isIDName {
			ft := field.Type
			for ft.Kind() == reflect.Ptr {
				ft = ft.Elem()
			}
			switch ft.Kind() {
			case reflect.Int, reflect.Int32, reflect.Uint, reflect.Uint32:
				return fmt.Errorf(
					"[ModelGuard] %s.%s uses %s; upgrade primary/foreign ID fields to int64 to prevent 32-bit truncation and match BIGINT schema",
					structName, field.Name, field.Type.String(),
				)
			}
		}

		// Rule 2: all exported persistent fields must explicitly declare a `json` tag.
		jsonTag, hasJSON := field.Tag.Lookup("json")
		if !hasJSON {
			return fmt.Errorf(
				"[ModelGuard] %s.%s is missing an explicit `json` struct tag; add `json:\"snake_case\"` or `json:\"-\"`",
				structName, field.Name,
			)
		}

		// Rule 3: sensitive credential fields must be hidden from JSON serialization (`json:"-"`).
		normalized := strings.ReplaceAll(strings.ToLower(field.Name), "_", "")
		if strings.Contains(normalized, "password") ||
			strings.Contains(normalized, "secret") ||
			strings.Contains(normalized, "sessiontoken") {
			if strings.TrimSpace(jsonTag) != "-" {
				return fmt.Errorf(
					"[ModelGuard] %s.%s appears to hold sensitive credentials (`json:%q`); set `json:\"-\"` to block accidental serialization",
					structName, field.Name, jsonTag,
				)
			}
		}
	}
	return nil
}

func hasORMToken(ormTag, token string) bool {
	if ormTag == "" {
		return false
	}
	for _, part := range strings.Split(ormTag, ";") {
		p := strings.ToLower(strings.TrimSpace(part))
		if p == token || strings.HasPrefix(p, token+"(") {
			return true
		}
	}
	return false
}
