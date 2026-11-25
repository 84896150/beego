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

package utils

import (
	"bytes"
	"crypto/rand"
)

var alphaNum = []byte(`0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz`)

// RandomCreateBytes generate random []byte by specify chars using unbiased crypto/rand rejection sampling.
func RandomCreateBytes(n int, alphabets ...byte) []byte {
	if n <= 0 {
		return []byte{}
	}
	if len(alphabets) == 0 {
		alphabets = alphaNum
	}
	m := len(alphabets)
	if m == 1 {
		return bytes.Repeat(alphabets, n)
	}

	result := make([]byte, 0, n)
	if m >= 256 {
		buf := make([]byte, n)
		if _, err := rand.Read(buf); err != nil {
			panic(fmtErrorRand(err))
		}
		for _, b := range buf {
			result = append(result, alphabets[int(b)%m])
		}
		return result
	}

	maxValid := 256 - (256 % m)
	buf := make([]byte, n*2)
	for len(result) < n {
		if _, err := rand.Read(buf); err != nil {
			panic(fmtErrorRand(err))
		}
		for _, b := range buf {
			if int(b) >= maxValid {
				continue
			}
			result = append(result, alphabets[int(b)%m])
			if len(result) == n {
				return result
			}
		}
	}
	return result
}

func fmtErrorRand(err error) string {
	return "core/utils: crypto/rand failed: " + err.Error()
}
