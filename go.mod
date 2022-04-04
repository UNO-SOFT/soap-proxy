module github.com/UNO-SOFT/soap-proxy

go 1.15

require (
	aqwari.net/xml v0.0.0-20181013063537-841f47b2a098
	github.com/UNO-SOFT/grpcer v0.8.1
	github.com/UNO-SOFT/ulog v1.3.1 // indirect
	github.com/go-kit/kit v0.12.0 // indirect
	github.com/go-logr/logr v1.2.3
	github.com/klauspost/compress v1.15.1 // indirect
	github.com/mitchellh/mapstructure v1.4.3 // indirect
	github.com/rogpeppe/retry v0.1.0
	github.com/tgulacsi/go v0.19.3 // indirect
	github.com/tgulacsi/oracall v0.23.0
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.31.0 // indirect
	golang.org/x/net v0.0.0-20220403103023-749bd193bc2b
	golang.org/x/sys v0.0.0-20220403205710-6acee93ad0eb // indirect
	golang.org/x/time v0.0.0-20220224211638-0e9765cccd65 // indirect
	google.golang.org/genproto v0.0.0-20220401170504-314d38edb7de // indirect
	google.golang.org/grpc v1.45.0
	google.golang.org/protobuf v1.28.0
)

//replace github.com/UNO-SOFT/grpcer => ../grpcer
