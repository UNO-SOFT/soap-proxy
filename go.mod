module github.com/UNO-SOFT/soap-proxy

go 1.15

require (
	aqwari.net/xml v0.0.0-20181013063537-841f47b2a098
	github.com/UNO-SOFT/grpcer v0.7.0
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-hclog v0.14.1 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.0
	github.com/tgulacsi/go v0.18.3
	github.com/tgulacsi/oracall v0.19.0
	golang.org/x/net v0.0.0-20210610132358-84b48f89b13b
	google.golang.org/grpc v1.38.0
	google.golang.org/protobuf v1.26.0
)

//replace github.com/UNO-SOFT/grpcer => ../grpcer
