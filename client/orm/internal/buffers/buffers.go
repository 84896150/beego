// Copyright 2014 beego Author. All Rights Reserved.
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
	"sync"
)

const maxPooledBufferCap = 64 << 10

var (
	_          Buffer = (*bytes.Buffer)(nil)
	bufferPool        = sync.Pool{
		New: func() any {
			return new(bytes.Buffer)
		},
	}
)

type Buffer interface {
	Write(p []byte) (int, error)
	WriteString(s string) (int, error)
	WriteByte(c byte) error
	String() string
}

func Get() Buffer {
	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	return buf
}

func Put(bf Buffer) {
	if bf == nil {
		return
	}
	buf, ok := bf.(*bytes.Buffer)
	if !ok {
		return
	}
	if buf.Cap() > maxPooledBufferCap {
		return
	}
	buf.Reset()
	bufferPool.Put(buf)
}
