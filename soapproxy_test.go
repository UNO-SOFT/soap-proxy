package soapproxy

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestSOAPParse(t *testing.T) {
	st, err := findBody(xml.NewDecoder(strings.NewReader(xml.Header + `<soap:Envelope
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
