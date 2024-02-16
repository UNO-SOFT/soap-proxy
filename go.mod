module github.com/UNO-SOFT/soap-proxy

go 1.21

toolchain go1.22.0

require (
	aqwari.net/xml v0.0.0-20210331023308-d9421b293817
	github.com/UNO-SOFT/grpcer v0.10.10
	github.com/UNO-SOFT/zlog v0.8.1
	github.com/klauspost/compress v1.17.6
	github.com/rogpeppe/retry v0.1.0
	github.com/tgulacsi/go v0.27.3
	github.com/tgulacsi/oracall v0.24.1
	golang.org/x/net v0.21.0
	google.golang.org/grpc v1.61.1
	google.golang.org/protobuf v1.32.0
)

require (
	github.com/UNO-SOFT/otel v0.6.2 // indirect
	github.com/dgryski/go-linebreak v0.0.0-20180812204043-d8f37254e7d3 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/go-logfmt/logfmt v0.6.0 // indirect
	github.com/go-logr/logr v1.4.1 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/godror/godror v0.40.2 // indirect
	github.com/godror/knownpb v0.1.1 // indirect
	github.com/golang/protobuf v1.5.3 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/tgulacsi/go-xmlrpc v0.2.2 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.48.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.48.0 // indirect
	go.opentelemetry.io/otel v1.23.1 // indirect
	go.opentelemetry.io/otel/exporters/stdout/stdoutmetric v1.23.1 // indirect
	go.opentelemetry.io/otel/exporters/stdout/stdouttrace v1.23.1 // indirect
	go.opentelemetry.io/otel/metric v1.23.1 // indirect
	go.opentelemetry.io/otel/sdk v1.23.1 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.23.1 // indirect
	go.opentelemetry.io/otel/trace v1.23.1 // indirect
	golang.org/x/exp v0.0.0-20240213143201-ec583247a57a // indirect
	golang.org/x/mod v0.15.0 // indirect
	golang.org/x/sync v0.6.0 // indirect
	golang.org/x/sys v0.17.0 // indirect
	golang.org/x/term v0.17.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	golang.org/x/tools v0.18.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240213162025-012b6fc9bca9 // indirect
)

//replace github.com/UNO-SOFT/grpcer => ../grpcer
