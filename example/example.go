package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/UNO-SOFT/grpcer"
	"github.com/UNO-SOFT/soap-proxy"
)

//go:generate go install -v github.com/UNO-SOFT/grpcer/protoc-gen-grpcer
//go:generate go install -v github.com/gogo/protobuf/protoc-gen-gofast

//go:generate protoc --wsdl_out=mysrvc:mysrvc/ -I . mysrvc/mysrvc.proto
//go:generate protoc --gofast_out=grpc:. -I $GOPATH/src -I . mysrvc/mysrvc.proto
//go:generate protoc --grpcer_out=main:. -I $GOPATH/src -I . $GOPATH/src/github.com/UNO-SOFT/soap-proxy/example/mysrvc/mysrvc.proto

func main() {
	flagEndpoint := flag.String("endpoint", "www.unosoft.hu:12443", "gRPC endpoint")
	flagCA := flag.String("ca", "", "CA file")
	flagHostOverride := flag.String("host-override", "", "override server's host")
	flag.Parse()

	if err := run(flag.Arg(0), *flagEndpoint, *flagCA, *flagHostOverride); err != nil {
		log.Fatal(err)
	}
}

func run(addr, endpoint, CA, hostOverride string) error {
	cc, err := grpcer.Connect(endpoint, CA, hostOverride)
	if err != nil {
		return err
	}

	if addr == "" {
		addr = ":8080"
	}
	log.Println("Listening on " + addr)
	return http.ListenAndServe(
		addr,
		soapproxy.SOAPHandler{
			Client: NewClient(cc),
			WSDL:   soapproxy.Ungzb64(WSDLgzb64),
		},
	)
}
