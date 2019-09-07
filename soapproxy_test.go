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
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	errors "golang.org/x/xerrors"

	"github.com/UNO-SOFT/grpcer"
	"github.com/tgulacsi/oracall/custom"
	"google.golang.org/grpc"
)

func TestRawXML(t *testing.T) {
	b, err := ioutil.ReadFile(filepath.Join("testdata", "withAny.wsdl"))
	if err != nil {
		t.Fatal(err)
	}
	h := &SOAPHandler{WSDL: string(b)}
	if !h.annotation("DbWebGdpr_Keres").Raw {
		t.Error("DbWebGdpdr_Keres: wanted true, got false")
	}
	if h.annotation("DbWebGdpr_Kereses").Raw {
		t.Error("DbWebGdpdr_Kereses: wanted false, got true")
	}
}

func TestParseAny(t *testing.T) {
	var buf bytes.Buffer
	dec := xml.NewDecoder(io.TeeReader(strings.NewReader(xml.Header+`<soap:Envelope
xmlns:soap="http://www.w3.org/2003/05/soap-envelope/"
soap:encodingStyle="http://www.w3.org/2003/05/soap-encoding">

<soap:Body>
  <m:GetPrice xmlns:m="http://www.w3schools.com/prices">
    <m:Item>Apples</m:Item>
  </m:GetPrice>
</soap:Body>

</soap:Envelope>`), &buf))
	st, err := findSoapBody(dec)
	if err != nil {
		t.Fatal(err)
	}
	type anyXML struct {
		RawXML string `xml:",innerxml"`
	}
	var any anyXML
	if err := dec.DecodeElement(&any, &st); err != nil {
		t.Error(errors.Errorf("into %T: %v\n%s: %w", any, err, buf.String(), errDecode))
	}
	t.Logf("any=%#v", any)
}

func TestSOAPParse(t *testing.T) {
	st, err := FindBody(xml.NewDecoder(strings.NewReader(xml.Header + `<soap:Envelope
xmlns:soap="http://www.w3.org/2003/05/soap-envelope/"
soap:encodingStyle="http://www.w3.org/2003/05/soap-encoding"
xmlns:head="http://services.aegon.hu/Header/">
   <soap:Header>
      <head:IMSSOAPHeader>
         <AuthToken>authToken</AuthToken>
         <ConnectionStyle>SYNCHRONOUS</ConnectionStyle>
         <SystemId>103</SystemId>
         <ActualUserId>0000917174</ActualUserId>
         <RequestDateTime></RequestDateTime>
         <ResponseDateTime></ResponseDateTime>
         <TechnicalUserId>FrontEnd</TechnicalUserId>
         <ResponseCode></ResponseCode>
         <ResponseDescription></ResponseDescription>
         <TargetServiceID>KKNYR</TargetServiceID>
         <TargetServiceVersion>1.0</TargetServiceVersion>
         <TransactionID>tran-sact-ion-ID</TransactionID>
         <ParentTransactionID></ParentTransactionID>
      </head:IMSSOAPHeader>
   </soap:Header>
<soap:Body>
  <m:GetPrice xmlns:m="http://www.w3schools.com/prices">
    <m:Item>Apples</m:Item>
  </m:GetPrice>
</soap:Body>

</soap:Envelope>`)))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("st=%#v", st)
	if st.Name.Local != "GetPrice" {
		t.Errorf("Got %s, wanted m:GetPrice", st)
	}
}

func TestXMLDecode(t *testing.T) {

	type Login_Input struct {
		PLoginNev string `protobuf:"bytes,1,opt,name=p_login_nev,json=pLoginNev,proto3" json:"p_login_nev,omitempty"`
		PJelszo   string `protobuf:"bytes,2,opt,name=p_jelszo,json=pJelszo,proto3" json:"p_jelszo,omitempty"`
		PAddr     string `protobuf:"bytes,3,opt,name=p_addr,json=pAddr,proto3" json:"p_addr,omitempty"`
	}
	dec := xml.NewDecoder(strings.NewReader(loginRequest))
	st, err := FindBody(dec)
	if err != nil {
		t.Fatal(err)
	}
	var inp Login_Input
	if err := dec.DecodeElement(&inp, &st); err != nil {
		t.Errorf("Decode into %T: %v", inp, err)
	}
	log.Printf("Decoded: %#v", inp)
	if inp.PLoginNev == "" {
		t.Errorf("empty struct: %#v", inp)
	}
}

type nullClient struct{}

func (n nullClient) List() []string { return nil }
func (n nullClient) Input(name string) interface{} {
	switch name {
	case "Login":
		var x struct{ PLoginNev, PJelszo string }
		return &x
	}
	return nil
}
func (n nullClient) Call(name string, ctx context.Context, input interface{}, opts ...grpc.CallOption) (grpcer.Receiver, error) {
	return nil, nil
}

func TestDecodeRequest(t *testing.T) {
	h := SOAPHandler{
		Client: nullClient{},
		Log: func(keyvals ...interface{}) error {
			var buf strings.Builder
			for i := 0; i < len(keyvals); i += 2 {
				fmt.Fprintf(&buf, "%s=%+v ", keyvals[i], keyvals[i+1])
			}
			t.Log(buf.String())
			return nil
		},

		DecodeHeader: func(ctx context.Context, dec *xml.Decoder, st *xml.StartElement) (context.Context, func(context.Context, io.Writer) error, error) {
			var hdr struct {
				IMSSOAPHeader struct {
					XMLName                                                                  xml.Name `xml:"IMSSOAPHeader"`
					AuthToken                                                                string   `xml:"AuthToken"`
					SystemID                                                                 string   `xml:"SystemId"`
					ActualUserID                                                             string   `xml:"ActualUserId"`
					RequestDateTime, ResponseDateTime                                        custom.DateTime
					TechnicalUserId                                                          string `xml:"TechnicalUserID"`
					ResponseCode, ResponseDescription, TargetServiceID, TargetServiceVersion string
					TransactionID, ParentTransactionID                                       string
				}
			}
			if err := dec.DecodeElement(&hdr, st); err != nil {
				return ctx, nil, errors.Errorf("decode %v: %w", st, err)
			}
			return ctx, func(ctx context.Context, w io.Writer) error {
				hdr.IMSSOAPHeader.ResponseDateTime.Time = time.Now()
				if err := xml.NewEncoder(w).Encode(hdr.IMSSOAPHeader); err != nil {
					return errors.Errorf("encode header: %w", err)
				}
				return nil
			}, nil
		},
	}
	req, err := http.NewRequest("POST", "http://example.com", strings.NewReader(loginRequest))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("SOAPAction", "Login")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	info, part, err := h.decodeRequest(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("part: %#v", part)
	t.Logf("info: %#v", info)
	var buf strings.Builder
	if err := info.EncodeHeader(ctx, &buf); err != nil {
		t.Error(err)
	}
	t.Log("hdr:", buf.String())
}

const loginRequest = `<?xml version="1.0" encoding="utf-8"?><soap:Envelope xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema" xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"
xmlns:head="http://services.aegon.hu/Header/">
   <soap:Header>
      <head:IMSSOAPHeader>
         <AuthToken>authToken</AuthToken>
         <ConnectionStyle>SYNCHRONOUS</ConnectionStyle>
         <SystemId>103</SystemId>
         <ActualUserId>0000917174</ActualUserId>
		 <RequestDateTime>2006-01-02T15:04:05Z</RequestDateTime>
         <ResponseDateTime></ResponseDateTime>
         <TechnicalUserId>FrontEnd</TechnicalUserId>
         <ResponseCode></ResponseCode>
         <ResponseDescription></ResponseDescription>
         <TargetServiceID>KKNYR</TargetServiceID>
         <TargetServiceVersion>1.0</TargetServiceVersion>
         <TransactionID>tran-sact-ion-ID</TransactionID>
         <ParentTransactionID></ParentTransactionID>
      </head:IMSSOAPHeader>
   </soap:Header>
<soap:Body><DbDealer_Login><PLoginNev>b0917174</PLoginNev><PJelszo>b0917174</PJelszo></DbDealer_Login></soap:Body></soap:Envelope>`

func TestRemoveNS(t *testing.T) {
	const rawXML = `<gdpr:GDPRRequest xmlns:gdpr="http://aegon.hu/exampl/GDPR">
   <GDPR_REQUEST_HEADER>
         <SystemID>BIZTALK</SystemID>
         <RequestID>44206876</RequestID>
         <RequestDate>2018-01-01</RequestDate>
         <RequestType>SEARCH</RequestType>
         <DataSubjectType>CUSTOMER</DataSubjectType>
         <TargetSystemID>123</TargetSystemID>
         <TransactionID>3A8159D452460018E0530A41F02437D12</TransactionID>
   </GDPR_REQUEST_HEADER>
   <PERSON_ITEMS>
<GDPRPerson><StartDate>1953-04-20</StartDate><LastName>Andras</LastName><FirstName>Huszti</FirstName><BirthCity>BUDAPEST</BirthCity><MotherLastName>Kiss Gizi</MotherLastName></GDPRPerson>

<GDPRPerson><ClaimList><GDPRClaim><ID>KKCL-009027228T-1</ID></GDPRClaim></ClaimList></GDPRPerson>
<GDPRPerson><EMailAddressList><GDPREMailAddress><Text>hajdumarcsi.580303@googmail.hu</Text></GDPREMailAddress></EMailAddressList><SimplifiedContractList><GDPRSimplifiedContract><ContractId>365128</ContractId></GDPRSimplifiedContract></SimplifiedContractList></GDPRPerson>
<GDPRPerson><EMailAddressList><GDPREMailAddress><Text>muranyi.jozsef@hdsnet.hu</Text></GDPREMailAddress></EMailAddressList><SimplifiedContractList><GDPRSimplifiedContract><ContractId>31685769</ContractId></GDPRSimplifiedContract></SimplifiedContractList></GDPRPerson>
</PERSON_ITEMS>
</gdpr:GDPRRequest>`

	request := requestInfo{Annotation: Annotation{Raw: true, RemoveNS: true}}
	got := request.TrimInput(rawXML)

	if want := strings.Replace(rawXML, "gdpr:GDPRRequest>", "GDPRRequest>", 2); want != got {
		t.Skipf("got\n%s\nwanted\n%s", got, want)
	}

	got2 := request.TrimInput(got)
	if got2 != got {
		t.Errorf("got %s\n, wanted %s", got2, got)
	}
}

// vim: set fileencoding=utf-8 noet:
