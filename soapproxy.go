// Copyright 2019, 2023 Tamás Gulácsi
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
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/UNO-SOFT/grpcer"
	"github.com/UNO-SOFT/otel"
	"github.com/klauspost/compress/gzhttp"
	"github.com/tgulacsi/go/iohlp"

	"golang.org/x/net/html/charset"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var DefaultTimeout = 5 * time.Minute

const textXML = "text/xml; charset=utf-8"

// SOAPHandlerConfig is the configuration for NewSOAPHandler
type SOAPHandlerConfig struct {
	grpcer.Client `json:"-"`
	*slog.Logger  `json:"-"`
	GetLogger     func(ctx context.Context) *slog.Logger
	DecodeInput   func(*string, *xml.Decoder, *xml.StartElement) (interface{}, error)                                                            `json:"-"`
	EncodeOutput  func(*xml.Encoder, interface{}) error                                                                                          `json:"-"`
	DecodeHeader  func(context.Context, *xml.Decoder, *xml.StartElement) (context.Context, func(context.Context, io.Writer, error) error, error) `json:"-"`
	LogRequest    func(context.Context, string, error)
	WSDL          string
	Locations     []string
	Timeout       time.Duration
}

func (c SOAPHandlerConfig) getLogger(ctx context.Context) *slog.Logger {
	if c.GetLogger != nil {
		if lgr := c.GetLogger(ctx); lgr != nil {
			return lgr
		}
	}
	return c.Logger
}

// soapHandler is a http.Handler which proxies SOAP requests to the Client.
// WSDL is served on GET requests.
type soapHandler struct {
	SOAPHandlerConfig
	annotations       map[string]Annotation `json:"-"`
	wsdlWithLocations string                `json:"-"`
}

func NewSOAPHandler(config SOAPHandlerConfig) soapHandler {
	h := soapHandler{SOAPHandlerConfig: config}

	if h.Logger == nil {
		h.Logger = slog.Default()
	}

	// init wsdlWithLocations
	h.wsdlWithLocations = h.WSDL
	if len(h.Locations) != 0 {
		if i := strings.LastIndex(h.WSDL, "</port>"); i >= 0 {
			var buf strings.Builder
			buf.WriteString(h.WSDL[:i])
			for _, loc := range h.Locations {
				loc = strings.Trim(loc, `"`)
				buf.WriteString(`<soap:address location="`)
				_ = xml.EscapeText(&buf, []byte(loc))
				buf.WriteString("\" />\n")
			}
			buf.WriteString(h.WSDL[i:])
			h.wsdlWithLocations = buf.String()
		}
	}

	// init annotations
	h.annotations = make(map[string]Annotation)
	dec := newXMLDecoder(strings.NewReader(h.WSDL))
	stack := make([]xml.StartElement, 0, 8)
	names := make(map[string]struct{})
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch x := tok.(type) {
		case xml.StartElement:
			if x.Name.Local == "any" && (x.Name.Space == "" || x.Name.Space == "http://www.w3.org/2001/XMLSchema") {
				if len(stack) >= 3 && stack[len(stack)-1].Name.Local == "sequence" && stack[len(stack)-2].Name.Local == "complexType" && stack[len(stack)-3].Name.Local == "element" {
					for _, attr := range stack[len(stack)-3].Attr {
						if attr.Name.Local == "name" {
							names[attr.Value] = struct{}{}
						}
					}
				}
			}
			stack = append(stack, x)
		case xml.CharData:
			if len(stack) > 1 && stack[len(stack)-1].Name.Local == "documentation" {
				x = bytes.TrimSpace(x)
				if bytes.HasPrefix(x, []byte{'{'}) {
					m := make(map[string]Annotation)
					if err = json.NewDecoder(bytes.NewReader(x)).Decode(&m); err != nil {
						h.Error("parse", "documentation", string(x), "error", err)
					} else {
						if h.annotations == nil {
							h.annotations = m
						} else {
							for k, v := range m {
								h.annotations[k] = v
							}
						}
					}
				}
			}
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		}
	}
	for nm := range names {
		k := strings.TrimSuffix(nm, "_Input")
		if k != nm {
			if _, ok := names[k+"_Output"]; ok {
				a := h.annotations[k]
				a.Raw = true
				h.annotations[k] = a
			}
		}
	}

	return h
}

type Annotation struct {
	Raw      bool
	RemoveNS bool
}

func (h soapHandler) Input(name string) interface{} {
	if inp := h.Client.Input(name); inp != nil {
		return inp
	}
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return h.Client.Input(name[i+1:])
	}
	return nil
}

var bufPool = sync.Pool{New: func() interface{} { return bytes.NewBuffer(make([]byte, 0, 1024)) }}

func (h soapHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	gzhttp.GzipHandler(http.HandlerFunc(h.serveHTTP)).ServeHTTP(w, r)
}
func (h soapHandler) serveHTTP(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	ctx := otel.ExtractHTTP(r.Context(), r.Header)
	logger := h.getLogger(ctx)
	if r.Method == "GET" {
		body := h.getWSDL()
		w.Header().Set("Content-Type", textXML)
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		// nosemgrep: go.lang.security.audit.xss.no-io-writestring-to-responsewriter.no-io-writestring-to-responsewriter
		io.WriteString(w, body)
		return
	}
	mayFilterEmptyTags(r, logger)

	rI, inp, err := h.DecodeRequest(ctx, r)
	r.Body.Close()
	if err != nil {
		logger.Error("decode", "into", fmt.Sprintf("%T", inp), "error", err)
		if errors.Is(err, errDecode) {
			soapError(w, err)
		} else {
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
		return
	}
	request := rI.(requestInfo)

	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	buf.Reset()
	jenc := json.NewEncoder(buf)
	_ = jenc.Encode(inp)
	logger.Info("Calling", "soapAction", request.Action, "inp", buf.String())

	var opts []grpc.CallOption
	if u, p, ok := r.BasicAuth(); ok {
		ctx = grpcer.WithBasicAuth(ctx, u, p)
	}
	if _, ok := ctx.Deadline(); !ok {
		timeout := h.Timeout
		if timeout == 0 {
			timeout = DefaultTimeout
		}
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
	}
	recv, err := h.Call(request.Action, ctx, inp, opts...)
	if h.LogRequest != nil {
		h.LogRequest(ctx, buf.String(), err)
	}
	if err != nil {
		logger.Error("call", "action", request.Action, "inp", fmt.Sprintf("%+v", inp), "error", err)
		soapError(w, err)
		return
	}

	h.encodeResponse(ctx, w, recv, request)
}

type requestInfo struct {
	EncodeHeader       func(context.Context, io.Writer, error) error
	Action, SOAPAction string
	Prefix, Postfix    string
	Annotation
	ForbidMerge bool
}

func (info requestInfo) Name() string { return info.Action }

const (
	prefix             = "soapenv"
	soapEnvelopeURI    = "http://schemas.xmlsoap.org/soap/envelope/"
	soapEnvelopeHeader = xml.Header + `<` + prefix + `:Envelope
	xmlns:` + prefix + "=" + soapEnvelopeURI + `
	xmlns:xsi="http://www.w3.org/1999/XMLSchema-instance"
	xmlns:xsd="http://www.w3.org/1999/XMLSchema"
	` + prefix + `:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
`
	soapEnvelopeFooter = `
</` + prefix + `:Body></` + prefix + `:Envelope>`
)

func (h soapHandler) encodeResponse(ctx context.Context, w http.ResponseWriter, recv grpcer.Receiver, request requestInfo) {
	logger := h.getLogger(ctx)
	w.Header().Set("Content-Type", textXML)
	// nosemgrep: go.lang.security.audit.xss.no-io-writestring-to-responsewriter.no-io-writestring-to-responsewriter
	io.WriteString(w, soapEnvelopeHeader)

	part, recvErr := recv.Recv()
	next, nextErr := recv.Recv()
	if recvErr != nil || nextErr != nil && !errors.Is(nextErr, io.EOF) {
		logger.Error("encodeResponse", "recvErr", recvErr, "nextErr", nextErr)
	} else {
		logger.Debug("encodeResponse", "recvErr", recvErr, "nextErr", nextErr)
	}

	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	if request.EncodeHeader != nil {
		buf.Reset()
		buf.WriteString("<" + prefix + ":Header>\n")
		hdrErr := request.EncodeHeader(ctx, buf, recvErr)
		buf.WriteString("</" + prefix + ":Header>\n")
		if hdrErr != nil {
			logger.Error("EncodeHeader", "error", hdrErr)
		} else {
			// nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter
			w.Write(buf.Bytes())
		}
	}
	io.WriteString(w, "<"+prefix+":Body>\n")
	defer func() { io.WriteString(w, soapEnvelopeFooter) }()

	if recvErr != nil {
		logger.Error("recv-error", "error", recvErr)
		encodeSoapFault(w, recvErr, true)
		return
	}
	if nextErr != nil && !errors.Is(nextErr, io.EOF) {
		logger.Error("next-error", "error", nextErr)
		encodeSoapFault(w, nextErr, true)
		return
	}
	typName := strings.TrimPrefix(fmt.Sprintf("%T", part), "*")
	buf.Reset()
	shouldMerge := !request.Raw && h.EncodeOutput == nil && nextErr == nil && !request.ForbidMerge
	var slice, notSlice []grpcer.Field
	if shouldMerge {
		slice, notSlice = grpcer.SliceFields(part, "xml")
	}
	logger.Info("mayMerge", "shouldMerge", shouldMerge, "slice", slice)
	if len(slice) == 0 {
		// Nothing to merge
		mw := io.MultiWriter(w, buf)
		enc := xml.NewEncoder(mw)
		if request.Raw {
			fmt.Fprintf(mw, "<%s%s_Output%s>", request.Prefix, request.Action, request.Postfix)
		}
		for {
			buf.Reset()
			var err error
			if request.Raw {
				// Use the first exported field
				rv := reflect.ValueOf(part).Elem()
				rt := rv.Type()
				for i := 0; i < rt.NumField(); i++ {
					if rt.Field(i).IsExported() {
						io.WriteString(mw, rv.Field(i).String())
						break
					}
				}
			} else if h.EncodeOutput != nil {
				err = h.EncodeOutput(enc, part)
			} else if strings.HasSuffix(typName, "_Output") {
				space := request.SOAPAction
				if i := strings.LastIndex(space, ".proto/"); i >= 0 {
					if j := strings.Index(space[i+7:], "/"); j >= 0 {
						space = space[:i+7+j] + "_types/"
					}
				}
				err = enc.EncodeElement(part,
					xml.StartElement{Name: xml.Name{Local: request.Action + "_Output", Space: space}},
				)
			} else {
				err = enc.Encode(part)
			}
			logger.Debug("found", "recv-xml", buf.String())
			if err != nil {
				logger.Error("encode", "part", part, "error", err)
				break
			}
			// nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter
			w.Write([]byte{'\n'})
			if part, err = recv.Recv(); err != nil {
				if !errors.Is(err, io.EOF) {
					logger.Error("recv", "error", err)
				}
				break
			}
		}
		if request.Raw {
			fmt.Fprintf(mw, "</%s%s_Output>", request.Prefix, request.Action)
		}
		return
	}

	// Merge slices
	enc := xml.NewEncoder(buf)
	ss := sliceSaver{files: make(map[string]*grpcer.TempFile, len(slice)), buf: buf, enc: enc}
	fieldOrder := make([]string, 0, 2*len(slice))
	for _, f := range slice {
		fieldOrder = append(fieldOrder, f.Name)
	}
	defer ss.Close()
	for {
		buf.Reset()
		var err error
		if next != nil { // first
			// Encode a "part" with only the non-slice elements filled,
			// strip & save the closing tag.
			tp := reflect.TypeOf(part)
			if tp.Kind() == reflect.Ptr {
				tp = tp.Elem()
			}
			rv := reflect.New(tp).Elem()
			for _, f := range notSlice {
				// nosemgrep: go.lang.security.audit.unsafe-reflect-by-name.unsafe-reflect-by-name
				rv.FieldByName(f.Name).Set(reflect.ValueOf(f.Value))
			}
			buf.Reset()
			if strings.HasSuffix(typName, "_Output") {
				space := request.SOAPAction
				if i := strings.LastIndex(space, ".proto/"); i >= 0 {
					if j := strings.Index(space[i+7:], "/"); j >= 0 {
						space = space[:i+7+j] + "_types/"
					}
				}
				err = enc.EncodeElement(rv.Interface(),
					xml.StartElement{Name: xml.Name{Local: request.Action + "_Output", Space: space}},
				)
			} else {
				err = enc.Encode(rv.Interface())
			}
			if err != nil {
				logger.Error("encode zero", "value", rv.Interface(), "error", err)
				break
			}
			b := buf.Bytes()
			_, end, ok := findOuterTag(b)
			if !ok {
				logger.Info("no findOuterTag", "b", string(b))
				break
			}
			// nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter
			if _, err = w.Write(b[:end[0]]); err != nil {
				logger.Error("write", "error", err)
				break
			}
			// nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter
			w.Write([]byte{'\n'})
			suffix := string(b[end[0]:end[1]])
			// nosemgrep: go.lang.security.audit.xss.no-io-writestring-to-responsewriter.no-io-writestring-to-responsewriter
			defer io.WriteString(w, suffix)
		}

		// Encode slice fields, each into its separate file.
		rv := reflect.ValueOf(part)
		if rv.Kind() == reflect.Ptr {
			rv = rv.Elem()
		}
		for _, f := range slice {
			// nosemgrep: go.lang.security.audit.unsafe-reflect-by-name.unsafe-reflect-by-name
			rf := rv.FieldByName(f.Name)
			if rf.IsZero() || rf.Len() == 0 {
				continue
			}
			if err := ss.Encode(f.Name, rf.Interface()); err != nil {
				logger.Error("encodeSliceField", "field", f.Name, "error", err)
				break
			}
		}

		if next != nil {
			part, err, next = next, nextErr, nil
		} else {
			part, err = recv.Recv()
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				logger.Error("recv", "error", err)
			}
			break
		}
		slice, notSlice = grpcer.SliceFields(part, "xml")
		for _, f := range slice {
			var found bool
			for _, nm := range fieldOrder {
				if found = f.Name == nm; found {
					break
				}
			}
			if !found {
				fieldOrder = append(fieldOrder, f.Name)
			}
		}
	}

	logger.Info("copy", "files", ss.files)
	for _, nm := range fieldOrder {
		rc, err := ss.files[nm].GetReader()
		if err != nil {
			logger.Error("GetReader", "file", nm, "error", err)
			continue
		}
		_, err = io.Copy(w, rc)
		rc.Close()
		if err != nil {
			logger.Error("copy", "file", nm, "error", err)
		}
	}
}

type sliceSaver struct {
	buf   *bytes.Buffer
	files map[string]*grpcer.TempFile
	enc   *xml.Encoder
}

func (ss sliceSaver) Close() error {
	for k, f := range ss.files {
		delete(ss.files, k)
		if f != nil {
			f.Close()
		}
	}
	return nil
}

func (ss sliceSaver) Encode(name string, value interface{}) error {
	ss.buf.Reset()
	err := ss.enc.EncodeElement(value, xml.StartElement{Name: xml.Name{Local: name}})
	if err != nil {
		return err
	}
	ss.buf.WriteByte('\n')
	b := ss.buf.Bytes()

	fh := ss.files[name]
	if fh == nil {
		if fh, err = grpcer.NewTempFile("", name+"-*.xml.zst"); err != nil {
			return err
		}
		ss.files[name] = fh
	}
	_, err = fh.Write(b)
	return err
}

var (
	errDecode = errors.New("decode XML")
)

func (h soapHandler) DecodeRequest(ctx context.Context, r *http.Request) (grpcer.RequestInfo, interface{}, error) {
	logger := h.getLogger(ctx)

	sr, err := iohlp.MakeSectionReader(r.Body, 1<<20)
	if err != nil {
		return requestInfo{}, nil, err
	}

	dec := newXMLDecoder(io.NewSectionReader(sr, 0, sr.Size()))
	st, err := findSoapBody(dec)
	if err != nil {
		b, _ := grpcer.ReadHeadTail(sr, 1024)
		return requestInfo{}, nil, fmt.Errorf("findSoapBody in %s: %w", string(b), err)
	}
	request := requestInfo{SOAPAction: strings.Trim(r.Header.Get("SOAPAction"), `"`)}
	request.ForbidMerge, _ = strconv.ParseBool(r.Header.Get("Forbid-Merge"))
	if h.DecodeHeader != nil {
		hDec := newXMLDecoder(io.NewSectionReader(sr, 0, sr.Size()))
		hSt, err := findSoapElt("header", hDec)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				logger.Error("findSoapHeader", "error", err)
			}
		} else if hSt.Name.Local != "" {
			_, encHeader, err := h.DecodeHeader(ctx, hDec, &hSt)
			if err != nil {
				b, _ := grpcer.ReadHeadTail(sr, 1024)
				logger.Error("DecodeHeader", "header", string(b), "error", err)
				return request, nil, fmt.Errorf("decodeHeader: %w", err)
			}
			request.EncodeHeader = encHeader
		}
	}

	request.Action = request.SOAPAction
	if i := strings.LastIndex(request.Action, ".proto/"); i >= 0 {
		request.Action = request.Action[i+7:]
	}
	if i := strings.IndexByte(request.Action, '/'); i >= 0 {
		request.Action = request.Action[i+1:]
	}
	request.Annotation = h.annotation(request.Action)
	logger.Info("request", "soapAction", request.Action, "justRawXML", request.Raw)
	if request.Raw {
		startPos := dec.InputOffset()
		if err = dec.Skip(); err != nil {
			return request, nil, fmt.Errorf("skip: %w", err)
		}
		b := make([]byte, dec.InputOffset()-startPos)
		n, _ := sr.ReadAt(b, int64(startPos))
		b = b[:n]
		b = b[:bytes.LastIndex(b, []byte("</"))]

		rawXML := string(b)
		rawXML = request.TrimInput(rawXML)
		logger.Info("raw", "prefix", request.Prefix, "postfix", request.Postfix)

		inp := h.Input(request.Action)
		logger.Info("raw", "rawXML", rawXML, "inp", fmt.Sprintf("%#v", inp), "T", fmt.Sprintf("%T", inp))
		rv := reflect.ValueOf(inp).Elem()
		rt := rv.Type()
		for i := 0; i < rt.NumField(); i++ {
			if rt.Field(i).IsExported() {
				rv.Field(i).SetString(rawXML)
				break
			}
		}
		return request, inp, nil
	}
	if st, err = nextStart(dec); err != nil && !errors.Is(err, io.EOF) {
		b, _ := grpcer.ReadHeadTail(sr, 1024)
		return request, nil, fmt.Errorf("nextStart: %s: %w", string(b), err)
	}

	if request.Action == "" {
		request.Action = st.Name.Local
	}
	var inp interface{}
	if h.DecodeInput != nil {
		inp, err := h.DecodeInput(&request.Action, dec, &st)
		if err != nil {
			b, _ := grpcer.ReadHeadTail(sr, 1024)
			return request, inp, fmt.Errorf("%s: %w", string(b), err)
		}
		return request, inp, nil
	}

	inp = h.Input(request.Action)
	if inp == nil {
		if i := strings.LastIndexByte(request.Action, '/'); i >= 0 {
			if inp = h.Input(request.Action[i+1:]); inp != nil {
				request.Action = request.Action[i+1:]
			}
		}
		if inp == nil {
			return request, nil, fmt.Errorf("no input for %q: %w", request.Action, grpcer.ErrNotFound)
		}
	}

	if err = dec.DecodeElement(inp, &st); err != nil {
		if errors.Is(err, io.EOF) {
			if t := reflect.TypeOf(inp).Elem(); t.Kind() == reflect.Struct && t.NumField() == 0 {
				return request, inp, nil
			} else {
				logger.Info("EOF", "inp", fmt.Sprintf("%T", inp))
			}
		} else {
			logger.Error("ERROR", "error", err)
		}

		b, _ := grpcer.ReadHeadTail(sr, 1024)
		err = fmt.Errorf("into %T: %v\n%s: %w", inp, err, string(b), errDecode)
	}
	return request, inp, err
}

func (h soapHandler) getWSDL() string { return h.wsdlWithLocations }

func (h soapHandler) annotation(soapAction string) (annotation Annotation) {
	defer func() {
		h.Info("annotations ends", "soapAction", soapAction, "annotation", annotation)
	}()

	var ok bool
	if annotation, ok = h.annotations[soapAction]; ok {
		return annotation
	}
	if i := strings.LastIndexByte(soapAction, '/'); i >= 0 {
		annotation = h.annotations[soapAction[i+1:]]
	}
	return annotation
}

func mayFilterEmptyTags(r *http.Request, logger *slog.Logger) {
	if !(r.Header.Get("Keep-Empty-Tags") == "1" || r.URL.Query().Get("keepEmptyTags") == "1") {
		//data = rEmptyTag.ReplaceAll(data, nil)
		save := bufPool.Get().(*bytes.Buffer)
		defer bufPool.Put(save)
		save.Reset()
		buf := bufPool.Get().(*bytes.Buffer)
		defer bufPool.Put(buf)
		buf.Reset()
		if err := FilterEmptyTags(buf, io.TeeReader(r.Body, save)); err != nil {
			logger.Info("FilterEmptyTags", "read", save.String(), "error", err)
			r.Body = struct {
				io.Reader
				io.Closer
			}{io.MultiReader(bytes.NewReader(save.Bytes()), r.Body), r.Body}
		} else {
			r.Body = struct {
				io.Reader
				io.Closer
			}{bytes.NewReader(buf.Bytes()), r.Body}
		}
	}
}

func soapError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", textXML)
	switch st := status.Convert(errors.Unwrap(err)); st.Code() {
	case codes.PermissionDenied, codes.Unauthenticated:
		w.WriteHeader(http.StatusUnauthorized)
	case codes.Unknown:
		if st.Message() == "bad username or password" {
			w.WriteHeader(http.StatusUnauthorized)
		}
	}

	encodeSoapFault(w, err, false)
}
func encodeSoapFault(w http.ResponseWriter, err error, justInner bool) error {
	code := http.StatusInternalServerError
	var c interface {
		Code() int
	}
	if errors.As(err, &c) {
		code = c.Code()
	} else if errors.Is(err, context.Canceled) {
		code = http.StatusFailedDependency
	} else if errors.Is(err, context.DeadlineExceeded) {
		code = http.StatusGatewayTimeout
	}
	// https://www.tutorialspoint.com/soap/soap_fault.html
	fault := SOAPFault{String: err.Error(), Detail: fmt.Sprintf("%+v", err)}
	var f interface {
		FaultCode() string
		FaultString() string
	}
	if errors.As(err, &f) {
		fault.Code, fault.String = f.FaultCode(), f.FaultString()
		var ok bool
		if i := strings.LastIndexByte(fault.Code, ':'); i >= 0 {
			switch fault.Code[i+1:] {
			case "VersionMismatch", "MustUnderstand", "Client", "Server":
				ok = true
			}
		}
		if !ok {
			fault.Code = ""
		}
	}
	if fault.Code == "" {
		fault.Code = prefix + ":Server"
		if code < 500 {
			fault.Code = prefix + ":Client"
		}
	}
	w.Header().Set("Content-Type", textXML)
	var buf bytes.Buffer
	if !justInner {
		io.WriteString(&buf, soapEnvelopeHeader+"<"+prefix+":Body>")
	}

	err = xml.NewEncoder(&buf).Encode(fault)
	if !justInner {
		io.WriteString(&buf, soapEnvelopeFooter)

		w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	}
	w.WriteHeader(code)

	// nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter
	w.Write(buf.Bytes())
	return err
}

// FindBody will find the first StartElement after soap:Body.
func FindBody(dec *xml.Decoder) (xml.StartElement, error) {
	st, err := findSoapBody(dec)
	if err != nil {
		return st, err
	}
	return nextStart(dec)
}

// findSoapBody will find the soap:Body StartElement.
func findSoapBody(dec *xml.Decoder) (xml.StartElement, error) {
	return findSoapElt("body", dec)
}

func findSoapElt(name string, dec *xml.Decoder) (xml.StartElement, error) {
	var st xml.StartElement
	for {
		tok, err := dec.Token()
		if err != nil {
			return st, err
		}
		var ok bool
		if st, ok = tok.(xml.StartElement); ok {
			if strings.EqualFold(st.Name.Local, name) {
				switch st.Name.Space {
				case "", "SOAP-ENV", prefix,
					"http://www.w3.org/2003/05/soap-envelope/",
					soapEnvelopeURI:
					return st, nil
				}
			}
		}
	}
}

// nextStart finds the first StartElement
func nextStart(dec *xml.Decoder) (xml.StartElement, error) {
	var st xml.StartElement
	for {
		tok, err := dec.Token()
		if err != nil {
			return st, err
		}
		var ok bool
		if st, ok = tok.(xml.StartElement); ok {
			return st, nil
		} else if _, ok = tok.(xml.EndElement); ok {
			return st, io.EOF
		}
	}
}

// Ungzb64 decodes-decompresses the given gzipped-base64-encoded string.
// Esp. useful for reading the WSDLgzb64 from protoc-gen-wsdl embedded WSDL strings.
func Ungzb64(s string) string {
	br := base64.NewDecoder(base64.StdEncoding, strings.NewReader(s))
	gr, err := gzip.NewReader(br)
	if err != nil {
		panic(err)
	}
	b, err := io.ReadAll(gr)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func FilterEmptyTags(w io.Writer, r io.Reader) error {
	dec := newXMLDecoder(r)
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
			if errors.Is(err, io.EOF) {
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

func (request *requestInfo) TrimInput(rawXML string) string {
	rawXML = strings.TrimSpace(rawXML)
	dec := xml.NewDecoder(strings.NewReader(rawXML))
	var st xml.StartElement
	var err error
	for !strings.HasSuffix(st.Name.Local, "_Input") {
		if st, err = nextStart(dec); err != nil {
			log.Println(err)
			break
		}
	}
	attrs := make([]xml.Attr, 0, len(st.Attr))
	if err == nil {
		attrs = append(attrs, st.Attr...)
		endPos := dec.InputOffset()

		nextStart(dec)
		dec.Skip()
		rawXML = strings.TrimSpace(rawXML[endPos:dec.InputOffset()])
	}

	if !request.RemoveNS {
		return rawXML
	}
	var buf strings.Builder
	var globalNS xml.Attr
	otherNS := make([]xml.Attr, 0, len(attrs))
	for _, attr := range attrs {
		if attr.Name.Space == "_xmlns" {
			attr.Name.Local, attr.Name.Space = "xmlns", attr.Name.Local
		}
		if attr.Name.Local != "xmlns" {
			continue
		}
		if attr.Name.Space == "" {
			globalNS = attr
		} else {
			otherNS = append(otherNS, attr)
		}
	}

	if len(otherNS) == 0 && globalNS.Value != "" {
		otherNS = append(otherNS, globalNS)
	}
	for _, attr := range otherNS {
		if attr.Name.Space != "" && request.Prefix == "" {
			request.Prefix = attr.Name.Space + ":"
		}
		buf.WriteByte(' ')
		nm := strings.TrimSuffix(attr.Name.Local+":"+attr.Name.Space, ":")
		buf.WriteString(nm)
		buf.WriteString(`="`)
		xml.EscapeText(&buf, []byte(attr.Value))
		buf.WriteByte('"')
	}
	request.Postfix = buf.String()
	return rawXML
}

func newXMLDecoder(r io.Reader) *xml.Decoder {
	dec := xml.NewDecoder(r)
	dec.CharsetReader = charset.NewReaderLabel
	return dec
}

// SOAPFault fault
type SOAPFault struct {
	XMLName xml.Name `xml:"Fault"`
	Code    string   `xml:"faultcode"`
	String  string   `xml:"faultstring"`
	Actor   string   `xml:"faultactor,omitempty"`
	Detail  string   `xml:"detail>ExceptionDetail,omitempty"`
}

func (f SOAPFault) MarshalXML(enc *xml.Encoder, st xml.StartElement) error {
	st.Name.Space = ""
	if i := strings.IndexByte(st.Name.Local, ':'); i < 0 {
		st.Name.Local = prefix + ":Fault"
	} else if st.Name.Local[i:] != ":Fault" {
		st.Name.Local = st.Name.Local[:i+1] + "Fault"
	}
	for i := 0; i < len(st.Attr); i++ {
		if a := st.Attr[i]; a.Name.Local == "xmlns" { // delete
			st.Attr[i] = st.Attr[len(st.Attr)-1]
			st.Attr = st.Attr[:len(st.Attr)-1]
			i--
		}
	}
	st.Attr = append(st.Attr, xml.Attr{
		Name:  xml.Name{Local: "xmlns:" + prefix},
		Value: soapEnvelopeURI,
	})
	var err error
	E := func(tok xml.Token) error {
		if err == nil {
			err = enc.EncodeToken(tok)
		}
		return err
	}
	S := func(name, value string) error {
		E(xml.StartElement{Name: xml.Name{Local: name}})
		E(xml.CharData(value))
		return E(xml.EndElement{Name: xml.Name{Local: name}})
	}
	E(st)
	S("faultcode", f.Code)
	S("faultstring", f.String)
	if f.Actor != "" {
		S("faultactor", f.Actor)
	}
	if f.Detail != "" {
		E(xml.StartElement{Name: xml.Name{Local: "detail"}})
		E(xml.StartElement{Name: xml.Name{Local: "ExceptionDetail"}})
		E(xml.CharData(f.Detail))
		E(xml.EndElement{Name: xml.Name{Local: "ExceptionDetail"}})
		E(xml.EndElement{Name: xml.Name{Local: "detail"}})
	}
	return E(xml.EndElement{Name: st.Name})
}

func findOuterTag(b []byte) (start, end [2]int, ok bool) {
	off := bytes.IndexByte(b, '<')
	if off < 0 {
		return start, end, false
	}
	start[0] = off + 1
	b = b[off:]
	i := bytes.IndexByte(b, '>')
	if i < 0 {
		return start, end, false
	}
	start[1] = off + i
	var tag []byte
	if j := bytes.IndexByte(b[:i], ' '); j < 0 {
		tag = b[1:i]
	} else {
		tag = b[1:j]
	}
	j := bytes.LastIndex(b, append(append(make([]byte, 0, 2+len(tag)), '<', '/'), tag...))
	if j < 0 {
		return start, end, false
	}
	end[0], end[1] = off+j, off+j+2+len(tag)+1
	return start, end, true
}
