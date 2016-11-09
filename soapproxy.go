package soapproxy

import (
	"compress/gzip"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	context "golang.org/x/net/context"

	"github.com/UNO-SOFT/grpcer"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
)

// SOAPHandler is a http.Handler which proxies SOAP requests to the Client.
// WSDL is served on GET requests.
type SOAPHandler struct {
	grpcer.Client
	WSDL string
}

func (h SOAPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	if r.Method == "GET" {
		w.Header().Set("Content-Type", "application/xml")
		io.WriteString(w, h.WSDL)
		return
	}
	soapAction := r.Header.Get("SOAPAction")
	if i := strings.LastIndex(soapAction, ".proto/"); i >= 0 {
		soapAction = soapAction[i+7:]
	}
	dec := xml.NewDecoder(r.Body)
	st, err := findBody(dec)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
	inp := h.Input(soapAction)
	if inp == nil {
		http.Error(w, fmt.Sprintf("no input for %q", soapAction), http.StatusBadRequest)
		return
	}
	if err := dec.DecodeElement(&inp, &st); err != nil {
		soapError(w, errors.Wrapf(err, "Decode into %T", inp))
		return
	}

	var opts []grpc.CallOption
	ctx := context.Background()
	if u, p, ok := r.BasicAuth(); ok {
		ctx = grpcer.WithBasicAuth(ctx, u, p)
	}
	ctx, cancel := context.WithTimeout(ctx, 1*time.Minute)
	defer cancel()
	out, err := h.Call(soapAction, ctx, inp, opts...)
	if err != nil {
		soapError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/soap+xml")
	fmt.Fprintf(w, xml.Header+`<soap:Envelope
	xmlns:soap="http://www.w3.org/2003/05/soap-envelope/"
	soap:encodingStyle="http://www.w3.org/2003/05/soap-encoding">
<soap:Body>
`)
	xml.NewEncoder(w).Encode(out)
	io.WriteString(w, "\n</soap:Body></soap:Envelope>")
}

func soapError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/soap+xml")
	io.WriteString(w, xml.Header+`<soap:Envelope
	xmlns:soap="http://www.w3.org/2003/05/soap-envelope/"
	soap:encodingStyle="http://www.w3.org/2003/05/soap-encoding">
<soap:Body><soap:Fault>
<Code></Code><Reason>`)
	xml.EscapeText(w, []byte(err.Error()))
	io.WriteString(w, `</Reason><Detail>`)
	xml.EscapeText(w, []byte(fmt.Sprintf("%+v", err)))
	io.WriteString(w, `</Detail>
</soap:Fault></soap:Body></soap:Envelope>`)
}

// findBody will find the first StartElement in soap:Body
func findBody(dec *xml.Decoder) (xml.StartElement, error) {
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
