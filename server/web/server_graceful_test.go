package web

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	beecontext "github.com/beego/beego/v2/server/web/context"
)

type testLifecycleCallback struct {
	beforeShutdownCalled bool
}

func (c *testLifecycleCallback) AfterStart(_ *HttpServer) {}
func (c *testLifecycleCallback) BeforeShutdown(_ *HttpServer) {
	c.beforeShutdownCalled = true
}

func TestHttpServer_GracefulShutdown(t *testing.T) {
	app := NewHttpServerWithCfg(newBConfig())
	cb := &testLifecycleCallback{}
	app.LifeCycleCallbacks = append(app.LifeCycleCallbacks, cb)

	inFlightStarted := make(chan struct{})
	app.Get("/slow", func(ctx *beecontext.Context) {
		close(inFlightStarted)
		time.Sleep(150 * time.Millisecond)
		ctx.Output.Body([]byte("graceful-ok"))
	})
	app.Handlers.Init()
	app.Server.Handler = app.Handlers

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	serveErrCh := make(chan error, 1)
	go func() {
		serveErrCh <- app.Server.Serve(ln)
	}()

	url := "http://" + ln.Addr().String() + "/slow"
	type respResult struct {
		status int
		body   string
		err    error
	}
	respCh := make(chan respResult, 1)
	go func() {
		resp, reqErr := http.Get(url)
		if reqErr != nil {
			respCh <- respResult{err: reqErr}
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		respCh <- respResult{status: resp.StatusCode, body: string(b)}
	}()

	<-inFlightStarted

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, app.Shutdown(shutdownCtx))
	assert.True(t, cb.beforeShutdownCalled)

	res := <-respCh
	require.NoError(t, res.err)
	assert.Equal(t, http.StatusOK, res.status)
	assert.Equal(t, "graceful-ok", res.body)

	serveErr := <-serveErrCh
	assert.True(t, errors.Is(serveErr, http.ErrServerClosed))

	// New requests after shutdown must be refused
	_, postErr := http.Get(url)
	assert.Error(t, postErr)
}
