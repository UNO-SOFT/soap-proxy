module github.com/UNO-SOFT/soap-proxy

go 1.15

require (
	aqwari.net/xml v0.0.0-20181013063537-841f47b2a098
	github.com/UNO-SOFT/grpcer v0.6.1
	github.com/godror/godror v0.22.3 // indirect
	github.com/golang/protobuf v1.4.3
	github.com/hashicorp/go-hclog v0.14.1 // indirect
	github.com/hashicorp/go-retryablehttp v0.6.8
	github.com/klauspost/compress v1.10.0
	github.com/mitchellh/mapstructure v1.4.0 // indirect
	github.com/tgulacsi/go v0.13.4
	github.com/tgulacsi/oracall v0.15.7
	golang.org/x/net v0.0.0-20201110031124-69a78807bb2b
	golang.org/x/sync v0.0.0-20201020160332-67f06af15bc9
	golang.org/x/sys v0.0.0-20201201145000-ef89a241ccb3 // indirect
	golang.org/x/text v0.3.4 // indirect
	google.golang.org/genproto v0.0.0-20201201144952-b05cb90ed32e // indirect
	google.golang.org/grpc v1.33.2
	google.golang.org/protobuf v1.25.0
)

//replace github.com/UNO-SOFT/grpcer => ../grpcer
