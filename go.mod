module github.com/UNO-SOFT/soap-proxy

go 1.15

require (
	aqwari.net/xml v0.0.0-20181013063537-841f47b2a098
	github.com/UNO-SOFT/grpcer v0.6.4
	github.com/golang/protobuf v1.5.2
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-hclog v0.14.1 // indirect
	github.com/hashicorp/go-retryablehttp v0.6.8
	github.com/klauspost/compress v1.11.13
	github.com/mitchellh/mapstructure v1.4.1 // indirect
	github.com/tgulacsi/go v0.18.1
	github.com/tgulacsi/oracall v0.19.0
	go.opentelemetry.io/otel/exporters/stdout v0.10.0 // indirect
	golang.org/dl v0.0.0-20200724191219-e4fbcf8a7a81 // indirect
	golang.org/x/net v0.0.0-20210331212208-0fccb6fa2b5c
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c
	golang.org/x/sys v0.0.0-20210402192133-700132347e07 // indirect
	golang.org/x/text v0.3.6 // indirect
	google.golang.org/genproto v0.0.0-20210402141018-6c239bbf2bb1 // indirect
	google.golang.org/grpc v1.36.1
	google.golang.org/protobuf v1.26.0
	gopkg.in/inconshreveable/log15.v2 v2.0.0-20200109203555-b30bc20e4fd1 // indirect
)

//replace github.com/UNO-SOFT/grpcer => ../grpcer
