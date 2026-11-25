// Copyright 2016 beego Author. All Rights Reserved.
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

package context

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/beego/beego/v2/server/web/session"
)

func TestXsrfReset_01(t *testing.T) {
	r := &http.Request{}
	c := NewContext()
	c.Request = r
	c.ResponseWriter = &Response{}
	c.ResponseWriter.reset(httptest.NewRecorder())
	c.Output.Reset(c)
	c.Input.Reset(c)
	c.XSRFToken("key", 16)
	if c._xsrfToken == "" {
		t.FailNow()
	}
	token := c._xsrfToken
	c.Reset(&Response{ResponseWriter: httptest.NewRecorder()}, r)
	if c._xsrfToken != "" {
		t.FailNow()
	}
	c.XSRFToken("key", 16)
	if c._xsrfToken == "" {
		t.FailNow()
	}
	if token == c._xsrfToken {
		t.FailNow()
	}
}

func TestContext_Session(t *testing.T) {
	c := NewContext()
	if store, err := c.Session(); store != nil || err == nil {
		t.FailNow()
	}
}

func TestContext_Session1(t *testing.T) {
	c := Context{}
	if store, err := c.Session(); store != nil || err == nil {
		t.FailNow()
	}
}

func TestContext_Session2(t *testing.T) {
	c := NewContext()
	c.Input.CruSession = &session.MemSessionStore{}

	if store, err := c.Session(); store == nil || err != nil {
		t.FailNow()
	}
}

func TestSetCookie(t *testing.T) {
	type cookie struct {
		Name     string
		Value    string
		MaxAge   int64
		Path     string
		Domain   string
		Secure   bool
		HttpOnly bool
		SameSite string
	}
	type testItem struct {
		item cookie
		want string
	}
	cases := []struct {
		request string
		valueGp []testItem
	}{
		{"/", []testItem{{cookie{"name", "value", -1, "/", "", false, false, "Strict"}, "name=value; Max-Age=0; Path=/; SameSite=Strict"}}},
		{"/", []testItem{{cookie{"name", "value", -1, "/", "", false, false, "Lax"}, "name=value; Max-Age=0; Path=/; SameSite=Lax"}}},
		{"/", []testItem{{cookie{"name", "value", -1, "/", "", false, false, "None"}, "name=value; Max-Age=0; Path=/; SameSite=None"}}},
		{"/", []testItem{{cookie{"name", "value", -1, "/", "", false, false, ""}, "name=value; Max-Age=0; Path=/"}}},
	}
	for _, c := range cases {
		r, _ := http.NewRequest("GET", c.request, nil)
		output := NewOutput()
		output.Context = NewContext()
		output.Context.Reset(httptest.NewRecorder(), r)
		for _, item := range c.valueGp {
			params := item.item
			others := []interface{}{params.MaxAge, params.Path, params.Domain, params.Secure, params.HttpOnly, params.SameSite}
			output.Context.SetCookie(params.Name, params.Value, others...)
			got := output.Context.ResponseWriter.Header().Get("Set-Cookie")
			if got != item.want {
				t.Fatalf("SetCookie error,should be:\n%v \ngot:\n%v", item.want, got)
			}
		}
	}
}

func TestContext_BindAdaptive(t *testing.T) {
	type payload struct {
		Name string `form:"name" json:"name"`
		Page int    `form:"page" json:"page"`
	}

	t.Run("GET without Content-Type binds query form", func(t *testing.T) {
		r, _ := http.NewRequest(http.MethodGet, "/articles?name=go127&page=2", nil)
		c := NewContext()
		c.Reset(httptest.NewRecorder(), r)
		var p payload
		if err := c.Bind(&p); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if p.Name != "go127" || p.Page != 2 {
			t.Fatalf("unexpected bound payload: %+v", p)
		}
	})

	t.Run("POST JSON without Content-Type binds JSON", func(t *testing.T) {
		body := `{"name":"beego","page":3}`
		r, _ := http.NewRequest(http.MethodPost, "/articles", strings.NewReader(body))
		c := NewContext()
		c.Reset(httptest.NewRecorder(), r)
		c.Input.RequestBody = []byte(body)
		var p payload
		if err := c.Bind(&p); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if p.Name != "beego" || p.Page != 3 {
			t.Fatalf("unexpected bound payload: %+v", p)
		}
	})

	t.Run("POST empty body without Content-Type falls back to query/form", func(t *testing.T) {
		r, _ := http.NewRequest(http.MethodPost, "/articles?name=emptybody&page=5", nil)
		c := NewContext()
		c.Reset(httptest.NewRecorder(), r)
		var p payload
		if err := c.Bind(&p); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if p.Name != "emptybody" || p.Page != 5 {
			t.Fatalf("unexpected bound payload: %+v", p)
		}
	})

	t.Run("POST JSON with charset parameter binds JSON", func(t *testing.T) {
		body := `{"name":"charset","page":7}`
		r, _ := http.NewRequest(http.MethodPost, "/articles", strings.NewReader(body))
		r.Header.Set("Content-Type", "Application/JSON; charset=utf-8")
		c := NewContext()
		c.Reset(httptest.NewRecorder(), r)
		c.Input.RequestBody = []byte(body)
		var p payload
		if err := c.Bind(&p); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if p.Name != "charset" || p.Page != 7 {
			t.Fatalf("unexpected bound payload: %+v", p)
		}
	})

	t.Run("POST multipart/form-data binds form fields", func(t *testing.T) {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		_ = w.WriteField("name", "multipart")
		_ = w.WriteField("page", "9")
		_ = w.Close()

		r, _ := http.NewRequest(http.MethodPost, "/articles", &buf)
		r.Header.Set("Content-Type", w.FormDataContentType())
		c := NewContext()
		c.Reset(httptest.NewRecorder(), r)
		var p payload
		if err := c.Bind(&p); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if p.Name != "multipart" || p.Page != 9 {
			t.Fatalf("unexpected bound payload: %+v", p)
		}
	})

	t.Run("Invalid proto target returns error instead of panic", func(t *testing.T) {
		r, _ := http.NewRequest(http.MethodPost, "/proto", strings.NewReader("abc"))
		r.Header.Set("Content-Type", ApplicationProto)
		c := NewContext()
		c.Reset(httptest.NewRecorder(), r)
		var p payload
		if err := c.Bind(&p); err == nil {
			t.Fatal("expected error for non-proto target")
		}
	})
}
