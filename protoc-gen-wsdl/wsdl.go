// Copyright 2017, 2021 Tamás Gulácsi
//
// SPDX-License-Identifier: Apache-2.0

package main

// nosemgrep: go.lang.security.audit.xss.import-text-template.import-text-template
import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"
	"unicode"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/types/descriptorpb"
)

var opts protogen.Options

func main() {
	opts.Run(Generate)
}

var IsHidden = func(s string) bool { return strings.HasSuffix(s, "_hidden") }
var wrapArray = os.Getenv("WRAP_ARRAY") == "1"

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
    <xs:schema elementFormDefault="qualified" targetNamespace="{{.TypesNS}}">
      <xs:element name="exception">
        <xs:complexType>
          <xs:sequence>
            <xs:element name="type" nillable="true" type="xs:string"/>
            <xs:element name="message" nillable="true" type="xs:string"/>
            <xs:element name="traceback" nillable="true" type="xs:string"/>
          </xs:sequence>
        </xs:complexType>
      </xs:element>
      {{range .RestrictedTypes}}
	  {{if ne .Name ""}}
	  <xs:simpleType name="{{.Name}}"><xs:restriction base="xs:
	  {{- if eq (slice .Name 0 3) "str"}}string"><xs:maxLength value="{{if eq .Prec 0}}32767{{else}}{{.Prec}}{{end}}"/>
	  {{- else}}{{if (and (ne .Prec 0) (eq .Scale 0))}}integer{{else}}decimal{{end}}"><xs:totalDigits value="{{if eq .Prec 0}}38{{else}}{{.Prec}}{{end}}"/>{{if ne .Scale 0}}<xs:fractionDigits value="{{.Scale}}"/>{{end}}
	  {{end}}</xs:restriction></xs:simpleType>
	  {{end}}{{end}}

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
    <part element="types:{{$input}}" name="input" />
  </message>
  <message name="{{$output}}">
    <part element="types:{{$output}}" name="output" />
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
	  {{if (ne "" (index $docu .GetName))}}<documentation><![CDATA[{{index $docu .GetName | xmlEscape}}]]></documentation>{{end}}
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

func Generate(p *protogen.Plugin) error {
	req := p.Request
	destPkg := req.GetParameter()
	if destPkg == "" {
		destPkg = "main"
	}
	// Find roots.
	rootNames := req.GetFileToGenerate()
	files := req.GetProtoFile()
	roots := make(map[string]*descriptorpb.FileDescriptorProto, len(rootNames))
	allTypes := make(map[string]*descriptorpb.DescriptorProto, 1024)
	var found int
	fieldDocs := make(map[string]string)
	restrictedTypes := make(map[string]XSDType)
	for i := len(files) - 1; i >= 0; i-- {
		f := files[i]
		msgs, pkg := f.GetMessageType(), f.GetPackage()
		for _, m := range msgs {
			allTypes["."+pkg+"."+m.GetName()] = m
		}
		if si := f.GetSourceCodeInfo(); si != nil {
			for _, loc := range si.GetLocation() {
				if path := loc.GetPath(); len(path) >= 4 && path[0] == 4 && path[2] == 2 {
					if s := strings.TrimSpace(loc.GetLeadingComments()); s != "" {
						m := msgs[int(path[1])]
						fieldDocs["."+pkg+"."+m.GetName()+"."+m.GetField()[int(path[3])].GetName()] = s
						if _, ok := restrictedTypes[s]; !ok {
							if xt := xsdTypeFromDocu(s); xt.Name != "" {
								if _, ok := restrictedTypes[xt.Name]; !ok {
									restrictedTypes[xt.Name] = xt
								}
							}
						}
					}
				}
			}
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

	msgTypes := make(map[string]*descriptorpb.DescriptorProto, len(allTypes))
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
			"xmlEscape":  xmlEscape,
		}).
		Parse(wsdlTmpl))

	// Service, bind and message types from roots.
	// All other types are from imported dependencies, recursively.
	type whole struct {
		GeneratedAt time.Time
		*descriptorpb.ServiceDescriptorProto
		Documentation     map[string]string
		Types             map[string]*descriptorpb.DescriptorProto
		RestrictedTypes   map[string]XSDType
		Package           string
		TargetNS, TypesNS string
		Version, Owner    string
		Locations         []string
	}

	now := time.Now()
	for _, root := range roots {
		root := root
		pkg := root.GetName()
		for svcNo, svc := range root.GetService() {
			methods := svc.GetMethod()
			data := whole{
				Package:  svc.GetName(),
				TargetNS: "http://" + pkg + "/" + svc.GetName() + "/",
				TypesNS:  "http://" + pkg + "/" + svc.GetName() + "_types/",
				Types:    msgTypes,

				ServiceDescriptorProto: svc,
				GeneratedAt:            now,
				Documentation:          make(map[string]string),
				RestrictedTypes:        restrictedTypes,
			}
			for k, v := range fieldDocs {
				data.Documentation[k] = v
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
			buf2 := bufPool.Get().(*bytes.Buffer)
			buf2.Reset()
			defer func() {
				buf.Reset()
				bufPool.Put(buf)
				buf2.Reset()
				bufPool.Put(buf2)
			}()
			err := wsdlTemplate.Execute(buf, data)
			if err != nil {
				p.Error(err)
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			cmd := exec.CommandContext(ctx, "xmllint", "--format", "-")
			content := buf.Bytes()
			var gzContent []byte
			cmd.Stdin = bytes.NewReader(content)
			cmd.Stdout, cmd.Stderr = buf2, os.Stderr
			fmtErr := cmd.Run()
			cancel()
			if fmtErr != nil {
				log.Println(cmd.Args, fmtErr)
				buf2.Reset()
				if gzErr := gzb64(buf2, content); gzErr != nil {
					return gzErr
				}
				gzContent = buf2.Bytes()
			} else {
				content = buf2.Bytes()
				buf.Reset()
				if gzErr := gzb64(buf, content); gzErr != nil {
					return gzErr
				}
				gzContent = buf.Bytes()
			}
			if _, err = p.NewGeneratedFile(destFn, protogen.GoImportPath(pkg)).Write(
				content,
			); err != nil {
				return err
			}

			// also, embed the wsdl
			if _, err = fmt.Fprintf(
				p.NewGeneratedFile(destFn+".go", protogen.GoImportPath(pkg)),
				`package %s

// WSDLgzb64 contains the WSDL, gzipped and base64-encoded.
// You can easily read it with soapproxy.Ungzb64.
const WSDLgzb64 = `+"`%s`\n",
				destPkg, gzContent,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

var bufPool = sync.Pool{New: func() interface{} { return bytes.NewBuffer(make([]byte, 0, 4096)) }}

var elementTypeTemplate = template.Must(
	template.New("elementType").
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
	template.New("xsdType").
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
	Types       map[string]*descriptorpb.DescriptorProto
	inputRawXml map[string]struct{}

	seen map[string]struct{}
}

func (t *typer) mkType(fullName string, m *descriptorpb.DescriptorProto, documentation map[string]string) string {
	buf := bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer func() {
		buf.Reset()
		bufPool.Put(buf)
	}()

	// <BrunoLevelek_Output><PLevelek><SzerzAzon>1</SzerzAzon><Tipus></Tipus><Url></Url><Datum></Datum></PLevelek><PLevelek><SzerzAzon>2</SzerzAzon><Tipus>f</Tipus><Url></Url><Datum></Datum></PLevelek><PHibaKod>0</PHibaKod><PHibaSzov></PHibaSzov></BrunoLevelek_Output>Hello, playground
	subTypes := make(map[string][]*descriptorpb.FieldDescriptorProto)
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
					//log.Printf("st=%+v namePrefix=%q", st, namePrefix)
					if st.Name.Local != "" {
						if !(st.Name.Local == "schema" && (st.Name.Space == "" || st.Name.Space == "http://www.w3.org/2001/XMLSchema")) {
							log.Printf("Documentation of %q is XML, but does not start with \"schema\" (but %q)", name, st.Name)
						} else if strings.Contains(docu, "element name=\""+namePrefix+"_Input\"") &&
							strings.Contains(docu, "element name=\""+name+"\" ") {
							delete(documentation, namePrefix)

							return docu[int(xr.InputOffset()):strings.LastIndex(docu, "</")]
							//} else {
							//	log.Printf("no %q element in %q", namePrefix+"_Input", docu)
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

	var addFieldSubtypes func(mFields []*descriptorpb.FieldDescriptorProto)
	addFieldSubtypes = func(mFields []*descriptorpb.FieldDescriptorProto) {
		for _, f := range mFields {
			tn := mkTypeName(f.GetTypeName())
			if tn == "" || len(subTypes[tn]) != 0 {
				continue
			}
			if wrapArray && f.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REPEATED {
				nm := f.GetName() + "_Rec"
				arr := descriptorpb.FieldDescriptorProto{
					Name:           &nm,
					Number:         f.Number,
					Label:          f.Label,
					Type:           f.Type,
					TypeName:       f.TypeName,
					Extendee:       f.Extendee,
					OneofIndex:     f.OneofIndex,
					JsonName:       f.JsonName,
					Options:        f.Options,
					Proto3Optional: f.Proto3Optional,
				}

				subTypes[tn+"_Arr"] = []*descriptorpb.FieldDescriptorProto{&arr}
			}
			ft := t.Types[f.GetTypeName()].GetField()
			subTypes[tn] = ft
			addFieldSubtypes(ft)
		}
	}
	addFieldSubtypes(mFields)

	type Fields struct {
		Name   string
		Fields []Field
	}
	newFields := func(name string, fields []*descriptorpb.FieldDescriptorProto) Fields {
		ff := filterHiddenFields(fields)
		fs := Fields{Name: name, Fields: make([]Field, len(ff))}
		for i, f := range ff {
			fld := Field{FieldDescriptorProto: f, Documentation: documentation[fullName+"."+f.GetName()]}
			if xt := xsdTypeFromDocu(fld.Documentation); xt.Name != "" {
				fld.XSDTypeName = xt.Name
			}
			fs.Fields[i] = fld
		}
		return fs
	}
	if fullName == "" {
		fullName = m.GetName()
	}
	typName := mkTypeName(fullName)
	//log.Println("full:", fullName, "typ:", typName, "len:", len(m.GetField()), "filtered:", len(filterHiddenFields(m.GetField())))

	if err := elementTypeTemplate.Execute(buf, newFields(typName, m.GetField())); err != nil {
		panic(err)
	}
	if len(subTypes) == 0 {
		return buf.String()
	}
	if t.seen == nil {
		t.seen = make(map[string]struct{})
	}
	for k, vv := range subTypes {
		if _, seen := t.seen[k]; seen {
			continue
		}
		t.seen[k] = struct{}{}
		if err := xsdTypeTemplate.Execute(buf, newFields(k, vv)); err != nil {
			panic(err)
		}
	}
	return buf.String()
}

type Field struct {
	*descriptorpb.FieldDescriptorProto
	XSDTypeName, Documentation string
}

func filterHiddenFields(fields []*descriptorpb.FieldDescriptorProto) []*descriptorpb.FieldDescriptorProto {
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

func mkXSDElement(f Field) string {
	name := CamelCase(f.GetName())
	typ := f.XSDTypeName
	var cplx bool
	if typ == "" {
		typ = mkTypeName(f.GetTypeName())
		cplx = typ != ""
	}
	if typ != "" {
		typ = "types:" + typ
	} else {
		typ = xsdType(f.GetType(), f.GetTypeName())
		if typ == "" {
			log.Printf("no type name for %s (%s)", f.GetTypeName(), f)
		}
	}
	maxOccurs := "1"
	if f.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REPEATED {
		if wrapArray && !strings.HasSuffix(f.GetName(), "_Rec") && cplx { // complex type
			//log.Println(f.GetName(), f)
			return fmt.Sprintf(`<xs:element minOccurs="0" nillable="true" maxOccurs="1" name="%s" type="%s_Arr"/>`,
				name, typ)
		}
		maxOccurs = "unbounded"
	}
	docu := f.Documentation
	if docu != "" {
		docu = "<!-- " + docu + " -->\n"
	}
	return fmt.Sprintf(
		`%s<xs:element minOccurs="0" nillable="true" maxOccurs="%s" name="%s" type="%s"/>`,
		docu, maxOccurs, name, typ,
	)
}

var rOraType = regexp.MustCompile(`(?:^|\s*)(DATE|(?:INTEGER|NUMBER)(?:[(][0-9]+[)])?|VARCHAR2[(][0-9]+[)]|NUMBER[(][0-9]+,[0-9]+[)])$`)

type XSDType struct {
	Name          string
	Documentation string
	Prec, Scale   int
}

func (xt XSDType) Element() string {
	if xt.Name == "" {
		return ""
	}
	switch xt.Name[:3] {
	case "str":
		if xt.Prec > 0 {
			return fmt.Sprintf(`<xs:element name="`+xt.Name+`"><xs:simpleType><xs:restriction base="xs:string"><xs:maxLength value="%d"/></xs:restriction></xs:simpleType></xs:element>`, xt.Prec)
		}
	case "dec":
		if xt.Prec == 0 {
			return `<xs:element name="` + xt.Name + `"><xs:simpleType><xs:restriction base="xs:decimal"><xs:totalDigits value="38"/></xs:restriction></xs:simpleType></xs:element>`
		}
		if xt.Scale == 0 {
			return fmt.Sprintf(`<xs:element name="%s"><xs:simpleType><xs:restriction base="xs:integer"><xs:totalDigits value="%d"/></xs:restriction></xs:simpleType></xs:element>`,
				xt.Name, xt.Prec,
			)
		}
		return fmt.Sprintf(
			`<xs:element name="%s"><xs:simpleType><xs:restriction base="xs:decimal"><xs:totalDigits value="%d"/><xs:fractionDigits value="%d"/></xs:restriction></xs:simpleType></xs:element>`,
			xt.Name, xt.Prec, xt.Scale,
		)
	}
	return ""
}

func xsdTypeFromDocu(docu string) XSDType {
	if docu == "" {
		return XSDType{}
	}
	xt := XSDType{Documentation: docu}
	var typ string
	ms := rOraType.FindStringSubmatch(docu)
	if len(ms) > 0 {
		typ = ms[1]
	}
	if typ == "" || len(typ) < 3 {
		return xt
	}

	b, e := strings.LastIndexByte(typ, '(')+1, len(typ)-1
	switch typ[:3] {
	case "CHA", "VAR":
		prec, err := strconv.Atoi(typ[b:e])
		if err != nil {
			panic(fmt.Errorf("%q: %w", typ[b:e], err))
		}
		xt.Prec = prec
		xt.Name = "string_" + typ[b:e]
		return xt

	case "NUM", "INT":
		if typ[e] == 'R' {
			return xt
		}
		i := strings.IndexByte(typ[b:e], ',')
		if i < 0 {
			i = e
		} else {
			i += b
		}
		//log.Printf("typ=%q b=%d i=%d e=%d", typ, b, i ,e)
		prec, err := strconv.Atoi(typ[b:i])
		if err != nil {
			panic(fmt.Errorf("%q: %w", typ[b:i], err))
		}
		xt.Prec = prec
		if i == e {
			xt.Name = "decimal_" + typ[b:i]
			return xt
		}

		scale, err := strconv.Atoi(typ[i+1 : e])
		if err != nil {
			panic(fmt.Errorf("%q: %w", typ[i+1:e], err))
		}
		xt.Scale = scale
		xt.Name = fmt.Sprintf("decimal_%d_%d", xt.Prec, xt.Scale)
		return xt
	}
	return xt
}

func mkTypeName(s string) string {
	if s == ".google.protobuf.Timestamp" {
		return ""
	}
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

func xsdType(t descriptorpb.FieldDescriptorProto_Type, typeName string) string {
	switch t {
	case descriptorpb.FieldDescriptorProto_TYPE_DOUBLE:
		return "xs:double"
	case descriptorpb.FieldDescriptorProto_TYPE_FLOAT:
		return "xs:float"
	case descriptorpb.FieldDescriptorProto_TYPE_INT64,
		descriptorpb.FieldDescriptorProto_TYPE_FIXED64,
		descriptorpb.FieldDescriptorProto_TYPE_SFIXED64,
		descriptorpb.FieldDescriptorProto_TYPE_SINT64:
		return "xs:long"
	case descriptorpb.FieldDescriptorProto_TYPE_UINT64:
		return "xs:unsignedLong"
	case descriptorpb.FieldDescriptorProto_TYPE_INT32,
		descriptorpb.FieldDescriptorProto_TYPE_FIXED32,
		descriptorpb.FieldDescriptorProto_TYPE_SFIXED32,
		descriptorpb.FieldDescriptorProto_TYPE_SINT32:
		return "xs:int"
	case descriptorpb.FieldDescriptorProto_TYPE_BOOL:
		return "xs:boolean"
	case descriptorpb.FieldDescriptorProto_TYPE_STRING:
		return "xs:string"
	case descriptorpb.FieldDescriptorProto_TYPE_GROUP:
		return "?grp?"
	case descriptorpb.FieldDescriptorProto_TYPE_MESSAGE:
		if typeName == ".google.protobuf.Timestamp" {
			return "xs:dateTime"
		}
		return "?msg?"
	case descriptorpb.FieldDescriptorProto_TYPE_BYTES:
		return "xs:base64Binary"
	case descriptorpb.FieldDescriptorProto_TYPE_UINT32:
		return "xs:unsignedInt"
	case descriptorpb.FieldDescriptorProto_TYPE_ENUM:
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

func gzb64(w io.Writer, b []byte) error {
	bw := base64.NewEncoder(base64.StdEncoding, w)
	gw := gzip.NewWriter(bw)
	if _, err := gw.Write(b); err != nil {
		return err
	}
	if err := gw.Close(); err != nil {
		return err
	}
	if err := bw.Close(); err != nil {
		return err
	}
	return nil
}

func xmlEscape(s string) string {
	var buf bytes.Buffer
	if err := xml.EscapeText(&buf, []byte(s)); err != nil {
		panic(err)
	}
	return buf.String()
}

// vim: set fileencoding=utf-8 noet:
