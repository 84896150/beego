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

package logs

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type captureAdapter struct {
	mu   sync.Mutex
	msgs []string
}

func (c *captureAdapter) Init(_ string) error { return nil }
func (c *captureAdapter) WriteMsg(lm *LogMsg) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, lm.OldStyleFormat())
	return nil
}
func (c *captureAdapter) Destroy()                  {}
func (c *captureAdapter) Flush()                    {}
func (c *captureAdapter) SetFormatter(LogFormatter) {}

func TestSlogAdapter_JSONAndText(t *testing.T) {
	var jsonBuf bytes.Buffer
	adapter := newSlogLoggerWithWriter(&jsonBuf)
	require.NoError(t, adapter.Init(`{"format":"json","level":7}`))

	err := adapter.WriteMsg(&LogMsg{
		Level:      LevelInfo,
		Msg:        "order created",
		When:       time.Now(),
		Prefix:     "[API]",
		FilePath:   "/app/services/order.go",
		LineNumber: 42,
	})
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(jsonBuf.Bytes(), &decoded))
	assert.Equal(t, "INFO", decoded["level"])
	assert.Equal(t, "order created", decoded["msg"])
	assert.Equal(t, "[API]", decoded["prefix"])
}

func TestBeeLogger_SlogHandlerAndCtxLogs(t *testing.T) {
	adapterName := "capture_slog_bridge_test"
	capAdapter := &captureAdapter{}
	Register(adapterName, func() Logger { return capAdapter })

	bl := NewLogger(16)
	require.NoError(t, bl.SetLogger(adapterName))

	// Direct Ctx structured logging with WithRequestID and raw key
	ctx1 := WithRequestID(context.Background(), "req-v7-001")
	bl.InfoCtx(ctx1, "user login", "user_id", int64(42), "ip", "127.0.0.1")

	ctx2 := context.WithValue(context.Background(), "X-Request-Id", "req-x-002")
	bl.WarnCtx(ctx2, "rate limited", "attempt", 5)

	// Standard slog.Logger wrapping BeeLogger
	sl := bl.AsSlog().WithGroup("http").With(slog.String("env", "prod"))
	sl.ErrorContext(ctx1, "upstream timeout", slog.Int("status", 504))

	require.Len(t, capAdapter.msgs, 3)
	assert.Contains(t, capAdapter.msgs[0], "request_id=req-v7-001")
	assert.Contains(t, capAdapter.msgs[0], "user_id=42")
	assert.Contains(t, capAdapter.msgs[1], "request_id=req-x-002")
	assert.Contains(t, capAdapter.msgs[1], "attempt=5")
	assert.Contains(t, capAdapter.msgs[2], "http.env=prod")
	assert.Contains(t, capAdapter.msgs[2], "http.status=504")
	assert.Contains(t, strings.ToLower(capAdapter.msgs[2]), "upstream timeout")
}

func TestBeeLogger_IdempotentCloseAndReset(t *testing.T) {
	bl := NewLogger(8)
	bl.Async(8)
	bl.Info("async message 1")

	assert.NotPanics(t, func() {
		bl.Reset()
		bl.Reset()
		bl.Close()
		bl.Close()
		bl.Reset()
		bl.Flush()
		bl.Info("after close ignored safely")
	})

	// Non-async logger idempotency as well
	blSync := NewLogger()
	assert.NotPanics(t, func() {
		blSync.Reset()
		blSync.Close()
		blSync.Close()
		blSync.Reset()
	})
}
