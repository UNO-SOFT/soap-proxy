// Copyright 2017 Tamás Gulácsi
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
	"compress/gzip"
	"context"
	"encoding/json"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	//"regexp"
	"strings"
	"sync"
	"time"

	"github.com/UNO-SOFT/grpcer"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
)

// SOAPHandler is a http.Handler which proxies SOAP requests to the Client.
// WSDL is served on GET requests.
type SOAPHandler struct {
	grpcer.Client
	WSDL      string
	Log       func(keyvals ...interface{}) error
	Locations []string
}

//var rEmptyTag = regexp.MustCompile(`<[^>]+/>|<([^ >]+)[^>]*></[^>]+>`)
var bufPool = sync.Pool{New: func() interface{} { return bytes.NewBuffer(make([]byte, 0, 1024)) }}

func (h SOAPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	Log := h.Log
	if logger, ok := r.Context().Value("logger").(interface {
		Log(...interface{}) error
	}); ok {
		Log = logger.Log
	}
	if r.Method == "GET" {
		w.Header().Set("Content-Type", "text/xml")
		if len(h.Locations) == 0 {
			io.WriteString(w, h.WSDL)
			return
		}
		i := strings.LastIndex(h.WSDL, "</port>")
		if i < 0 {
			io.WriteString(w, h.WSDL)
			return
		}
		io.WriteString(w, h.WSDL[:i])
		for _, loc := range h.Locations {
			io.WriteString(w, "<soap:address location=\"")
			xml.EscapeText(w, []byte(loc))
			io.WriteString(w, "\"/>\n")
		}
		io.WriteString(w, h.WSDL[i:])
		return
	}
	soapAction := strings.Trim(r.Header.Get("SOAPAction"), `"`)
	if i := strings.LastIndex(soapAction, ".proto/"); i >= 0 {
		soapAction = soapAction[i+7:]
	}

	if !(r.Header.Get("Keep-Empty-Tags") == "1" || r.URL.Query().Get("keepEmptyTags") == "1") {
		//data = rEmptyTag.ReplaceAll(data, nil)
		save := bufPool.Get().(*bytes.Buffer)
		defer bufPool.Put(save)
		save.Reset()
		buf := bufPool.Get().(*bytes.Buffer)
		defer bufPool.Put(buf)
		buf.Reset()
		if err := FilterEmptyTags(buf, io.TeeReader(r.Body, save)); err != nil {
			Log("FilterEmptyTags", save.String(), "error", err)
			r.Body = struct {
				io.Reader
				io.Closer
			}{io.MultiReader(bytes.NewReader(save.Bytes()), r.Body), r.Body}
		} else {
			r.Body = struct{
				io.Reader
				io.Closer
			}{bytes.NewReader(buf.Bytes()), r.Body}
		}
	}

	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	buf.Reset()

	dec := xml.NewDecoder(io.TeeReader(r.Body, buf))
	st, err := FindBody(dec)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if soapAction == "" {
		soapAction = st.Name.Local
	}
	inp := h.Input(soapAction)
	if inp == nil {
		if i := strings.LastIndexByte(soapAction, '/'); i >= 0 {
			if inp = h.Input(soapAction[i+1:]); inp != nil {
				soapAction = soapAction[i+1:]
			}
		}
		if inp == nil {
			http.Error(w, fmt.Sprintf("no input for %q", soapAction), http.StatusBadRequest)
			return
		}
	}
	if err := dec.DecodeElement(inp, &st); err != nil {
		Log("decode", buf.String(), "into", fmt.Sprintf("%T", inp), "error", err)
		soapError(w, errors.Wrapf(err, "Decode into %T\n%s", inp, buf.String()))
		return
	}
	buf.Reset()
	jenc := json.NewEncoder(buf)
	_ = jenc.Encode(inp)
	Log("msg", "Calling", "soapAction", soapAction, "inp", buf.String())

	var opts []grpc.CallOption
	ctx := context.Background()
	if u, p, ok := r.BasicAuth(); ok {
		ctx = grpcer.WithBasicAuth(ctx, u, p)
	}
	ctx, cancel := context.WithTimeout(ctx, 1*time.Minute)
	defer cancel()
	recv, err := h.Call(soapAction, ctx, inp, opts...)
	if err != nil {
		Log("call", soapAction, "inp", inp, "error", err)
		soapError(w, err)
		return
	}

	w.Header().Set("Content-Type", "text/xml")
	fmt.Fprintf(w, xml.Header+`<soap:Envelope
	xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"
	soap:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
<soap:Body>
`)
	defer func() { io.WriteString(w, "\n</soap:Body></soap:Envelope>") }()
	part, err := recv.Recv()
	if err != nil {
		Log("recv-error", err)
		encodeSoapFault(w, err)
		return
	}
	for {
		buf.Reset()
		_ = jenc.Encode(part)
		Log("recv", buf.String())
		if err := xml.NewEncoder(w).Encode(part); err != nil {
			Log("msg", "encode", "error", err, "part", part)
			break
		}
		w.Write([]byte{'\n'})
		if part, err = recv.Recv(); err != nil {
			if err != io.EOF {
				Log("msg", "recv", "error", err)
			}
			break
		}
	}
}

