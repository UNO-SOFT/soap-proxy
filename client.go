// Copyright 2020 Tamás Gulácsi
//
//
//    Licensed under the Apache License, Version 2.0 (the "License");
//    you may not use this file except in compliance with the License.
//    You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
//    Unless required by applicable law or agreed to in writing, software
//    distributed under the License is distributed on an "AS IS" BASIS,
//    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//    See the License for the specific language governing permissions and
//    limitations under the License.

package soapproxy

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"io/ioutil"
	"strconv"
	"sync"
	"time"

	errors "golang.org/x/xerrors"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/tgulacsi/go/httpclient"
)

var (DefaultCallTimeout = time.Minute

clientsMu sync.RWMutex
clients = make(map[string]*retryablehttp.Client)
)

const (
	SOAPHeader = `<?xml version="1.0" encoding="UTF-8"?><soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Header/><soapenv:Body>`
	SOAPFooter = `</soapenv:Body></soapenv:Envelope>`
)

// SOAPCall destURL with SOAPAction=action, decoding the response body into resp.
func SOAPCall(ctx context.Context, destURL, action string, reqBody string , resp interface{}, Log func(...interface{}) error) error {
	buf := bufPool.Get().(*bytes.Buffer)
	defer func(){
		buf.Reset()
		bufPool.Put(buf)
	}()
	buf.Write([]byte(SOAPHeader))
	io.WriteString(buf, reqBody)
	buf.Write([]byte(SOAPFooter))

	request, err := retryablehttp.NewRequest("POST", destURL, buf.Bytes())
	if err != nil {
		return err
	}
	request = request.WithContext(ctx)
	request.Header.Set("Content-Type", "text/xml; charset=utf-8")
	request.Header.Set("SOAPAction", action)
	request.Header.Set("Length", strconv.Itoa(buf.Len()))
	if Log != nil {
		Log("POST", destURL, "header", request.Header, "xml", buf.String())
	}
	clientsMu.RLock()
	cl, ok := clients[destURL]
	clientsMu.RUnlock()
	if !ok {
		clientsMu.Lock()
		if cl, ok = clients[destURL]; !ok {
			to := DefaultCallTimeout
			if dl, ok := ctx.Deadline(); ok {
				if d := time.Until(dl); d > time.Second {
					to = d
				}
			}
			cl = httpclient.NewWithClient("url="+destURL, nil, 1*time.Second, to, 0.6, Log)
			clients[destURL] = cl
		}
		clientsMu.Unlock()
	}
	response, err := cl.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	buf.Reset()
	if response.StatusCode >= 400 {
		io.Copy(buf, response.Body)
		return errors.Errorf("%s: %w", buf.String(), errors.New(response.Status))
	}

	tr := io.TeeReader(response.Body, buf)
	dec := xml.NewDecoder(tr)
	st, err := FindBody(dec)
	if err != nil {
		return err
	}
	err = dec.DecodeElement(resp, &st)
	if Log != nil {
		if err != nil {
			io.Copy(ioutil.Discard, tr)
		}
		Log("response", buf.String(), "decoded", resp, "error", err)
	}
	return err
}

