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

package main

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"path"
	"strings"
	"sync"
	"text/template"
	"unicode"

	"github.com/golang/protobuf/protoc-gen-go/descriptor"
	protoc "github.com/golang/protobuf/protoc-gen-go/plugin"
	"golang.org/x/sync/errgroup"
)

const wsdlTmpl = xml.Header + `<definitions
	name="{{.Package}}"
	targetNamespace="{{.TargetNS}}"
	xmlns:tns="{{.TargetNS}}"
	xmlns:types="{{.TypesNS}}"
	xmlns="http://schemas.xmlsoap.org/wsdl/"
	xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
	xmlns:soapenc="http://schemas.xmlsoap.org/soap/encoding/"
	xmlns:wsdl="http://schemas.xmlsoap.org/wsdl/"
	xmlns:xsd="http://www.w3.org/2001/XMLSchema">
  <types>
    <xsd:schema elementFormDefault="qualified" targetNamespace="{{.TargetNS}}">
      <xsd:element name="exception">
        <xsd:complexType>
          <xsd:sequence>
            <xsd:element name="type" nillable="true" type="xsd:string"/>
            <xsd:element name="message" nillable="true" type="xsd:string"/>
            <xsd:element name="traceback" nillable="true" type="xsd:string"/>
          </xsd:sequence>
        </xsd:complexType>
      </xsd:element>
      {{range $k, $m := .Types}}
	    {{mkType $k $m}}
	  {{end}}
	</xsd:schema>
  </types>
  <message name="error">
    <part element="types:exception" name="error"/>
  </message>
  <portType name="{{.Package}}">
    {{range .GetMethod}}
    <operation name="{{.GetName}}">
      <input message="tns:{{mkTypeName .GetInputType}}"/>
      <output message="tns:{{mkTypeName .GetOutputType}}"/>
      <fault message="tns:error" name="error"/>
    </operation>
	{{end}}
  </portType>
  <binding name="{{.Package}}_soap" type="tns:{{.Package}}">
    <soap:binding transport="http://schemas.xmlsoap.org/soap/http"/>
	{{range .GetMethod}}<operation name="{{.Name}}">
      <soap:operation soapAction="{{$.TargetNS}}{{.GetName}}" style="document"/>
      <input><soap:body use="literal"/></input>
      <output><soap:body use="literal"/></output>
      <fault name="error"><soap:fault name="error" use="literal"/></fault>
    </operation>{{end}}
  </binding>
  <service name="{{.Package}}__service">
    <port binding="tns:{{.Package}}_soap" name="web">
      <soap:address location="http://localhost:12469/portal/"/>
    </port>
  </service>
</definitions>
`

