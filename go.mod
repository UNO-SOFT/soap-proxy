module github.com/UNO-SOFT/soap-proxy

go 1.15

require (
	aqwari.net/xml v0.0.0-20210331023308-d9421b293817
	github.com/UNO-SOFT/grpcer v0.8.5
	github.com/go-logr/logr v1.2.3
	github.com/klauspost/compress v1.16.3
	github.com/rogpeppe/retry v0.1.0
	github.com/tgulacsi/oracall v0.24.1
	golang.org/x/net v0.8.0
	google.golang.org/grpc v1.53.0
	google.golang.org/protobuf v1.29.0
)

//replace github.com/UNO-SOFT/grpcer => ../grpcer
