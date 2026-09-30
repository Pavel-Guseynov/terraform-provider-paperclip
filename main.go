package main

import (
	"context"
	"flag"
	"log"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

var (
	version string = "0.1.0"
)

func main() {
	var debug bool

	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	opts := providerserver.ServeOpts{
		Address: "git.guseynov.org/guseynov/paperclip",
		Debug:   debug,
	}

	err := providerserver.Serve(context.Background(), provider.New(version), opts)
	if err != nil {
		log.Fatal(err.Error())
	}
}
