# protoc-gen-wsdl
Generate a WSDL from the .proto file, and embedded in a .go file - package name can be specified before the output path:

	protoc --wsdl_out=main:myproxy -I $GOPATH/src $GOPATH/src/unosoft.hu/ws/bruno/pb/dealer/dealer.proto

will create `myproxy/dealer.wsdl` and `myproxy/dealer.wsdl.go`.

