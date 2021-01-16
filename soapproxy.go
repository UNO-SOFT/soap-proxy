// Copyright 2019, 2021 Tamás Gulácsi
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
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"reflect"
	"time"

	//"regexp"
	"strings"
	"sync"

	"github.com/UNO-SOFT/grpcer"
	//"github.com/UNO-SOFT/otel"
	"github.com/klauspost/compress/zstd"

	"golang.org/x/net/html/charset"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var DefaultTimeout = 5 * time.Minute

const textXML = "text/xml; charset=utf-8"

// SOAPHandler is a http.Handler which proxies SOAP requests to the Client.
// WSDL is served on GET requests.
type SOAPHandler struct {
	grpcer.Client
	WSDL         string
	Log          func(keyvals ...interface{}) error
	Locations    []string
	DecodeInput  func(*string, *xml.Decoder, *xml.StartElement) (interface{}, error)
	EncodeOutput func(*xml.Encoder, interface{}) error
	DecodeHeader func(context.Context, *xml.Decoder, *xml.StartElement) (context.Context, func(context.Context, io.Writer, error) error, error)

	Timeout           time.Duration
	wsdlWithLocations string
	annotations       map[string]Annotation
}

type Annotation struct {
	Raw      bool
	RemoveNS bool
}

func (h *SOAPHandler) Input(name string) interface{} {
	if inp := h.Client.Input(name); inp != nil {
		return inp
	}
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return h.Client.Input(name[i+1:])
	}
	return nil
}

var bufPool = sync.Pool{New: func() interface{} { return bytes.NewBuffer(make([]byte, 0, 1024)) }}

func (h *SOAPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	//ctx := otel.ExtractHTTP(r.Context(), r.Header)
	ctx := r.Context()
	Log := h.Log
	if logger, ok := ctx.Value("logger").(interface {
		Log(...interface{}) error
	}); ok {
		Log = logger.Log
	}
	if r.Method == "GET" {
		w.Header().Set("Content-Type", textXML)
		io.WriteString(w, h.getWSDL())
		return
	}
	mayFilterEmptyTags(r, Log)

	request, inp, err := h.decodeRequest(ctx, r)
	if err != nil {
		Log("msg", "decode", "into", fmt.Sprintf("%T", inp), "error", err)
		if errors.Is(err, errDecode) {
			soapError(w, err)
		} else {
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
		return
	}

	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	buf.Reset()
	jenc := json.NewEncoder(buf)
	_ = jenc.Encode(inp)
	Log("msg", "Calling", "soapAction", request.Action, "inp", buf.String())

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
	if err != nil {
		Log("call", request.Action, "inp", fmt.Sprintf("%+v", inp), "error", err)
		soapError(w, err)
		return
	}

	h.encodeResponse(ctx, w, recv, request, Log)
}

type requestInfo struct {
	Annotation
	Action, SOAPAction string
	Prefix, Postfix    string
	EncodeHeader       func(context.Context, io.Writer, error) error
}

const (
	soapEnvelopeHeader = xml.Header + `
<SOAP-ENV:Envelope
	xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/"
	xmlns:xsi="http://www.w3.org/1999/XMLSchema-instance"
	xmlns:xsd="http://www.w3.org/1999/XMLSchema"
	SOAP-ENV:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
`
	soapEnvelopeFooter = `
</SOAP-ENV:Body></SOAP-ENV:Envelope>`
)

func (h *SOAPHandler) encodeResponse(ctx context.Context, w http.ResponseWriter, recv grpcer.Receiver, request requestInfo, Log func(...interface{}) error) {
	w.Header().Set("Content-Type", textXML)
	io.WriteString(w, soapEnvelopeHeader)

	part, recvErr := recv.Recv()
	next, nextErr := recv.Recv()

	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	if request.EncodeHeader != nil {
		buf.Reset()
		if hdrErr := request.EncodeHeader(ctx, buf, recvErr); hdrErr != nil {
			Log("EncodeHeader", hdrErr)
		} else {
			io.WriteString(w, "<SOAP-ENV:Header>\n")
			w.Write(buf.Bytes())
			io.WriteString(w, "</SOAP-ENV:Header>\n")
		}
	}
	io.WriteString(w, "<SOAP-ENV:Body>\n")
	defer func() { io.WriteString(w, soapEnvelopeFooter) }()

	if recvErr != nil {
		Log("recv-error", recvErr)
		encodeSoapFault(w, recvErr)
		return
	}
	typName := strings.TrimPrefix(fmt.Sprintf("%T", part), "*")
	buf.Reset()
	shouldMerge := !request.Raw && h.EncodeOutput == nil && nextErr == nil
	var slice, notSlice []grpcer.Field
	if shouldMerge {
		slice, notSlice = grpcer.SliceFields(part, "xml")
	}
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
				io.WriteString(mw, reflect.ValueOf(part).Elem().Field(0).String())
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
			Log("recv-xml", buf.String())
			if err != nil {
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
		if request.Raw {
			fmt.Fprintf(mw, "</%s%s_Output>", request.Prefix, request.Action)
		}
		return
	}

	// Merge slices
	enc := xml.NewEncoder(buf)
	ss := sliceSaver{files: make(map[string]fileWithTag, len(slice)), buf: buf, enc: enc}
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
			//Log("tp", tp, "rv", rv, "kid", rv.Kind())
			for _, f := range notSlice {
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
				Log("msg", "encode zero", "value", rv.Interface(), "error", err)
				break
			}
			b := buf.Bytes()
			_, end, ok := findOuterTag(b)
			if !ok {
				Log("msg", "no findOuterTag", "b", string(b))
				break
			}
			if _, err = w.Write(b[:end[0]]); err != nil {
				Log("msg", "write", "error", err)
				break
			}
			w.Write([]byte{'\n'})
			suffix := string(b[end[0]:end[1]])
			defer io.WriteString(w, suffix)
		}

		// Encode slice fields, each into its separate file.
		rv := reflect.ValueOf(part)
		if rv.Kind() == reflect.Ptr {
			rv = rv.Elem()
		}
		for _, f := range slice {
			rf := rv.FieldByName(f.Name)
			if rf.IsZero() || rf.Len() == 0 {
				continue
			}
			if err := ss.Encode(f.Name, rf.Interface()); err != nil {
				Log("msg", "encodeSliceField", "field", f.Name, "error", err)
				break
			}
		}

		if next != nil {
			part, err, next = next, nextErr, nil
		} else {
			part, err = recv.Recv()
		}
		if err != nil {
			if err != io.EOF {
				Log("msg", "recv", "error", err)
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

	Log("msg", "copy", "files", ss.files)
	for _, nm := range fieldOrder {
		tag := ss.files[nm].Tag
		r, err := ss.GetReader(nm)
		if err != nil {
			Log("msg", "GetReader", "file", nm, "error", err)
			continue
		}
		_, err = io.Copy(w, r)
		r.Close()
		if err != nil {
			Log("msg", "copy", "file", nm, "error", err)
		}
		io.WriteString(w, "</"+tag+">\n")
	}
}

type fileWithTag struct {
	io.WriteCloser
	File *os.File
	Tag string
}

type sliceSaver struct {
	buf   *bytes.Buffer
	files map[string]fileWithTag
	enc   *xml.Encoder
}

func (ss sliceSaver) Close() error {
	for k, f := range ss.files {
		delete(ss.files, k)
		if f.WriteCloser != nil {
			f.WriteCloser.Close()
		}
		if f.File != nil {
			f.File.Close()
			os.Remove(f.File.Name())
		}
	}
	return nil
}

func (ss sliceSaver) Encode(name string, value interface{}) error {
	ss.buf.Reset()
	err := ss.enc.Encode(value)
	if err != nil {
		return err
	}
	b := ss.buf.Bytes()
	start, end, ok := findOuterTag(b)
	if !ok {
		return fmt.Errorf("no outer tag found in %q", string(b))
	}

	fh, ok := ss.files[name]
	if ok {
		_, err = fh.Write(append(b[start[1]+1:end[0]], '\n'))
	} else {
		if fh.File, err = ioutil.TempFile("", name+"-*.xml.zst"); err != nil {
			return err
		}
		fh.Tag = string(b[end[0]+2 : end[1]-1])
		os.Remove(fh.File.Name())
		ss.files[name] = fh
		if fh.WriteCloser, err = zstd.NewWriter(fh.File, zstd.WithEncoderLevel(zstd.SpeedFastest)); err !=nil {
			return err
		}
		ss.files[name] = fh
		_, err = fh.Write(append(b[:end[0]], '\n'))
	}
	return err
}
func (ss sliceSaver) GetReader(name string) (io.ReadCloser, error) {
	fh := ss.files[name]
	delete(ss.files, name)
	if err := fh.WriteCloser.Close(); err != nil {
		fh.File.Close()
		return nil, fmt.Errorf("close writer: %w", err)
	}
		if _, err := fh.File.Seek(0, 0); err != nil {
			fh.File.Close()
			return nil, fmt.Errorf("seek: %w", err)
		}
		r, err := zstd.NewReader(fh.File)
		if err != nil {
			fh.File.Close()
			return nil , err
		}
		return struct{
			io.Reader
			io.Closer
		}{r, closerFunc(func() error { r.Close(); return fh.File.Close(); })}, nil
	}

var (
	errDecode   = errors.New("decode XML")
	errNotFound = errors.New("not found")
)

func (h *SOAPHandler) decodeRequest(ctx context.Context, r *http.Request) (requestInfo, interface{}, error) {
	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	buf.Reset()

	dec := newXMLDecoder(io.TeeReader(r.Body, buf))
	st, err := findSoapBody(dec)
	if err != nil {
		return requestInfo{}, nil, fmt.Errorf("findSoapBody in %s: %w", buf.String(), err)
	}
	request := requestInfo{SOAPAction: strings.Trim(r.Header.Get("SOAPAction"), `"`)}
	if h.DecodeHeader != nil {
		hDec := newXMLDecoder(bytes.NewReader(buf.Bytes()))
		hSt, err := findSoapElt("header", hDec)
		if err != nil {
			h.Log("findSoapHeader", err)
		} else {
			if _, request.EncodeHeader, err = h.DecodeHeader(ctx, hDec, &hSt); err != nil {
				h.Log("DecodeHeader", err, "header", buf.String())
				return request, nil, fmt.Errorf("decodeHeader: %w", err)
			}
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
	h.Log("soapAction", request.Action, "justRawXML", request.Raw)
	if request.Raw {
		startPos := dec.InputOffset()
		if err = dec.Skip(); err != nil {
			return request, nil, fmt.Errorf("skip: %w", err)
		}
		b := bytes.TrimSpace(buf.Bytes()[startPos:dec.InputOffset()])
		b = b[:bytes.LastIndex(b, []byte("</"))]

		rawXML := string(b)
		rawXML = request.TrimInput(rawXML)
		h.Log("prefix", request.Prefix, "postfix", request.Postfix)

		inp := h.Input(request.Action)
		h.Log("rawXML", rawXML, "inp", inp, "T", fmt.Sprintf("%T", inp))
		reflect.ValueOf(inp).Elem().Field(0).SetString(rawXML)
		return request, inp, nil
	}
	if st, err = nextStart(dec); err != nil && !errors.Is(err, io.EOF) {
		return request, nil, fmt.Errorf("nextStart: %s: %w", buf.String(), err)
	}

	if request.Action == "" {
		request.Action = st.Name.Local
	}
	var inp interface{}
	if h.DecodeInput != nil {
		inp, err := h.DecodeInput(&request.Action, dec, &st)
		if err != nil {
			return request, inp, fmt.Errorf("%s: %w", buf.String(), err)
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
			return request, nil, fmt.Errorf("no input for %q: %w", request.Action, errNotFound)
		}
	}

	if err = dec.DecodeElement(inp, &st); err != nil {
		if errors.Is(err, io.EOF) {
			if t := reflect.TypeOf(inp).Elem(); t.Kind() == reflect.Struct && t.NumField() == 0 {
				return request, inp, nil
			} else {
				h.Log("inp", fmt.Sprintf("%T", inp))
			}
		} else {
			h.Log("ERROR", err)
		}

		err = fmt.Errorf("into %T: %v\n%s: %w", inp, err, buf.String(), errDecode)
	}
	return request, inp, err
}

func (h *SOAPHandler) getWSDL() string {
	if h.wsdlWithLocations != "" {
		return h.wsdlWithLocations
	}
	h.wsdlWithLocations = h.WSDL
	if len(h.Locations) < 0 {
		return h.wsdlWithLocations
	}
	i := strings.LastIndex(h.WSDL, "</port>")
	if i < 0 {
		return h.wsdlWithLocations
	}
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
	return h.wsdlWithLocations
}

func (h *SOAPHandler) annotation(soapAction string) (annotation Annotation) {
	defer func() {
		if h == nil || h.Log == nil {
			return
		}
		h.Log("soapAction", soapAction, "annotation", annotation)
	}()

	get := func(soapAction string) Annotation {
		var ok bool
		if annotation, ok = h.annotations[soapAction]; ok {
			return annotation
		}
		if i := strings.LastIndexByte(soapAction, '/'); i >= 0 {
			annotation = h.annotations[soapAction[i+1:]]
		}
		return annotation
	}
	if h.annotations != nil {
		return get(soapAction)
	}

	h.annotations = make(map[string]Annotation)
	dec := newXMLDecoder(strings.NewReader(h.WSDL))
	stack := make([]xml.StartElement, 0, 8)
	names := make(map[string]struct{})
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		//h.Log("tok", fmt.Sprintf("%T:%+v", tok, tok))
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
						h.Log("msg", "parse", "documentation", string(x), "error", err)
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
	return get(soapAction)
}

func mayFilterEmptyTags(r *http.Request, Log func(...interface{}) error) {
	if !(r.Header.Get("Keep-Empty-Tags") == "1" || r.URL.Query().Get("keepEmptyTags") == "1") {
		//data = rEmptyTag.ReplaceAll(data, nil)
		save := bufPool.Get().(*bytes.Buffer)
		defer bufPool.Put(save)
		save.Reset()
		buf := bufPool.Get().(*bytes.Buffer)
		defer bufPool.Put(buf)
		buf.Reset()
		if err := FilterEmptyTags(buf, io.TeeReader(r.Body, save)); err != nil {
			if Log != nil {
				Log("FilterEmptyTags", save.String(), "error", err)
			}
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

	encodeSoapFault(w, err)
}
func encodeSoapFault(w http.ResponseWriter, err error) error {
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
		fault.Code = "SOAP-ENV:Server"
		if code < 500 {
			fault.Code = "SOAP-ENV:Client"
		}
	}
	w.Header().Set("Content-Type", textXML)
	w.WriteHeader(code)

	io.WriteString(w, soapEnvelopeHeader+`<SOAP-ENV:Body>`)
	err = xml.NewEncoder(w).Encode(fault)
	io.WriteString(w, soapEnvelopeFooter)
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
			if strings.EqualFold(st.Name.Local, name) &&
				(st.Name.Space == "" ||
					st.Name.Space == "SOAP-ENV" ||
					st.Name.Space == "http://www.w3.org/2003/05/soap-envelope/" ||
					st.Name.Space == "http://schemas.xmlsoap.org/soap/envelope/") {
				return st, nil
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
	b, err := ioutil.ReadAll(gr)
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
	XMLName xml.Name `xml:"SOAP-ENV:Fault"`
	Code    string   `xml:"faultcode,omitempty"`
	String  string   `xml:"faultstring,omitempty"`
	Actor   string   `xml:"faultactor,omitempty"`
	Detail  string   `xml:"detail>ExceptionDetail,omitempty"`
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
func trimOuterTag(b []byte) (string, []byte) {
	start, end, ok := findOuterTag(b)
	if !ok {
		return "", b
	}
	return string(b[end[0]+2 : end[1]-1]), b[start[1]+1 : end[0]-1]
}

type closerFunc func() error 
func(f closerFunc) Close() error { return f() }

// vim: set fileencoding=utf-8 noet:
