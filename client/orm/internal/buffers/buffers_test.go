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

package buffers

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBufferPool_GetPutAndOversizeDrop(t *testing.T) {
	b := Get()
	require.NotNil(t, b)
	_, err := b.WriteString("SELECT id FROM user")
	require.NoError(t, err)
	assert.Equal(t, "SELECT id FROM user", b.String())
	Put(b)

	// Next Get should return a reset buffer
	b2 := Get()
	assert.Equal(t, "", b2.String())
	Put(b2)

	// Oversized buffers should be reset/dropped instead of pinning huge capacity in pool
	huge := new(bytes.Buffer)
	huge.Grow(maxPooledBufferCap + 1024)
	huge.WriteString(strings.Repeat("x", maxPooledBufferCap+1))
	Put(huge)

	// Nil buffer should be a safe no-op
	Put(nil)
}
