// Copyright 2020, 2024 Tamás Gulácsi
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
	"net/http"
	"strconv"
	"time"

	"github.com/klauspost/compress/gzhttp"
	"github.com/rogpeppe/retry"
	"github.com/tgulacsi/go/iohlp"
	"log/slog"
)

var (
	DefaultCallTimeout = time.Minute

	retryStrategy = retry.Strategy{
		Delay:       100 * time.Millisecond,
		MaxDelay:    5 * time.Second,
		MaxDuration: 30 * time.Second,
		Factor:      2,
	}
)

const (
	SOAPHeader = `<?xml version="1.0" encoding="utf-8"?><soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Header>`
	SOAPBody   = `</soapenv:Header><soapenv:Body>`
	SOAPFooter = `</soapenv:Body></soapenv:Envelope>`
)

// SOAPCallWithHeader calls with the given SOAP- and extra header and action.
func SOAPCallWithHeaderClient(ctx context.Context,
	client *http.Client,
	destURL string,
	customizeRequest func(req *http.Request), customizeResponse func(resp *http.Response),
	action, soapHeader, reqBody string, resp interface{},
	logger *slog.Logger,
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

	if client == nil {
		client = http.DefaultClient
	}
	if client.Transport == nil {
		client.Transport = http.DefaultTransport
	}
	client.Transport = gzhttp.Transport(client.Transport)
	retryStrategy := retryStrategy
	if dl, ok := ctx.Deadline(); ok {
		if d := time.Until(dl); d > time.Second {
			retryStrategy.MaxDuration = d
		}
	}
	var response *http.Response
	var dur time.Duration
	var tryCount int
	reqHead, reqTail := splitHeadTail(buf.Bytes(), 1024)
	for iter := retryStrategy.Start(); ; {
		request, err := http.NewRequest("POST", destURL, bytes.NewReader(buf.Bytes()))
		if err != nil {
			return err
		}
		request = request.WithContext(ctx)
		if customizeRequest != nil {
			customizeRequest(request)
		}
		request.Header.Set("Content-Type", "text/xml; charset=utf-8")
		request.Header.Set("SOAPAction", action)
		request.Header.Set("Length", strconv.Itoa(buf.Len()))
		logger.Info("request", "POST", destURL, "header", request.Header, "reqHead", reqHead, "reqTail", reqTail)

		tryCount++
		start := time.Now()
		response, err = client.Do(request)
		dur = time.Since(start)
		if err == nil {
			break
		}
		if !iter.Next(ctx.Done()) {
			return err
		}
	}
	if customizeResponse != nil {
		customizeResponse(response)
	}
	defer response.Body.Close()

	buf.Reset()
	if response.StatusCode >= 400 {
		io.Copy(buf, response.Body)
		return fmt.Errorf("%s: %w", buf.String(), errors.New(response.Status))
	}

	sr, err := iohlp.MakeSectionReader(response.Body, 1<<20)
	if err != nil {
		return err
	}
	dec := xml.NewDecoder(io.NewSectionReader(sr, 0, sr.Size()))
	st, err := FindBody(dec)
	if err != nil {
		return err
	}
	err = dec.DecodeElement(resp, &st)
	if !logger.Enabled(ctx, slog.LevelDebug) {
		return err
	}
	if err != nil {
		buf.Reset()
		io.Copy(buf, sr)
		logger.Error("response", buf.String(), "decoded", resp, "error", err)
		return err
	}
	respLen := sr.Size()
	respHead, respTail := make([]byte, 1024), make([]byte, 1024)
	n, _ := sr.ReadAt(respHead, 0)
	if rest := sr.Size() - int64(n); rest > 0 {
		sr.ReadAt(respTail, sr.Size()-min(rest, int64(cap(respTail))))
	}
	buf.Reset()
	fmt.Fprintf(buf, "%#v", resp)
	decHead, decTail := splitHeadTail(buf.Bytes(), 512)
	logger.Info("response", "resp-length", respLen,
		"resp-head", respHead, "resp-tail", respTail,
		"decoded-length", buf.Len(), "decoded-head", decHead, "decoded-tail", decTail,
		"dur", dur.String(), "try-count", tryCount,
	)
	return nil
}

// SOAPCallWithHeader calls with the given SOAP- and extra header and action.
func SOAPCallWithHeader(ctx context.Context,
	destURL string,
	customizeRequest func(*http.Request), customizeResponse func(*http.Response),
	action, soapHeader, reqBody string, resp interface{},
	logger *slog.Logger,
) error {
	return SOAPCallWithHeaderClient(ctx, nil,
		destURL, customizeRequest, customizeResponse,
		action, soapHeader, reqBody, resp, logger,
	)
}

// SOAPCall destURL with SOAPAction=action, decoding the response body into resp.
func SOAPCall(ctx context.Context, destURL, action string, reqBody string, resp interface{}, logger *slog.Logger) error {
	return SOAPCallWithHeader(ctx, destURL, nil, nil, action, "", reqBody, resp, logger)
}

func splitHeadTail(b []byte, length int) (head string, tail string) {
	if n := len(b) / 2; n <= length {
		s := string(b)
		return s[:n], s[n:]
	}
	return string(b[:length]), string(b[len(b)-length:])
}