func soapError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "text/xml")
	io.WriteString(w, xml.Header+`<soap:Envelope
	xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"
	soap:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
<soap:Body>`)
	encodeSoapFault(w, err)
	io.WriteString(w, `</soap:Body></soap:Envelope>`)
}
func encodeSoapFault(w io.Writer, err error) error {
	var code string
	if c, ok := errors.Cause(err).(interface {
		Code() int
	}); ok {
		code = fmt.Sprintf("%d", c.Code())
	}
	io.WriteString(w, `
<soap:Fault>
<Code>`+code+`</Code><Reason>`)
	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	buf.Reset()
	xml.EscapeText(buf, []byte(err.Error()))
	w.Write(bytes.Replace(buf.Bytes(), []byte("&#xA;"), []byte{'\n'}, -1))
	io.WriteString(w, `</Reason><Detail>`)
	xml.EscapeText(w, []byte(fmt.Sprintf("%+v", err)))
	_, err = io.WriteString(w, `</Detail>
</soap:Fault>
`)
	return err
}

// FindBody will find the first StartElement in soap:Body
func FindBody(dec *xml.Decoder) (xml.StartElement, error) {
	var st xml.StartElement
	var state uint8
	for {
		tok, err := dec.Token()
		if err != nil {
			return st, err
		}
		var ok bool
		st, ok = tok.(xml.StartElement)
		if ok {
			if state == 0 && strings.EqualFold(st.Name.Local, "body") {
				state++
				continue
			} else if state == 1 {
				return st, nil
			}
		} else if _, ok = tok.(xml.EndElement); ok && state == 1 {
			return st, io.EOF
		}
	}
	return st, io.EOF
}

// Ungzb64 decodes-decompresses the given gzipped-base64-encoded string.
// Esp. useful for reading the WSDLgzb64 from protoc-gen-wsdl embedded WSDL strings.
func Ungzb64(s string) string {
	br := base64.NewDecoder(base64.StdEncoding, strings.NewReader(s))
	gr, err := gzip.NewReader(br)
	if err != nil {
		panic(err)
	}
	b, err := ioutil.ReadAll(gr)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func FilterEmptyTags(w io.Writer, r io.Reader) error {
	dec := xml.NewDecoder(r)
	enc := xml.NewEncoder(w)
	var unwritten []xml.Token
	Unwrite := func() error {
		var err error
		for _, t := range unwritten {
			if err = enc.EncodeToken(t); err != nil {
				break
			}
		}
		unwritten = unwritten[:0]
		return err
	}
	for {
		tok, err := dec.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		switch tok.(type) {
		case xml.Comment, xml.Directive, xml.ProcInst:
			if err := enc.EncodeToken(tok); err != nil {
				return err
			}
			continue

		case xml.StartElement:
			if len(unwritten) != 0 {
				Unwrite()
			}
			unwritten = append(unwritten, tok)

		case xml.EndElement:
			if len(unwritten) != 0 {
				if _, ok := unwritten[len(unwritten)-1].(xml.StartElement); ok {
					unwritten = unwritten[:len(unwritten)-1]
					continue
				}
				Unwrite()
			}
			enc.EncodeToken(tok)

		case xml.CharData:
			if len(unwritten) == 0 {
				enc.EncodeToken(tok)
				continue
			}
			if _, ok := unwritten[len(unwritten)-1].(xml.StartElement); ok {
				Unwrite()
				enc.EncodeToken(tok)
			} else {
				unwritten = append(unwritten, tok)
			}
		}
	}
	Unwrite()
	return enc.Flush()
}

// vim: set fileencoding=utf-8 noet:
