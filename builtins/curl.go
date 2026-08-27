// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/builtins: Émulation pure Go de ext-curl pour guzzlehttp/guzzle et SDKs

package builtins

import (
	"bytes"
	"io"
	"net/http"
	"sync"
	"time"

	"code.hazyhaar.fr/devhoros/php127/types"
)

type CurlHandle struct {
	URL            string
	Method         string
	Headers        map[string]string
	Body           []byte
	Timeout        time.Duration
	ReturnTransfer bool
	LastResponse   []byte
	LastStatusCode int
	LastError      string
	LastErrno      int
}

var (
	curlMu      sync.RWMutex
	curlHandles       = make(map[int64]*CurlHandle)
	nextCurlId  int64 = 1
	httpClient        = &http.Client{
		Timeout: 30 * time.Second,
	}
)

const (
	CURLOPT_URL            = 10002
	CURLOPT_RETURNTRANSFER = 19913
	CURLOPT_POST           = 47
	CURLOPT_POSTFIELDS     = 10015
	CURLOPT_HTTPHEADER     = 10023
	CURLOPT_TIMEOUT        = 13
	CURLOPT_CUSTOMREQUEST  = 10036
	CURLINFO_HTTP_CODE     = 2097154
)

func CurlInit(urlVal types.Value) types.Value {
	curlMu.Lock()
	defer curlMu.Unlock()

	id := nextCurlId
	nextCurlId++

	h := &CurlHandle{
		Method:         "GET",
		Headers:        make(map[string]string),
		Timeout:        30 * time.Second,
		ReturnTransfer: true,
	}
	if urlVal.Type == types.TypeString && urlVal.String() != "" {
		h.URL = urlVal.String()
	}

	curlHandles[id] = h
	return types.Value{Type: types.TypeObject, Data: id}
}

func CurlSetopt(hVal, optVal, val types.Value) types.Value {
	curlMu.Lock()
	defer curlMu.Unlock()

	h, ok := curlHandles[hVal.Data]
	if !ok {
		return types.NewBool(false)
	}

	opt := optVal.ToLong()
	switch opt {
	case CURLOPT_URL:
		h.URL = val.String()
	case CURLOPT_RETURNTRANSFER:
		h.ReturnTransfer = val.ToBool()
	case CURLOPT_POST:
		if val.ToBool() {
			h.Method = "POST"
		}
	case CURLOPT_POSTFIELDS:
		h.Body = val.Bytes()
	case CURLOPT_CUSTOMREQUEST:
		h.Method = val.String()
	case CURLOPT_TIMEOUT:
		h.Timeout = time.Duration(val.ToLong()) * time.Second
	}

	return types.NewBool(true)
}

func CurlExec(hVal types.Value) types.Value {
	curlMu.Lock()
	h, ok := curlHandles[hVal.Data]
	curlMu.Unlock()

	if !ok || h.URL == "" {
		return types.NewBool(false)
	}

	req, err := http.NewRequest(h.Method, h.URL, bytes.NewReader(h.Body))
	if err != nil {
		h.LastError = err.Error()
		h.LastErrno = 1
		return types.NewBool(false)
	}

	for k, v := range h.Headers {
		req.Header.Set(k, v)
	}

	client := httpClient
	if h.Timeout > 0 {
		client = &http.Client{Timeout: h.Timeout}
	}

	resp, err := client.Do(req)
	if err != nil {
		h.LastError = err.Error()
		h.LastErrno = 2
		return types.NewBool(false)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	h.LastResponse = respBody
	h.LastStatusCode = resp.StatusCode

	if h.ReturnTransfer {
		return types.NewString(string(respBody))
	}
	return types.NewBool(true)
}

func CurlGetinfo(hVal, optVal types.Value) types.Value {
	curlMu.RLock()
	h, ok := curlHandles[hVal.Data]
	curlMu.RUnlock()

	if !ok {
		return types.NewBool(false)
	}

	if optVal.ToLong() == CURLINFO_HTTP_CODE {
		return types.NewLong(int64(h.LastStatusCode))
	}

	return types.NewLong(int64(h.LastStatusCode))
}

func CurlClose(hVal types.Value) types.Value {
	curlMu.Lock()
	defer curlMu.Unlock()
	delete(curlHandles, hVal.Data)
	return types.NewNull()
}

func CurlError(hVal types.Value) types.Value {
	curlMu.RLock()
	defer curlMu.RUnlock()
	if h, ok := curlHandles[hVal.Data]; ok {
		return types.NewString(h.LastError)
	}
	return types.NewString("")
}

func CurlErrno(hVal types.Value) types.Value {
	curlMu.RLock()
	defer curlMu.RUnlock()
	if h, ok := curlHandles[hVal.Data]; ok {
		return types.NewLong(int64(h.LastErrno))
	}
	return types.NewLong(0)
}
