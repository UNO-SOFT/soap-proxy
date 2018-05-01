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
	"time"
	"unicode"

	"github.com/golang/protobuf/protoc-gen-go/descriptor"
	protoc "github.com/golang/protobuf/protoc-gen-go/plugin"
	"golang.org/x/sync/errgroup"
)

var IsHidden = func(s string) bool { return strings.HasSuffix(s, "_hidden") }

const wsdlTmpl = xml.Header + `<definitions
    name="{{.Package}}"
    targetNamespace="{{.TargetNS}}"
    xmlns="http://schemas.xmlsoap.org/wsdl/"
    xmlns:tns="{{.TargetNS}}"
    xmlns:types="{{.TypesNS}}"
    xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
    xmlns:soapenc="http://schemas.xmlsoap.org/soap/encoding/"
    xmlns:wsdl="http://schemas.xmlsoap.org/wsdl/"
    xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <documentation>
    Service: {{.Package}}
    Version: {{.Version}}
    Generated: {{.GeneratedAt}}
    Owner: {{.Owner}}
  </documentation>
  <types>
    <xs:schema elementFormDefault="unqualified" targetNamespace="{{.TypesNS}}">
      <xs:element name="exception">
        <xs:complexType>
          <xs:sequence>
            <xs:element name="type" nillable="true" type="xs:string"/>
            <xs:element name="message" nillable="true" type="xs:string"/>
            <xs:element name="traceback" nillable="true" type="xs:string"/>
          </xs:sequence>
        </xs:complexType>
      </xs:element>
	  {{$docu := .Documentation}}
      {{range $k, $m := .Types}}
        {{mkType $k $m $docu}}
      {{end}}
    </xs:schema>
  </types>
  <message name="error">
    <part element="types:exception" name="error"/>
  </message>
  {{range .GetMethod}}
  {{$input := mkTypeName .GetInputType}}
  {{$output := mkTypeName .GetOutputType}}
  <message name="{{$input}}">
    <part element="types:{{$input}}" name="{{$input}}" />
  </message>
  <message name="{{$output}}">
    <part element="types:{{$output}}" name="{{$output}}" />
  </message>
  {{end}}
  <portType name="{{.Package}}">
    {{range .GetMethod}}
    <operation name="{{.GetName}}">
      <input message="tns:{{mkTypeName .GetInputType}}"/>
      <output message="tns:{{mkTypeName .GetOutputType}}"/>
      <fault message="tns:error" name="error"/>
    </operation>{{end}}
  </portType>
  <binding name="{{.Package}}_soap" type="tns:{{.Package}}">
    <soap:binding transport="http://schemas.xmlsoap.org/soap/http"/>
	{{$docu := .Documentation}}
    {{range .GetMethod}}
    <operation name="{{.Name}}">
      <wsdl:documentation><![CDATA[{{index $docu .GetName | xmlEscape}}]]></wsdl:documentation>
      <soap:operation soapAction="{{$.TargetNS}}{{.GetName}}" style="document" />
      <input><soap:body use="literal"/></input>
      <output><soap:body use="literal"/></output>
      <fault name="error"><soap:fault name="error" use="literal"/></fault>
    </operation>{{end}}
  </binding>
  <service name="{{.Package}}__service">
    <port binding="tns:{{.Package}}_soap" name="{{.Package}}">
      {{range .Locations}}
      <soap:address location="{{.}}"/>
      {{end}}
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
		for _, svc := range root.GetService() {
			for _, m := range svc.GetMethod() {
				kk := m.GetInputType()
				msgTypes[kk] = allTypes[kk]
				kk = m.GetOutputType()
				msgTypes[kk] = allTypes[kk]
			}
		}
	}

	// delete not needed message types
	wsdlTemplate := template.Must(template.
		New("wsdl").
		Funcs(template.FuncMap{
			"mkTypeName": mkTypeName,
			"mkType":     (&typer{Types: allTypes}).mkType,
			"xmlEscape": func(s string) string {
				var buf bytes.Buffer
				if err := xml.EscapeText(&buf, []byte(s)); err != nil {
					panic(err)
				}
				return buf.String()
			},
		}).
		Parse(wsdlTmpl))

	// Service, bind and message types from roots.
	// All other types are from imported dependencies, recursively.
	type whole struct {
		*descriptor.ServiceDescriptorProto
		Package           string
		TargetNS, TypesNS string
		Types             map[string]*descriptor.DescriptorProto
		GeneratedAt       time.Time
		Version, Owner    string
		Locations         []string
		Documentation     map[string]string
	}

	now := time.Now()
	var grp errgroup.Group
	var mu sync.Mutex
	resp.File = make([]*protoc.CodeGeneratorResponse_File, 0, len(roots))
	for _, root := range roots {
		root := root
		pkg := root.GetName()
		for svcNo, svc := range root.GetService() {
			grp.Go(func() error {
				methods := svc.GetMethod()
				data := whole{
					Package:  svc.GetName(),
					TargetNS: "http://" + pkg + "/" + svc.GetName() + "/",
					TypesNS:  "http://" + pkg + "/" + svc.GetName() + "_types/",
					Types:    msgTypes,

					ServiceDescriptorProto: svc,
					GeneratedAt:            now,
					Documentation:          make(map[string]string),
				}
				if si := root.GetSourceCodeInfo(); si != nil {
					for _, loc := range si.GetLocation() {
						if path := loc.GetPath(); len(path) == 4 && path[0] == 6 && path[1] == int32(svcNo) && path[2] == 2 {
							s := strings.TrimPrefix(strings.Replace(loc.GetLeadingComments(), "\n/", "\n", -1), "/")

							const nsEq = "REPLACE namespace=\""
							if i := strings.Index(s, nsEq); i >= 0 {
								i += len(nsEq)
								if j := strings.IndexByte(s[i:], '"'); j >= 0 {
									tbr := s[i : i+j]
									// remove <!-- REPLACE namespace="..." -->
									i = strings.LastIndex(s[:i], "<!--")
									j += strings.Index(s[j:], "-->")
									s = s[:i] + s[j+3:]
									// replace namespace to TypesNS
									s = strings.Replace(s, tbr, data.TypesNS, -1)
								}
							}
							data.Documentation[methods[int(path[3])].GetName()] = s
						}
					}
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

var elementTypeTemplate = template.Must(
	template.New("type").
		Funcs(template.FuncMap{
			"mkXSDElement": mkXSDElement,
		}).
		Parse(`<xs:element name="{{.Name}}">
  <xs:complexType>
    <xs:sequence>
	{{range .Fields}}{{mkXSDElement .}}
	{{end}}
    </xs:sequence>
  </xs:complexType>
</xs:element>
`))

var xsdTypeTemplate = template.Must(
	template.New("type").
		Funcs(template.FuncMap{
			"mkXSDElement": mkXSDElement,
		}).
		Parse(`<xs:complexType name="{{.Name}}">
  <xs:sequence>
  {{range .Fields}}{{mkXSDElement .}}
  {{end}}
  </xs:sequence>
</xs:complexType>
`))

type typer struct {
	Types       map[string]*descriptor.DescriptorProto
	inputRawXml map[string]struct{}

	seen map[string]struct{}
}

func (t *typer) mkType(fullName string, m *descriptor.DescriptorProto, documentation map[string]string) string {
	buf := bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer func() {
		buf.Reset()
		bufPool.Put(buf)
	}()

	// <BrunoLevelek_Output><PLevelek><SzerzAzon>1</SzerzAzon><Tipus></Tipus><Url></Url><Datum></Datum></PLevelek><PLevelek><SzerzAzon>2</SzerzAzon><Tipus>f</Tipus><Url></Url><Datum></Datum></PLevelek><PHibaKod>0</PHibaKod><PHibaSzov></PHibaSzov></BrunoLevelek_Output>Hello, playground
	subTypes := make(map[string][]*descriptor.FieldDescriptorProto)
	mFields := m.GetField()
	if len(mFields) == 1 && mFields[0].GetType().String() == "TYPE_STRING" {
		name := mkTypeName(fullName)
		var any bool
		var namePrefix string
		if t.inputRawXml == nil {
			t.inputRawXml = make(map[string]struct{})
		}
		var isOutput bool
		if any = strings.HasSuffix(name, "_Input") && mFields[0].GetName() == "p_raw_xml"; any {
			namePrefix = strings.TrimSuffix(name, "_Input")
			t.inputRawXml[namePrefix] = struct{}{}
		} else if mFields[0].GetName() == "ret" {
			namePrefix = strings.TrimSuffix(name, "_Output")
			_, any = t.inputRawXml[namePrefix]
			isOutput = true
		}
		if any {
			if documentation != nil {
				if !isOutput {
					return ""
				}
				if docu := documentation[namePrefix]; docu != "" {
					docu = strings.TrimSpace(docu)
					xr := xml.NewDecoder(strings.NewReader(docu))
					var st xml.StartElement
					for {
						tok, err := xr.Token()
						if err != nil {
							log.Printf("Parse %q of %q as XML: %v", docu, name, err)
							break
						}
						var ok bool
						if st, ok = tok.(xml.StartElement); ok {
							break
						}
					}
					if st.Name.Local != "" {
						if !(st.Name.Local == "schema" && (st.Name.Space == "" || st.Name.Space == "http://www.w3.org/2001/XMLSchema")) {
							log.Printf("Documentation of %q is XML, but does not start with \"schema\" (but %q)", name, st.Name)
						} else if strings.Contains(docu, "element name=\""+namePrefix+"_Input\" ") &&
							strings.Contains(docu, "element name=\""+name+"\" ") {
							delete(documentation, namePrefix)
							return docu[int(xr.InputOffset()):strings.LastIndex(docu, "</")]
						}
					}
				}
			}
			fmt.Fprintf(buf, `<xs:element name="%s">
	<xs:complexType><xs:sequence><xs:any /></xs:sequence></xs:complexType>
	</xs:element>`, name)
			return buf.String()
		}
	}
	for _, f := range mFields {
		tn := mkTypeName(f.GetTypeName())
		if tn == "" {
			continue
		}
		if len(subTypes[tn]) == 0 {
			subTypes[tn] = t.Types[f.GetTypeName()].GetField()
		}
	}
	type Fields struct {
		Name   string
		Fields []*descriptor.FieldDescriptorProto
	}
	if fullName == "" {
		fullName = m.GetName()
	}
	typName := mkTypeName(fullName)
	//log.Println("full:", fullName, "typ:", typName, "len:", len(m.GetField()), "filtered:", len(filterHiddenFields(m.GetField())))
	fields := Fields{Name: typName, Fields: filterHiddenFields(m.GetField())}
	if err := elementTypeTemplate.Execute(buf, fields); err != nil {
		panic(err)
	}
	if t.seen == nil {
		t.seen = make(map[string]struct{})
	}
	for k, vv := range subTypes {
		if _, seen := t.seen[k]; seen {
			continue
		}
		t.seen[k] = struct{}{}
		if err := xsdTypeTemplate.Execute(buf,
			Fields{Name: k, Fields: filterHiddenFields(vv)},
		); err != nil {
			panic(err)
		}
	}
	return buf.String()
}

func filterHiddenFields(fields []*descriptor.FieldDescriptorProto) []*descriptor.FieldDescriptorProto {
	if IsHidden == nil {
		return fields
	}
	oLen, deleted := len(fields), 0
	for i := 0; i < len(fields); i++ {
		if IsHidden(fields[i].GetName()) {
			fields = append(fields[:i], fields[i+1:]...)
			i--
			deleted++
		}
	}
	if oLen-deleted != len(fields) {
		panic(fmt.Sprintf("Deleted %d fields from %d, got %d!", deleted, oLen, len(fields)))
	}
	return fields
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
	} else {
		typ = "types:" + typ
	}
	return fmt.Sprintf(
		`<xs:element minOccurs="0" nillable="true" maxOccurs="%d" name="%s" type="%s"/>`,
		maxOccurs, name, typ,
	)
}

func mkTypeName(s string) string {
	s = strings.TrimPrefix(s, ".")
	if s == "" {
		return s
	}
	i := strings.IndexByte(s, '.')
	if i < 0 {
		return s
	}
	s = CamelCase(s[:i]) + "_" + (s[i+1:])
	i = strings.IndexByte(s, '_')
	if strings.HasPrefix(s[i+1:], s[:i+1]) {
		s = s[i+1:]
	}
	return s
}

func xsdType(t descriptor.FieldDescriptorProto_Type) string {
	switch t {
	case descriptor.FieldDescriptorProto_TYPE_DOUBLE:
		return "xs:double"
	case descriptor.FieldDescriptorProto_TYPE_FLOAT:
		return "xs:float"
	case descriptor.FieldDescriptorProto_TYPE_INT64,
		descriptor.FieldDescriptorProto_TYPE_FIXED64,
		descriptor.FieldDescriptorProto_TYPE_SFIXED64,
		descriptor.FieldDescriptorProto_TYPE_SINT64:
		return "xs:long"
	case descriptor.FieldDescriptorProto_TYPE_UINT64:
		return "xs:unsignedLong"
	case descriptor.FieldDescriptorProto_TYPE_INT32,
		descriptor.FieldDescriptorProto_TYPE_FIXED32,
		descriptor.FieldDescriptorProto_TYPE_SFIXED32,
		descriptor.FieldDescriptorProto_TYPE_SINT32:
		return "xs:int"
	case descriptor.FieldDescriptorProto_TYPE_BOOL:
		return "xs:bool"
	case descriptor.FieldDescriptorProto_TYPE_STRING:
		return "xs:string"
	case descriptor.FieldDescriptorProto_TYPE_GROUP:
		return "?grp?"
	case descriptor.FieldDescriptorProto_TYPE_MESSAGE:
		return "?msg?"
	case descriptor.FieldDescriptorProto_TYPE_BYTES:
		return "xs:base64Binary"
	case descriptor.FieldDescriptorProto_TYPE_UINT32:
		return "xs:unsignedInt"
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
