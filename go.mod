module github.com/UNO-SOFT/soap-proxy

go 1.21

toolchain go1.21.0

require (
	aqwari.net/xml v0.0.0-20210331023308-d9421b293817
	github.com/UNO-SOFT/grpcer v0.10.7
	github.com/UNO-SOFT/zlog v0.8.1
	github.com/klauspost/compress v1.17.4
	github.com/rogpeppe/retry v0.1.0
	github.com/tgulacsi/oracall v0.24.1
	golang.org/x/net v0.19.0
	google.golang.org/grpc v1.60.1
	google.golang.org/protobuf v1.32.0
)

require (
	github.com/UNO-SOFT/otel v0.6.0 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/go-logfmt/logfmt v0.6.0 // indirect
	github.com/go-logr/logr v1.4.1 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/godror/godror v0.40.2 // indirect
	github.com/godror/knownpb v0.1.1 // indirect
	github.com/golang/protobuf v1.5.3 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/tgulacsi/go-xmlrpc v0.2.2 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.46.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.46.1 // indirect
	go.opentelemetry.io/otel v1.21.0 // indirect
	go.opentelemetry.io/otel/exporters/stdout/stdoutmetric v0.44.0 // indirect
	go.opentelemetry.io/otel/exporters/stdout/stdouttrace v1.21.0 // indirect
	go.opentelemetry.io/otel/metric v1.21.0 // indirect
	go.opentelemetry.io/otel/sdk v1.21.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.21.0 // indirect
	go.opentelemetry.io/otel/trace v1.21.0 // indirect
	golang.org/x/exp v0.0.0-20230817173708-d852ddb80c63 // indirect
	golang.org/x/mod v0.12.0 // indirect
	golang.org/x/sys v0.15.0 // indirect
	golang.org/x/term v0.15.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	golang.org/x/tools v0.12.1-0.20230815132531-74c255bcf846 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20231212172506-995d672761c0 // indirect
)

//replace github.com/UNO-SOFT/grpcer => ../grpcer