func Generate(resp *protoc.CodeGeneratorResponse, req protoc.CodeGeneratorRequest) error {
	destPkg := req.GetParameter()
	if destPkg == "" {
		destPkg = "main"
	}
	// Find roots.
	rootNames := req.GetFileToGenerate()
	files := req.GetProtoFile()
	roots := make(map[string]*descriptor.FileDescriptorProto, len(rootNames))
	allTypes := make(map[string]*descriptor.DescriptorProto, 1024)
	var found int
	for i := len(files) - 1; i >= 0; i-- {
		f := files[i]
		for _, m := range f.GetMessageType() {
			allTypes["."+f.GetPackage()+"."+m.GetName()] = m
		}
		if found == len(rootNames) {
			continue
		}
		for _, root := range rootNames {
			if f.GetName() == root {
				roots[root] = files[i]
				found++
				break
			}
		}
	}

	msgTypes := make(map[string]*descriptor.DescriptorProto, len(allTypes))
	for _, root := range roots {
		//k := "." + root.GetName() + "."
		var k string
		for _, svc := range root.GetService() {
			for _, m := range svc.GetMethod() {
				if kk := k + m.GetInputType(); len(kk) > len(k) {
					msgTypes[kk] = allTypes[kk]
				}
				if kk := k + m.GetOutputType(); len(kk) > len(k) {
					msgTypes[kk] = allTypes[kk]
				}
			}
		}
	}

	// delete not needed message types
	wsdlTemplate := template.Must(template.
		New("wsdl").
		Funcs(template.FuncMap{
			"mkTypeName": mkTypeName,
			"mkType":     typer{Types: allTypes}.mkType,
		}).
		Parse(wsdlTmpl))

	// Service, bind and message types from roots.
	// All other types are from imported dependencies, recursively.
	type whole struct {
		*descriptor.ServiceDescriptorProto
		Package, TargetNS, TypesNS string
		Types                      map[string]*descriptor.DescriptorProto
	}

	var grp errgroup.Group
	var mu sync.Mutex
	resp.File = make([]*protoc.CodeGeneratorResponse_File, 0, len(roots))
	for _, root := range roots {
		root := root
		pkg := root.GetName()
		for _, svc := range root.GetService() {
			grp.Go(func() error {
				data := whole{
					Package:  svc.GetName(),
					TargetNS: "http://" + pkg + "/" + svc.GetName() + "/",
					TypesNS:  "http://" + pkg + "/" + svc.GetName() + "_types/",
					Types:    msgTypes,

					ServiceDescriptorProto: svc,
				}
				destFn := strings.TrimSuffix(path.Base(pkg), ".proto") + ".wsdl"
				buf := bufPool.Get().(*bytes.Buffer)
				buf.Reset()
				defer func() {
					buf.Reset()
					bufPool.Put(buf)
				}()
				err := wsdlTemplate.Execute(buf, data)
				content := buf.String()
				mu.Lock()
				if err != nil {
					errS := err.Error()
					resp.Error = &errS
				}
				resp.File = append(resp.File,
					&protoc.CodeGeneratorResponse_File{
						Name:    &destFn,
						Content: &content,
					})
				// also, embed the wsdl
				goFn := destFn + ".go"
				goContent := `package ` + destPkg + `

// WSDLgzb64 contains the WSDL, gzipped and base64-encoded.
// You can easily read it with soapproxy.Ungzb64.
const WSDLgzb64 = ` + "`" + gzb64(content) + "`\n"
				resp.File = append(resp.File,
					&protoc.CodeGeneratorResponse_File{
						Name:    &goFn,
						Content: &goContent,
					})
				mu.Unlock()
				return nil
			})
		}
	}
	return grp.Wait()
}

var bufPool = sync.Pool{New: func() interface{} { return bytes.NewBuffer(make([]byte, 0, 4096)) }}

var typeTemplate = template.Must(
	template.New("type").
		Funcs(template.FuncMap{
			"mkXSDElement": mkXSDElement,
		}).
		Parse(`
<xsd:element name="{{.Name}}">
  <xsd:complexType>
    <xsd:all>
	{{range .Fields}}{{mkXSDElement .}}
	{{end}}
    </xsd:all>
  </xsd:complexType>
</xsd:element>
`))

type typer struct {
	Types map[string]*descriptor.DescriptorProto
}

func (t typer) mkType(fullName string, m *descriptor.DescriptorProto) string {
	buf := bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer func() {
		buf.Reset()
		bufPool.Put(buf)
	}()

	subTypes := make(map[string][]*descriptor.FieldDescriptorProto)
	for _, f := range m.GetField() {
		tn := mkTypeName(f.GetTypeName())
		if tn == "" {
			continue
		}
		subTypes[tn] = append(subTypes[tn],
			t.Types[f.GetTypeName()].GetField()...)
	}
	type Fields struct {
		Name   string
		Fields []*descriptor.FieldDescriptorProto
	}
	for k, vv := range subTypes {
		if err := typeTemplate.Execute(buf,
			Fields{Name: k, Fields: vv},
		); err != nil {
			panic(err)
		}
	}
	if fullName == "" {
		fullName = m.GetName()
	}
	typName := mkTypeName(fullName)
	if err := typeTemplate.Execute(buf,
		Fields{Name: typName, Fields: m.GetField()},
	); err != nil {
		panic(err)
	}
	return buf.String()
}

func mkXSDElement(f *descriptor.FieldDescriptorProto) string {
	maxOccurs := 1
	if f.GetLabel() == descriptor.FieldDescriptorProto_LABEL_REPEATED {
		maxOccurs = 999
	}
	name := CamelCase(f.GetName())
	typ := mkTypeName(f.GetTypeName())
	if typ == "" {
		typ = xsdType(f.GetType())
		if typ == "" {
			log.Printf("no type name for %s (%s)", f.GetTypeName(), f)
		}
	}
	return fmt.Sprintf(
		`<xsd:element minOccurs="0" nillable="true" maxOccurs="%d" name="%s" type="%s"/>`,
		maxOccurs, name, typ,
	)
}

func mkTypeName(s string) string {
	s = strings.TrimPrefix(s, ".")
	if s == "" {
		return s
	}
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return CamelCase(s[:i]) + "_" + (s[i+1:])
	}
	if 'A' <= s[0] && s[0] <= 'Z' {
		return (s)
	}
	return s
}

func xsdType(t descriptor.FieldDescriptorProto_Type) string {
	switch t {
	case descriptor.FieldDescriptorProto_TYPE_DOUBLE:
		return "xsd:double"
	case descriptor.FieldDescriptorProto_TYPE_FLOAT:
		return "xsd:float"
	case descriptor.FieldDescriptorProto_TYPE_INT64,
		descriptor.FieldDescriptorProto_TYPE_FIXED64,
		descriptor.FieldDescriptorProto_TYPE_SFIXED64,
		descriptor.FieldDescriptorProto_TYPE_SINT64:
		return "xsd:long"
	case descriptor.FieldDescriptorProto_TYPE_UINT64:
		return "xsd:unsignedLong"
	case descriptor.FieldDescriptorProto_TYPE_INT32,
		descriptor.FieldDescriptorProto_TYPE_FIXED32,
		descriptor.FieldDescriptorProto_TYPE_SFIXED32,
		descriptor.FieldDescriptorProto_TYPE_SINT32:
		return "xsd:int"
	case descriptor.FieldDescriptorProto_TYPE_BOOL:
		return "xsd:bool"
	case descriptor.FieldDescriptorProto_TYPE_STRING:
		return "xsd:string"
	case descriptor.FieldDescriptorProto_TYPE_GROUP:
		return "?grp?"
	case descriptor.FieldDescriptorProto_TYPE_MESSAGE:
		return "?msg?"
	case descriptor.FieldDescriptorProto_TYPE_BYTES:
		return "xsd:base64Binary"
	case descriptor.FieldDescriptorProto_TYPE_UINT32:
		return "xsd:unsignedInt"
	case descriptor.FieldDescriptorProto_TYPE_ENUM:
		return "?enum?"
	}
	return "???"
}

var digitUnder = strings.NewReplacer(
	"_0", "__0",
	"_1", "__1",
	"_2", "__2",
	"_3", "__3",
	"_4", "__4",
	"_5", "__5",
	"_6", "__6",
	"_7", "__7",
	"_8", "__8",
	"_9", "__9",
)

func CamelCase(text string) string {
	if text == "" {
		return text
	}
	var prefix string
	if text[0] == '*' {
		prefix, text = "*", text[1:]
	}

	text = digitUnder.Replace(text)
	var last rune
	return prefix + strings.Map(func(r rune) rune {
		defer func() { last = r }()
		if r == '_' {
			if last != '_' {
				return -1
			}
			return '_'
		}
		if last == 0 || last == '_' || '0' <= last && last <= '9' {
			return unicode.ToUpper(r)
		}
		return unicode.ToLower(r)
	},
		text,
	)
}

// UnCamelCase reverses CamelCase
func UnCamelCase(s string) string {
	v := make([]rune, 0, len(s))
	for i, r := range s {
		if i == 0 {
			v = append(v, unicode.ToLower(r))
			continue
		}
		if unicode.IsUpper(r) {
			v = append(v, '_', unicode.ToLower(r))
			continue
		}
		v = append(v, r)
	}
	return string(v)
}

func gzb64(s string) string {
	buf := bufPool.Get().(*bytes.Buffer)
	defer func() {
		buf.Reset()
		bufPool.Put(buf)
	}()
	buf.Reset()
	bw := base64.NewEncoder(base64.StdEncoding, buf)
	gw := gzip.NewWriter(bw)
	io.WriteString(gw, s)
	if err := gw.Close(); err != nil {
		panic(err)
	}
	if err := bw.Close(); err != nil {
		panic(err)
	}
	return buf.String()
}

// vim: set fileencoding=utf-8 noet:
