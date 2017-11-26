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
	"encoding/xml"
	"log"
	"strings"
	"testing"
)

func TestSOAPParse(t *testing.T) {
	st, err := FindBody(xml.NewDecoder(strings.NewReader(xml.Header + `<soap:Envelope
xmlns:soap="http://www.w3.org/2003/05/soap-envelope/"
soap:encodingStyle="http://www.w3.org/2003/05/soap-encoding">

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
	dec := xml.NewDecoder(strings.NewReader(`<?xml version="1.0" encoding="utf-8"?><soap:Envelope xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema" xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><DbDealer_Login><PLoginNev>b0917174</PLoginNev><PJelszo>b0917174</PJelszo></DbDealer_Login></soap:Body></soap:Envelope>`))
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

// vim: set fileencoding=utf-8 noet:
