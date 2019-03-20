// Copyright 2019 Tamás Gulácsi
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
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"reflect"
	"time"

	//"regexp"
	"strings"
	"sync"

	"github.com/UNO-SOFT/grpcer"
	"github.com/pkg/errors"
	"golang.org/x/net/html/charset"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var DefaultTimeout = 5 * time.Minute

// SOAPHandler is a http.Handler which proxies SOAP requests to the Client.
// WSDL is served on GET requests.
type SOAPHandler struct {
	grpcer.Client
	WSDL         string
	Log          func(keyvals ...interface{}) error
	Locations    []string
	DecodeInput  func(*string, *xml.Decoder, *xml.StartElement) (interface{}, error)
	EncodeOutput func(*xml.Encoder, interface{}) error
	DecodeHeader func(*xml.Decoder, *xml.StartElement) (func(io.Writer) error, error)

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
	ctx := r.Context()
	Log := h.Log
	if logger, ok := ctx.Value("logger").(interface {
		Log(...interface{}) error
	}); ok {
		Log = logger.Log
	}
	if r.Method == "GET" {
		w.Header().Set("Content-Type", "text/xml")
		io.WriteString(w, h.getWSDL())
		return
	}
	mayFilterEmptyTags(r, Log)

	request, inp, err := h.decodeRequest(r)
	if err != nil {
		Log("msg", "decode", "into", fmt.Sprintf("%T", inp), "error", err)
		switch errors.Cause(err) {
		case errDecode:
			soapError(w, err)
		default:
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
		return
	}

	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	buf.Reset()
	jenc := json.NewEncoder(buf)
	_ = jenc.Encode(inp)
	Log("msg", "Calling", "soapAction", request.SOAPAction, "inp", buf.String())

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
	recv, err := h.Call(request.SOAPAction, ctx, inp, opts...)
	if err != nil {
		Log("call", request.SOAPAction, "inp", fmt.Sprintf("%+v", inp), "error", err)
		soapError(w, err)
		return
	}

	h.encodeResponse(w, recv, request, Log)
}

type requestInfo struct {
	Annotation
	SOAPAction      string
	Prefix, Postfix string
	EncodeHeader    func(w io.Writer) error
}

func (h *SOAPHandler) encodeResponse(w http.ResponseWriter, recv grpcer.Receiver, request requestInfo, Log func(...interface{}) error) {
	w.Header().Set("Content-Type", "text/xml")
	io.WriteString(w, xml.Header)
	io.WriteString(w, `<soap:Envelope
	xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"
	soap:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
`)
	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	if request.EncodeHeader != nil {
		buf.Reset()
		if err := request.EncodeHeader(buf); err != nil {
			Log("EncodeHeader", err)
		} else {
			io.WriteString(w, "<soap:Header>\n")
			w.Write(buf.Bytes())
			io.WriteString(w, "</soap:Header>\n")
		}
	}
	io.WriteString(w, "<soap:Body>\n")
	defer func() { io.WriteString(w, "\n</soap:Body></soap:Envelope>") }()

	part, err := recv.Recv()
	if err != nil {
		Log("recv-error", err)
		encodeSoapFault(w, err)
		return
	}
	typName := strings.TrimPrefix(fmt.Sprintf("%T", part), "*")
	buf.Reset()
	jenc := json.NewEncoder(buf)
	mw := io.MultiWriter(w, buf)
	enc := xml.NewEncoder(mw)
	for {
		buf.Reset()
		_ = jenc.Encode(part)
		Log("recv", buf.String(), "type", typName, "soapAction", request.SOAPAction)
		buf.Reset()
		if request.Raw {
			fmt.Fprintf(mw, "<%s%s_Output%s>", request.Prefix, request.SOAPAction, request.Postfix)
			io.WriteString(mw, reflect.ValueOf(part).Elem().Field(0).String())
			fmt.Fprintf(mw, "</%s%s_Output>", request.Prefix, request.SOAPAction)
		} else if h.EncodeOutput != nil {
			err = h.EncodeOutput(enc, part)
		} else if strings.HasSuffix(typName, "_Output") {
			err = enc.EncodeElement(part,
				xml.StartElement{Name: xml.Name{Local: request.SOAPAction + "_Output"}},
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
}

var (
	errDecode   = errors.New("decode XML")
	errNotFound = errors.New("not found")
)

func (h *SOAPHandler) decodeRequest(r *http.Request) (requestInfo, interface{}, error) {
	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	buf.Reset()

	dec := newXMLDecoder(io.TeeReader(r.Body, buf))
	st, err := findSoapBody(dec)
	if err != nil {
		return requestInfo{}, nil, errors.WithMessage(err, "findSoapBody in "+buf.String())
	}
	request := requestInfo{SOAPAction: strings.Trim(r.Header.Get("SOAPAction"), `"`)}
	if h.DecodeHeader != nil {
		hDec := newXMLDecoder(bytes.NewReader(buf.Bytes()))
		hSt, err := findSoapElt("header", hDec)
		if err != nil {
			h.Log("findSoapHeader", err)
		} else {
			if request.EncodeHeader, err = h.DecodeHeader(hDec, &hSt); err != nil {
				h.Log("DecodeHeader", err, "header", buf.String())
				return request, nil, err
			}
		}
	}

	if i := strings.LastIndex(request.SOAPAction, ".proto/"); i >= 0 {
		request.SOAPAction = request.SOAPAction[i+7:]
	}
	if i := strings.IndexByte(request.SOAPAction, '/'); i >= 0 {
		request.SOAPAction = request.SOAPAction[i+1:]
	}
	request.Annotation = h.annotation(request.SOAPAction)
	h.Log("soapAction", request.SOAPAction, "justRawXML", request.Raw)
	if request.Raw {
		startPos := dec.InputOffset()
		if err = dec.Skip(); err != nil {
			return request, nil, err
		}
		b := bytes.TrimSpace(buf.Bytes()[startPos:dec.InputOffset()])
		b = b[:bytes.LastIndex(b, []byte("</"))]

		rawXML := string(b)
		rawXML = request.TrimInput(rawXML)
		h.Log("prefix", request.Prefix, "postfix", request.Postfix)

		inp := h.Input(request.SOAPAction)
		h.Log("rawXML", rawXML, "inp", inp, "T", fmt.Sprintf("%T", inp))
		reflect.ValueOf(inp).Elem().Field(0).SetString(rawXML)
		return request, inp, nil
	}
	if st, err = nextStart(dec); err != nil {
		return request, nil, errors.WithMessage(err, buf.String())
	}

	if request.SOAPAction == "" {
		request.SOAPAction = st.Name.Local
	}
	var inp interface{}
	if h.DecodeInput != nil {
		inp, err := h.DecodeInput(&request.SOAPAction, dec, &st)
		return request, inp, errors.WithMessage(err, buf.String())
	}

	inp = h.Input(request.SOAPAction)
	if inp == nil {
		if i := strings.LastIndexByte(request.SOAPAction, '/'); i >= 0 {
			if inp = h.Input(request.SOAPAction[i+1:]); inp != nil {
				request.SOAPAction = request.SOAPAction[i+1:]
			}
		}
		if inp == nil {
			return request, nil, errors.Wrapf(errNotFound, "no input for %q", request.SOAPAction)
		}
	}

	if err = dec.DecodeElement(inp, &st); err != nil {
		err = errors.Wrapf(errDecode, "into %T: %v\n%s", inp, err, buf.String())
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
	w.Header().Set("Content-Type", "text/xml")
	switch st := status.Convert(errors.Cause(err)); st.Code() {
	case codes.PermissionDenied, codes.Unauthenticated:
		w.WriteHeader(http.StatusUnauthorized)
	case codes.Unknown:
		if st.Message() == "bad username or password" {
			w.WriteHeader(http.StatusUnauthorized)
		}
	}

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

var _ = xml.Unmarshaler((*DateTime)(nil))
var _ = xml.Marshaler(DateTime{})

type DateTime struct {
	time.Time
}

func (dt *DateTime) UnmarshalXML(dec *xml.Decoder, st xml.StartElement) error {
	var s string
	if err := dec.DecodeElement(&s, &st); err != nil {
		return err
	}
	s = strings.TrimSpace(s)
	n := len(s)
	if n == 0 {
		dt.Time = time.Time{}
		log.Println("time=")
		return nil
	}
	if n > len(time.RFC3339) {
		n = len(time.RFC3339)
	} else if n < 4 {
		n = 4
	}
	var err error
	dt.Time, err = time.Parse(time.RFC3339[:n], s)
	log.Printf("s=%q time=%v err=%+v", s, dt.Time, err)
	return err
}
func (dt DateTime) MarshalXML(enc *xml.Encoder, start xml.StartElement) error {
	return enc.EncodeElement(dt.Time.Format(time.RFC3339), start)
}

// vim: set fileencoding=utf-8 noet:
