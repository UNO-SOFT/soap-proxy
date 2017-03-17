// Copyright 2017 Tamás Gulácsi
//
//
//    Licensed under the Apache License, Version 2.0 (the "License");
//    you may not use this file except in compliance with the License.
//    You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
//    Unless required by applicable law or agreed to in writing, software
//    distributed under the License is distributed on an "AS IS" BASIS,
//    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//    See the License for the specific language governing permissions and
//    limitations under the License.

package main

import (
	"io/ioutil"
	"log"
	"os"

	"github.com/golang/protobuf/proto"
	protoc "github.com/golang/protobuf/protoc-gen-go/plugin"
)

func main() {
	data, err := ioutil.ReadAll(os.Stdin)
	if err != nil {
		log.Fatal(err)
	}

	var req protoc.CodeGeneratorRequest
	if err = proto.Unmarshal(data, &req); err != nil {
		log.Fatal(err)
	}

	var resp protoc.CodeGeneratorResponse
	if err := Generate(&resp, req); err != nil {
		log.Fatal(err)
	}
	data, err = proto.Marshal(&resp)
	if err != nil {
		log.Fatal(err)
	}
	if _, err = os.Stdout.Write(data); err != nil {
		log.Fatal(err)
	}
}

// vim: set fileencoding=utf-8 noet:
