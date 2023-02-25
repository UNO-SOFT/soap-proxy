module github.com/UNO-SOFT/soap-proxy

go 1.15

require (
	aqwari.net/xml v0.0.0-20210331023308-d9421b293817
	github.com/UNO-SOFT/grpcer v0.8.3
	github.com/UNO-SOFT/otel v0.3.3 // indirect
	github.com/go-logr/logr v1.2.3
	github.com/klauspost/compress v1.15.9
	github.com/niemeyer/pretty v0.0.0-20200227124842-a10e7caefd8e // indirect
	github.com/rogpeppe/retry v0.1.0
	github.com/tgulacsi/oracall v0.24.1
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.34.0 // indirect
	go.opentelemetry.io/otel/exporters/stdout/stdouttrace v1.9.0 // indirect
	golang.org/x/net v0.7.0
	golang.org/x/oauth2 v0.0.0-20210514164344-f6687ab2804c // indirect
	golang.org/x/time v0.0.0-20220722155302-e5dcc9cfc0b9 // indirect
	google.golang.org/genproto v0.0.0-20220819174105-e9f053255caa // indirect
	google.golang.org/grpc v1.48.0
	google.golang.org/protobuf v1.28.1
	gopkg.in/check.v1 v1.0.0-20200227125254-8fa46927fb4f // indirect
	gopkg.in/yaml.v3 v3.0.0-20210107192922-496545a6307b // indirect
)

//replace github.com/UNO-SOFT/grpcer => ../grpcer
