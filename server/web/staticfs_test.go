package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStaticFS(t *testing.T) {
	mockFS := fstest.MapFS{
		"pages/user.json": &fstest.MapFile{
			Data: []byte(`{"type":"page","title":"Users"}`),
		},
		"index.html": &fstest.MapFile{
			Data: []byte(`<html>home</html>`),
		},
		"sub/readme.txt": &fstest.MapFile{
			Data: []byte("hello embed"),
		},
	}

	SetStaticFS("/static", mockFS)
	defer DelStaticFS("/static")

	handler := NewControllerRegister()

	// 1. Serve embedded JSON file
	req, err := http.NewRequest(http.MethodGet, "/static/pages/user.json", nil)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"title":"Users"`)

	// 2. Serve index.html on prefix root
	req, err = http.NewRequest(http.MethodGet, "/static/", nil)
	require.NoError(t, err)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "<html>home</html>", rec.Body.String())

	// 3. Directory without index.html forbidden when DirectoryIndex=false
	prevDirIdx := BConfig.WebConfig.DirectoryIndex
	BConfig.WebConfig.DirectoryIndex = false
	defer func() { BConfig.WebConfig.DirectoryIndex = prevDirIdx }()

	req, err = http.NewRequest(http.MethodGet, "/static/sub/", nil)
	require.NoError(t, err)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	// 4. DelStaticFS removes mounted filesystem
	DelStaticFS("/static")
	req, err = http.NewRequest(http.MethodGet, "/static/pages/user.json", nil)
	require.NoError(t, err)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
