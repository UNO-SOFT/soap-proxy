# soapproxy
A simple SOAP <-> gRPC proxy.

## 1. Generate the WSDL
with [./protoc-gen-wsdl](protoc-gen-wsdl):

	protoc --wsdl_out=myproxy -I $GOPATH/src $GOPATH/src/unosoft.hu/ws/bruno/pb/dealer/dealer.proto

will create `myproxy/dealer.wsdl`.

## 2. Generate the Client code
with [github.com/UNO-SOFT/gprcer/protoc-gen-grpcer](protoc-gen-grpcer):

	protoc --grpcer_out=myproxy -I $GOPATH/src $GOPATH/src/unosoft.hu/ws/bruno/pb/dealer/dealer.proto

will create `myproxy/dealer.grpcer.go`, with `grpc.Client` implementation in it.

## 3. Profit!
Then use the `SOAPHandler` in `myproxy/main.go` (see [./example/example.go](example.go)):

	cc, err := grpcer.Connect("grpc-host:port", "ca.pem", "localhost")
	if err != nil {
		log.Fatal(err)
	}
	http.ListenAndServe(
		":8080",
		soapproxy.SOAPHandler{NewClient(cc)},
	)

