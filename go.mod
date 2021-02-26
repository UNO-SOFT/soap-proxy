module github.com/UNO-SOFT/soap-proxy

go 1.15

require (
	aqwari.net/xml v0.0.0-20181013063537-841f47b2a098
	github.com/UNO-SOFT/grpcer v0.6.4
	github.com/golang/protobuf v1.4.3
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-hclog v0.14.1 // indirect
	github.com/hashicorp/go-retryablehttp v0.6.8
	github.com/klauspost/compress v1.11.8
	github.com/mitchellh/mapstructure v1.4.1 // indirect
	github.com/tgulacsi/go v0.14.0
	github.com/tgulacsi/oracall v0.17.4
	go.opentelemetry.io/otel/exporters/stdout v0.10.0 // indirect
	golang.org/dl v0.0.0-20200724191219-e4fbcf8a7a81 // indirect
	golang.org/x/net v0.0.0-20210226172049-e18ecbb05110
	golang.org/x/sync v0.0.0-20201207232520-09787c993a3a
	golang.org/x/sys v0.0.0-20210225134936-a50acf3fe073 // indirect
	golang.org/x/text v0.3.5 // indirect
	google.golang.org/genproto v0.0.0-20210226172003-ab064af71705 // indirect
	google.golang.org/grpc v1.36.0
	google.golang.org/protobuf v1.25.0
	gopkg.in/inconshreveable/log15.v2 v2.0.0-20200109203555-b30bc20e4fd1 // indirect
)

//replace github.com/UNO-SOFT/grpcer => ../grpcer
