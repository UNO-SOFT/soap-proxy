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
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/tgulacsi/go/httpclient"
)

var (
	DefaultCallTimeout = time.Minute

	clientsMu sync.RWMutex
	clients   = make(map[string]*retryablehttp.Client)
)

const (
	SOAPHeader = `<?xml version="1.0" encoding="utf-8"?><soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Header>`
	SOAPBody   = `</soapenv:Header><soapenv:Body>`
	SOAPFooter = `</soapenv:Body></soapenv:Envelope>`
)

// SOAPCallWithHeader calls with the given SOAP- and extra header and action.
func SOAPCallWithHeaderClient(ctx context.Context,
	client *http.Client,
	destURL string, customize func(req *http.Request),
	action, soapHeader, reqBody string, resp interface{},
	Log func(...interface{}) error,
) error {
	buf := bufPool.Get().(*bytes.Buffer)
	defer func() {
		buf.Reset()
		bufPool.Put(buf)
	}()
	buf.WriteString(SOAPHeader)
	buf.WriteString(soapHeader)
	buf.WriteString(SOAPBody)
	buf.WriteString(reqBody)
	buf.WriteString(SOAPFooter)

	request, err := retryablehttp.NewRequest("POST", destURL, buf.Bytes())
	if err != nil {
		return err
	}
	request = request.WithContext(ctx)
	if customize != nil {
		customize(request.Request)
	}
	request.Header.Set("Content-Type", "text/xml; charset=utf-8")
	request.Header.Set("SOAPAction", action)
	request.Header.Set("Length", strconv.Itoa(buf.Len()))
	if Log != nil {
		Log("POST", destURL, "header", request.Header, "xml", buf.String())
	}
	to := DefaultCallTimeout
	if dl, ok := ctx.Deadline(); ok {
		if d := time.Until(dl); d > time.Second {
			to = d
		}
	}
	var cl *retryablehttp.Client
	if client != nil {
		cl = httpclient.NewWithClient("url="+destURL, client, 1*time.Second, to, 0.6, Log)
	} else {
		clientsMu.RLock()
		cl, ok := clients[destURL]
		clientsMu.RUnlock()
		if !ok {
			clientsMu.Lock()
			if cl, ok = clients[destURL]; !ok {
				cl = httpclient.NewWithClient("url="+destURL, nil, 1*time.Second, to, 0.6, Log)
				clients[destURL] = cl
			}
			clientsMu.Unlock()
		}
	}
	response, err := cl.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	buf.Reset()
	if response.StatusCode >= 400 {
		io.Copy(buf, response.Body)
		return fmt.Errorf("%s: %w", buf.String(), errors.New(response.Status))
	}

	tr := io.TeeReader(response.Body, buf)
	dec := xml.NewDecoder(tr)
	st, err := FindBody(dec)
	if err != nil {
		return err
	}
	err = dec.DecodeElement(resp, &st)
	if Log == nil {
		return err
	}
	if err != nil {
		io.Copy(ioutil.Discard, tr)
		Log("msg", "response", buf.String(), "decoded", resp, "error", err)
		return err
	}
	respLen := buf.Len()
	respHead, respTail := splitHeadTail(buf.Bytes(), 512)
	buf.Reset()
	fmt.Fprintf(buf, "%#v", resp)
	decHead, decTail := splitHeadTail(buf.Bytes(), 512)
	Log("msg", "response", "resp-length", respLen,
		"resp-head", respHead, "resp-tail", respTail,
		"decoded-length", buf.Len(), "decoded-head", decHead, "decoded-tail", decTail)
	return nil
}

// SOAPCallWithHeader calls with the given SOAP- and extra header and action.
func SOAPCallWithHeader(ctx context.Context,
	destURL string, customize func(req *http.Request),
	action, soapHeader, reqBody string, resp interface{},
	Log func(...interface{}) error,
) error {
	return SOAPCallWithHeaderClient(ctx, nil, destURL, customize, action, soapHeader, reqBody, resp, Log)
}

// SOAPCall destURL with SOAPAction=action, decoding the response body into resp.
func SOAPCall(ctx context.Context, destURL, action string, reqBody string, resp interface{}, Log func(...interface{}) error) error {
	return SOAPCallWithHeader(ctx, destURL, nil, action, "", reqBody, resp, Log)
}

func splitHeadTail(b []byte, length int) (head string, tail string) {
	if n := len(b) / 2; n <= length {
		s := string(b)
		return s[:n], s[n:]
	}
	return string(b[:length]), string(b[len(b)-length:])
}
