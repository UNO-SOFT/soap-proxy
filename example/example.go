package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/UNO-SOFT/grpcer"
	"github.com/UNO-SOFT/soap-proxy"
)

//go:generate protoc --grpcer_out=. -I $GOPATH/src $GOPATH/src/unosoft.hu/ws/bruno/pb/dealer/dealer.proto

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

	return http.ListenAndServe(
		addr,
		soapproxy.SOAPHandler{NewClient(cc)},
	)
}
